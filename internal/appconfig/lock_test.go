package appconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcquireConfigLockSerializesIndependentCallers(t *testing.T) {
	store := lockTestStore(t)
	first, err := store.acquireConfigLock()
	if err != nil {
		t.Fatalf("first acquireConfigLock() returned an error: %v", err)
	}
	t.Cleanup(func() { _ = first.release() })

	type result struct {
		lock *configLock
		err  error
	}
	acquired := make(chan result, 1)
	go func() {
		lock, err := store.acquireConfigLock()
		acquired <- result{lock: lock, err: err}
	}()

	select {
	case result := <-acquired:
		t.Fatalf("second acquireConfigLock() completed before release: %#v", result)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.release(); err != nil {
		t.Fatalf("first release() returned an error: %v", err)
	}

	select {
	case result := <-acquired:
		if result.err != nil {
			t.Fatalf("second acquireConfigLock() returned an error: %v", result.err)
		}
		if result.lock == nil {
			t.Fatal("second acquireConfigLock() returned a nil lock")
		}
		if err := result.lock.release(); err != nil {
			t.Fatalf("second release() returned an error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second acquireConfigLock() did not complete after release")
	}
}

func TestAcquireConfigLockTimesOutForLiveOwner(t *testing.T) {
	store := lockTestStore(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	store.sleep = func(duration time.Duration) { now = now.Add(duration) }
	store.lockTimeout = 50 * time.Millisecond
	store.lockRetry = 25 * time.Millisecond
	store.processAlive = func(int) (bool, error) { return true, nil }
	writeLockOwnerFixture(t, store, configLockOwner{ID: "live", PID: 42, StartedAt: now.Format(time.RFC3339Nano)})

	_, err := store.acquireConfigLock()
	if err == nil || !strings.Contains(err.Error(), "Could not lock ycy configuration") {
		t.Fatalf("acquireConfigLock() error = %v", err)
	}
}

func TestAcquireConfigLockHonorsOwnerPublicationGraceThenRecoversStaleLocks(t *testing.T) {
	store := lockTestStore(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	store.sleep = func(duration time.Duration) { now = now.Add(duration) }
	store.lockTimeout = time.Second
	store.lockRetry = 25 * time.Millisecond
	store.lockGrace = 50 * time.Millisecond
	if err := os.MkdirAll(store.lockPath(), 0o700); err != nil {
		t.Fatalf("create ownerless lock directory: %v", err)
	}
	if err := os.Chtimes(store.lockPath(), now, now); err != nil {
		t.Fatalf("set ownerless lock time: %v", err)
	}

	lock, err := store.acquireConfigLock()
	if err != nil {
		t.Fatalf("acquireConfigLock() returned an error: %v", err)
	}
	if now.Before(time.Date(2026, time.January, 1, 0, 0, 0, 50*int(time.Millisecond), time.UTC)) {
		t.Fatalf("acquireConfigLock() skipped owner publication grace; time = %s", now)
	}
	if err := lock.release(); err != nil {
		t.Fatalf("release() returned an error: %v", err)
	}

	writeLockOwnerFixture(t, store, configLockOwner{ID: "dead", PID: 99, StartedAt: now.Format(time.RFC3339Nano)})
	store.processAlive = func(int) (bool, error) { return false, nil }
	lock, err = store.acquireConfigLock()
	if err != nil {
		t.Fatalf("acquireConfigLock() did not recover a dead owner: %v", err)
	}
	owner, ok := readConfigLockOwner(store.lockPath())
	if !ok || owner.ID == "dead" {
		t.Fatalf("recovered lock owner = %#v", owner)
	}
	if err := lock.release(); err != nil {
		t.Fatalf("release() returned an error: %v", err)
	}
}

func TestConfigLockReleaseRequiresMatchingOwner(t *testing.T) {
	store := lockTestStore(t)
	lock, err := store.acquireConfigLock()
	if err != nil {
		t.Fatalf("acquireConfigLock() returned an error: %v", err)
	}
	writeLockOwnerFixture(t, store, configLockOwner{ID: "replacement", PID: 9, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)})

	if err := lock.release(); err != nil {
		t.Fatalf("release() returned an error: %v", err)
	}
	if _, err := os.Stat(store.lockPath()); err != nil {
		t.Fatalf("replacement owner was removed: %v", err)
	}
}

func TestNativeProcessAliveRecognizesCurrentProcess(t *testing.T) {
	alive, err := nativeProcessAlive(os.Getpid())
	if err != nil {
		t.Fatalf("nativeProcessAlive() returned an error: %v", err)
	}
	if !alive {
		t.Fatal("nativeProcessAlive() did not recognize the current process")
	}
	if alive, err := nativeProcessAlive(0); err != nil || alive {
		t.Fatalf("nativeProcessAlive(0) = (%t, %v), want (false, nil)", alive, err)
	}
}

func lockTestStore(t *testing.T) *Store {
	t.Helper()
	store := testStore(t)
	store.lockTimeout = time.Second
	store.lockRetry = 5 * time.Millisecond
	store.lockGrace = time.Second
	return store
}

func writeLockOwnerFixture(t *testing.T, store *Store, owner configLockOwner) {
	t.Helper()
	if err := os.MkdirAll(store.lockPath(), 0o700); err != nil {
		t.Fatalf("create lock directory: %v", err)
	}
	contents, err := json.Marshal(owner)
	if err != nil {
		t.Fatalf("marshal lock owner: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.lockPath(), "owner.json"), contents, 0o600); err != nil {
		t.Fatalf("write lock owner: %v", err)
	}
}

func TestReadConfigLockOwnerRejectsMalformedOwner(t *testing.T) {
	store := lockTestStore(t)
	if err := os.MkdirAll(store.lockPath(), 0o700); err != nil {
		t.Fatalf("create lock directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.lockPath(), "owner.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed owner: %v", err)
	}
	if _, ok := readConfigLockOwner(store.lockPath()); ok {
		t.Fatal("readConfigLockOwner() accepted malformed owner")
	}

	if err := os.RemoveAll(store.lockPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove malformed lock: %v", err)
	}
}
