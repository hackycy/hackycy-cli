package filesession

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenCreatesOnlyGoOwnedStateBelowTheBaseDirectory(t *testing.T) {
	base := t.TempDir()
	unrelated := filepath.Join(base, "operator-note.json")
	contents := []byte("operator managed")
	if err := os.WriteFile(unrelated, contents, 0o600); err != nil {
		t.Fatalf("write unrelated base entry: %v", err)
	}

	manager, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	wantDirectory := filepath.Join(base, goStateDirectoryName)
	if manager.Directory() != wantDirectory {
		t.Fatalf("Directory() = %q, want %q", manager.Directory(), wantDirectory)
	}
	if got, err := os.ReadFile(unrelated); err != nil || string(got) != string(contents) {
		t.Fatalf("unrelated base entry = (%q, %v), want unchanged", got, err)
	}
	assertPrivatePath(t, manager.Directory(), 0o700)
	assertPrivatePath(t, filepath.Join(manager.Directory(), sessionKeyFileName), 0o600)
	lockPath := filepath.Join(manager.Directory(), sessionLockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("stat session lock: %v", err)
	}
	assertPrivatePath(t, lockPath, 0o600)
}

func TestManagerUsesOneLiveLockAndReleasesOnlyItsOwnLock(t *testing.T) {
	base := t.TempDir()
	first, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if _, err := Open(Options{BaseDirectory: base}); !errors.Is(err, ErrDirectoryInUse) {
		t.Fatalf("second Open() error = %v, want ErrDirectoryInUse", err)
	}
	lockPath := filepath.Join(first.Directory(), sessionLockFileName)
	if err := writeLockFixture(lockPath, lockOwner{ID: "replacement", PID: os.Getpid()}); err != nil {
		t.Fatalf("replace lock owner: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("replacement lock was removed: %v", err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove replacement lock: %v", err)
	}
	second, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("Open() after release error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
}

func TestOpenRecoversAStaleGoOwnedLock(t *testing.T) {
	base := t.TempDir()
	directory := filepath.Join(base, goStateDirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	lockPath := filepath.Join(directory, sessionLockFileName)
	if err := writeLockFixture(lockPath, lockOwner{ID: "stale", PID: math.MaxInt32}); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	manager, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	owner, valid := readLockOwner(lockPath)
	if !valid || owner.ID == "stale" || owner.PID != os.Getpid() {
		t.Fatalf("lock owner = %#v, want current owner", owner)
	}
}

func TestCredentialRevisionPersistsOnlyThroughTheGoOwnedKey(t *testing.T) {
	base := t.TempDir()
	first, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	firstRevision, err := first.CredentialRevision("alice\x00password")
	if err != nil {
		t.Fatalf("first CredentialRevision() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	secondRevision, err := second.CredentialRevision("alice\x00password")
	if err != nil {
		t.Fatalf("second CredentialRevision() error = %v", err)
	}
	if firstRevision != secondRevision || len(secondRevision) != 43 {
		t.Fatalf("credential revisions = (%q, %q), want stable 43-byte base64url value", firstRevision, secondRevision)
	}
	if _, err := second.CredentialRevision("alice\x00replacement"); err != nil {
		t.Fatalf("CredentialRevision() with replacement input error = %v", err)
	}
}

func TestOpenRejectsAnInvalidGoOwnedKey(t *testing.T) {
	base := t.TempDir()
	first, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	directory := first.Directory()
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, sessionKeyFileName), make([]byte, 31), 0o600); err != nil {
		t.Fatalf("corrupt key: %v", err)
	}
	if _, err := Open(Options{BaseDirectory: base}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Open() error = %v, want ErrInvalidKey", err)
	}
}

func TestManagerIssuesRefreshesAndRevokesOpaqueSessions(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, time.August, 24, 1, 0, 0, 0, time.UTC)
	manager, err := Open(Options{
		BaseDirectory: base,
		IdleLifetime:  time.Hour,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	revision, err := manager.CredentialRevision("alice\x00password")
	if err != nil {
		t.Fatalf("CredentialRevision() error = %v", err)
	}
	issued, err := manager.Issue("alice", revision)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(issued.Token) || issued.Subject != "alice" || !issued.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("issued session = %#v", issued)
	}
	recordPath := filepath.Join(manager.Directory(), tokenHash(issued.Token)+".json")
	recordContents, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if strings.Contains(string(recordContents), issued.Token) || strings.Contains(filepath.Base(recordPath), issued.Token) {
		t.Fatal("raw session token was persisted")
	}
	var record sessionRecord
	if err := json.Unmarshal(recordContents, &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if record.Version != 1 || record.TokenHash != tokenHash(issued.Token) || record.Subject != "alice" || record.Revision != revision || record.CreatedAt != "2026-08-24T01:00:00.000Z" || record.LastAccessAt != record.CreatedAt || record.ExpiresAt != "2026-08-24T02:00:00.000Z" {
		t.Fatalf("record = %#v", record)
	}
	assertPrivatePath(t, recordPath, 0o600)

	now = now.Add(15 * time.Minute)
	resumed, err := manager.Resume(issued.Token, func(subject string) string {
		if subject != "alice" {
			t.Fatalf("revision requested for %q", subject)
		}
		return revision
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed == nil || !resumed.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("resumed session = %#v", resumed)
	}
	if err := manager.Revoke(issued.Token); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := os.Stat(recordPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record after revocation = %v, want absent", err)
	}
	resumed, err = manager.Resume(issued.Token, func(string) string { return revision })
	if err != nil || resumed != nil {
		t.Fatalf("Resume(revoked) = (%#v, %v), want (nil, nil)", resumed, err)
	}
}

func TestManagerRevokesAChangedCredentialRevision(t *testing.T) {
	manager, err := Open(Options{BaseDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	issued, err := manager.Issue("alice", "original")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	resumed, err := manager.Resume(issued.Token, func(string) string { return "replacement" })
	if err != nil || resumed != nil {
		t.Fatalf("Resume(changed revision) = (%#v, %v), want (nil, nil)", resumed, err)
	}
	if _, err := os.Stat(filepath.Join(manager.Directory(), tokenHash(issued.Token)+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed-credential record = %v, want absent", err)
	}
}

func TestManagerResumeAllowsCredentialRevisionCallbacks(t *testing.T) {
	manager, err := Open(Options{BaseDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	revision, err := manager.CredentialRevision("alice\x00password")
	if err != nil {
		t.Fatalf("CredentialRevision() error = %v", err)
	}
	issued, err := manager.Issue("alice", revision)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	resumed, err := manager.Resume(issued.Token, func(subject string) string {
		if subject != "alice" {
			t.Fatalf("revision subject = %q", subject)
		}
		current, revisionErr := manager.CredentialRevision("alice\x00password")
		if revisionErr != nil {
			t.Fatalf("CredentialRevision() in callback error = %v", revisionErr)
		}
		return current
	})
	if err != nil || resumed == nil || resumed.Subject != "alice" {
		t.Fatalf("Resume() = (%#v, %v)", resumed, err)
	}
}

func TestOpenRestoresOnlyGoOwnedRecordsAndRefreshesTheirIdleDeadline(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, time.August, 24, 3, 0, 0, 0, time.UTC)
	options := Options{
		BaseDirectory: base,
		IdleLifetime:  time.Hour,
		Now:           func() time.Time { return now },
	}
	first, err := Open(options)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	issued, err := first.Issue("alice", "revision")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	now = now.Add(30 * time.Minute)
	second, err := Open(options)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	resumed, err := second.Resume(issued.Token, func(subject string) string {
		if subject != "alice" {
			t.Fatalf("revision subject = %q", subject)
		}
		return "revision"
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed == nil || !resumed.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("resumed session = %#v", resumed)
	}
}

func TestOpenPrunesExpiredInvalidAndInterruptedGoRecords(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, time.August, 24, 4, 0, 0, 0, time.UTC)
	options := Options{
		BaseDirectory: base,
		IdleLifetime:  time.Minute,
		Now:           func() time.Time { return now },
	}
	first, err := Open(options)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	issued, err := first.Issue("alice", "revision")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	directory := first.Directory()
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write invalid record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "interrupted.json.tmp-123"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("write interrupted record: %v", err)
	}

	now = now.Add(2 * time.Minute)
	second, err := Open(options)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if resumed, err := second.Resume(issued.Token, func(string) string { return "revision" }); err != nil || resumed != nil {
		t.Fatalf("Resume(expired) = (%#v, %v), want (nil, nil)", resumed, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read state directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") || strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("unpruned record entry = %q", entry.Name())
		}
	}
}

func TestOpenReprotectsValidGoRecords(t *testing.T) {
	base := t.TempDir()
	first, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	issued, err := first.Issue("alice", "revision")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	recordPath := filepath.Join(first.Directory(), tokenHash(issued.Token)+".json")
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := os.Chmod(recordPath, 0o644); err != nil {
		t.Fatalf("loosen record permission: %v", err)
	}
	second, err := Open(Options{BaseDirectory: base})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	assertPrivatePath(t, recordPath, 0o600)
}

func TestManagerEnforcesDefaultSessionLRULimits(t *testing.T) {
	base := t.TempDir()
	now := time.Date(2026, time.August, 24, 5, 0, 0, 0, time.UTC)
	manager, err := Open(Options{
		BaseDirectory: base,
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	const revision = "revision"
	revisionFor := func(string) string { return revision }
	var aliceTokens []string
	firstEvicted := make(chan struct{}, 1)
	for index := range 9 {
		issued, err := manager.Issue("alice", revision)
		if err != nil {
			t.Fatalf("Issue(alice, %d) error = %v", index, err)
		}
		aliceTokens = append(aliceTokens, issued.Token)
		if index == 0 {
			manager.Observe(issued.Token, func() { firstEvicted <- struct{}{} })
		}
		now = now.Add(time.Second)
	}
	if resumed, err := manager.Resume(aliceTokens[0], revisionFor); err != nil || resumed != nil {
		t.Fatalf("Resume(oldest subject session) = (%#v, %v), want (nil, nil)", resumed, err)
	}
	select {
	case <-firstEvicted:
	default:
		t.Fatal("per-subject LRU eviction did not notify its observer")
	}
	if _, err := os.Stat(filepath.Join(manager.Directory(), tokenHash(aliceTokens[1])+".json")); err != nil {
		t.Fatalf("newest retained subject session is absent: %v", err)
	}

	for index := range 120 {
		if _, err := manager.Issue("account-"+strconv.Itoa(index), revision); err != nil {
			t.Fatalf("Issue(total %d) error = %v", index, err)
		}
		now = now.Add(time.Second)
	}
	if _, err := manager.Issue("overflow", revision); err != nil {
		t.Fatalf("Issue(total overflow) error = %v", err)
	}
	if resumed, err := manager.Resume(aliceTokens[1], revisionFor); err != nil || resumed != nil {
		t.Fatalf("Resume(oldest total session) = (%#v, %v), want (nil, nil)", resumed, err)
	}
	if len(manager.sessions) != 128 {
		t.Fatalf("retained session count = %d, want 128", len(manager.sessions))
	}
}

func TestManagerNotifiesObserversForRevocationAndCancellation(t *testing.T) {
	manager, err := Open(Options{BaseDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	explicit, err := manager.Issue("alice", "original")
	if err != nil {
		t.Fatalf("Issue(explicit) error = %v", err)
	}
	explicitNotifications := make(chan struct{}, 1)
	manager.Observe(explicit.Token, func() { explicitNotifications <- struct{}{} })
	if err := manager.Revoke(explicit.Token); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	select {
	case <-explicitNotifications:
	default:
		t.Fatal("explicit revocation did not notify its observer")
	}

	revisionChanged, err := manager.Issue("alice", "original")
	if err != nil {
		t.Fatalf("Issue(revision changed) error = %v", err)
	}
	revisionNotifications := make(chan struct{}, 1)
	manager.Observe(revisionChanged.Token, func() { revisionNotifications <- struct{}{} })
	if resumed, err := manager.Resume(revisionChanged.Token, func(string) string { return "replacement" }); err != nil || resumed != nil {
		t.Fatalf("Resume(changed revision) = (%#v, %v), want (nil, nil)", resumed, err)
	}
	select {
	case <-revisionNotifications:
	default:
		t.Fatal("credential revision revocation did not notify its observer")
	}

	cancelled, err := manager.Issue("alice", "original")
	if err != nil {
		t.Fatalf("Issue(cancelled observer) error = %v", err)
	}
	cancelledNotifications := make(chan struct{}, 1)
	cancel := manager.Observe(cancelled.Token, func() { cancelledNotifications <- struct{}{} })
	cancel()
	if err := manager.Revoke(cancelled.Token); err != nil {
		t.Fatalf("Revoke(cancelled observer) error = %v", err)
	}
	select {
	case <-cancelledNotifications:
		t.Fatal("cancelled observer was notified")
	default:
	}
}

func TestManagerExpiresActiveSessionsAndNotifiesObservers(t *testing.T) {
	manager, err := Open(Options{
		BaseDirectory: t.TempDir(),
		IdleLifetime:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	issued, err := manager.Issue("alice", "revision")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	expired := make(chan struct{}, 1)
	manager.Observe(issued.Token, func() { expired <- struct{}{} })
	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("active session did not expire")
	}
	if resumed, err := manager.Resume(issued.Token, func(string) string { return "revision" }); err != nil || resumed != nil {
		t.Fatalf("Resume(expired) = (%#v, %v), want (nil, nil)", resumed, err)
	}
}

func TestManagerSerializesConcurrentResumes(t *testing.T) {
	manager, err := Open(Options{BaseDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	issued, err := manager.Issue("alice", "revision")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	const resumes = 32
	start := make(chan struct{})
	errs := make(chan error, resumes)
	var group sync.WaitGroup
	for range resumes {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			resumed, err := manager.Resume(issued.Token, func(string) string { return "revision" })
			if err != nil {
				errs <- err
				return
			}
			if resumed == nil || resumed.Subject != "alice" {
				errs <- errors.New("concurrent Resume returned no active session")
			}
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	recordPath := filepath.Join(manager.Directory(), tokenHash(issued.Token)+".json")
	contents, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read concurrently refreshed record: %v", err)
	}
	if _, valid := parseSessionRecord(contents, recordPath); !valid {
		t.Fatal("concurrently refreshed record is invalid")
	}
	entries, err := os.ReadDir(manager.Directory())
	if err != nil {
		t.Fatalf("read session directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("concurrent refresh left candidate %q", entry.Name())
		}
	}
}

func writeLockFixture(path string, owner lockOwner) error {
	contents, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(contents, '\n'), 0o600)
}
