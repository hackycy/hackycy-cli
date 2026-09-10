package connect

import (
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clientInstanceIdentityStub struct {
	id  string
	err error
}

func (identity clientInstanceIdentityStub) TunnelInstanceID(_ *url.URL, _ string) (string, error) {
	return identity.id, identity.err
}

func TestAcquireClientInstanceLocksOnlyItsStableInstanceDirectory(t *testing.T) {
	root := t.TempDir()
	firstID := clientTestInstanceID('a')
	secondID := clientTestInstanceID('b')
	first, err := AcquireClientInstance(ClientConfig{Server: clientTestServer(t), Token: "first-token"}, clientInstanceIdentityStub{id: firstID}, ClientInstanceOptions{StateRoot: root})
	if err != nil {
		t.Fatalf("AcquireClientInstance(first) error = %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })
	if got, want := first.StateDirectory, filepath.Join(root, firstID); got != want {
		t.Errorf("first state directory = %q, want %q", got, want)
	}

	if _, err := AcquireClientInstance(ClientConfig{Server: clientTestServer(t), Token: "first-token"}, clientInstanceIdentityStub{id: firstID}, ClientInstanceOptions{StateRoot: root}); !errors.Is(err, tunnelruntime.ErrInstanceActive) {
		t.Fatalf("same instance error = %v, want ErrInstanceActive", err)
	}

	second, err := AcquireClientInstance(ClientConfig{Server: clientTestServer(t), Token: "second-token"}, clientInstanceIdentityStub{id: secondID}, ClientInstanceOptions{StateRoot: root})
	if err != nil {
		t.Fatalf("AcquireClientInstance(second) error = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release(second) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".instances.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registry lock remains after acquisition: %v", err)
	}
}

func TestAcquireClientInstanceCleansOnlyExpiredUnlockedVersionedDirectories(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, time.August, 25, 0, 0, 0, 0, time.UTC)
	old := now.Add(-91 * 24 * time.Hour)
	recent := now.Add(-89 * 24 * time.Hour)
	currentID := clientTestInstanceID('a')
	expiredID := clientTestInstanceID('b')
	recentID := clientTestInstanceID('c')
	activeID := clientTestInstanceID('d')
	staleID := clientTestInstanceID('e')
	unknownDirectory := filepath.Join(root, "legacy-client-state")

	for _, directory := range []string{
		filepath.Join(root, expiredID),
		filepath.Join(root, recentID),
		filepath.Join(root, activeID),
		filepath.Join(root, staleID, ".lock"),
		unknownDirectory,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create fixture directory %q: %v", directory, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, staleID, ".lock", "owner.json"), []byte(`{"id":"stale","pid":2147483647,"startedAt":"2020-01-01T00:00:00.000Z","stateDirectory":"stale"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write stale owner: %v", err)
	}
	active, err := tunnelruntime.AcquireStateDirectoryLock(filepath.Join(root, activeID))
	if err != nil {
		t.Fatalf("acquire active fixture lock: %v", err)
	}
	t.Cleanup(func() { _ = active.Release() })
	for _, directory := range []string{filepath.Join(root, expiredID), filepath.Join(root, activeID), filepath.Join(root, staleID), unknownDirectory} {
		if err := os.Chtimes(directory, old, old); err != nil {
			t.Fatalf("age %q: %v", directory, err)
		}
	}
	if err := os.Chtimes(filepath.Join(root, recentID), recent, recent); err != nil {
		t.Fatalf("age recent directory: %v", err)
	}

	instance, err := AcquireClientInstance(ClientConfig{Server: clientTestServer(t), Token: "current-token"}, clientInstanceIdentityStub{id: currentID}, ClientInstanceOptions{StateRoot: root, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("AcquireClientInstance() error = %v", err)
	}
	if err := instance.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	for _, test := range []struct {
		name   string
		path   string
		exists bool
	}{
		{name: "expired", path: filepath.Join(root, expiredID), exists: false},
		{name: "stale", path: filepath.Join(root, staleID), exists: false},
		{name: "recent", path: filepath.Join(root, recentID), exists: true},
		{name: "active", path: filepath.Join(root, activeID), exists: true},
		{name: "unknown", path: unknownDirectory, exists: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := os.Stat(test.path)
			if test.exists && err != nil {
				t.Fatalf("%s removed: %v", test.name, err)
			}
			if !test.exists && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s still exists: %v", test.name, err)
			}
		})
	}
}

func TestClientAppliedStateRoundTripsAtomicallyAndIgnoresInvalidCache(t *testing.T) {
	directory := t.TempDir()
	state := ClientAppliedState{
		ClientDesiredConfiguration: ClientDesiredConfiguration{
			AdvertisedFRPHost: "frp.example.test",
			AdvertisedFRPPort: 7000,
			InternalFRPToken:  "internal-token",
			Snapshot: tunnelruntime.TunnelSnapshot{
				ClientKey: "client-id",
				Revision:  4,
			},
		},
		Revision: 4,
	}
	if err := WriteClientAppliedState(directory, state); err != nil {
		t.Fatalf("WriteClientAppliedState() error = %v", err)
	}
	contents, err := os.ReadFile(clientAppliedStatePath(directory))
	if err != nil {
		t.Fatalf("read applied state: %v", err)
	}
	if !strings.HasSuffix(string(contents), "\n") || !strings.Contains(string(contents), "\n  \"revision\": 4\n") {
		t.Fatalf("persisted state = %q, want pretty JSON plus newline", contents)
	}
	assertClientPrivateFile(t, clientAppliedStatePath(directory), 0o600)
	loaded, ok := ReadClientAppliedState(directory)
	if !ok || loaded == nil || loaded.Revision != 4 || loaded.Snapshot.Revision != 4 || loaded.InternalFRPToken != "internal-token" {
		t.Fatalf("ReadClientAppliedState() = (%#v, %t)", loaded, ok)
	}

	if err := os.WriteFile(clientAppliedStatePath(directory), []byte(`{"revision":5,"snapshot":{"revision":4}}`), 0o600); err != nil {
		t.Fatalf("write mismatched cache: %v", err)
	}
	if loaded, ok := ReadClientAppliedState(directory); ok || loaded != nil {
		t.Fatalf("ReadClientAppliedState(mismatched) = (%#v, %t), want absent", loaded, ok)
	}
	if err := os.WriteFile(clientAppliedStatePath(directory), []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write malformed cache: %v", err)
	}
	if loaded, ok := ReadClientAppliedState(directory); ok || loaded != nil {
		t.Fatalf("ReadClientAppliedState(malformed) = (%#v, %t), want absent", loaded, ok)
	}
}

func TestClientStateRootUsesPlatformStateRoot(t *testing.T) {
	root, err := clientStateRoot(func(name string) string {
		if name == "XDG_STATE_HOME" {
			return "/custom-state"
		}
		return ""
	}, func() (string, error) {
		return "/home/example", nil
	}, "linux")
	if err != nil {
		t.Fatalf("clientStateRoot() error = %v", err)
	}
	if got, want := root, filepath.Join("/custom-state", "ycy", "tunnel", "client"); got != want {
		t.Fatalf("clientStateRoot() = %q, want %q", got, want)
	}
}

func clientTestInstanceID(character rune) string {
	return "v1_" + strings.Repeat(string(character), 43)
}

func clientTestServer(t *testing.T) *url.URL {
	t.Helper()
	server, err := normalizeControlPlaneURL("https://control.example.test")
	if err != nil {
		t.Fatalf("normalize control server: %v", err)
	}
	return server
}
