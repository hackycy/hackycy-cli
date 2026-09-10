package sevenzipruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/sevenzipmanifest"
)

func TestEnsureAtMaterializesAndRegeneratesVerifiedRuntime(t *testing.T) {
	root := t.TempDir()
	payload := Current()
	executable, err := EnsureAt(root, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantDirectory := filepath.Join(root, "ycy", "7zip", sevenzipmanifest.Version)
	if filepath.Dir(executable) != wantDirectory || filepath.Base(executable) != executableName(payload) || !validRuntime(wantDirectory, payload) {
		t.Fatalf("EnsureAt() executable = %q", executable)
	}
	if runtime.GOOS != "windows" {
		for _, file := range payload.Files {
			info, err := os.Stat(filepath.Join(wantDirectory, file.Metadata.Filename))
			if err != nil {
				t.Fatal(err)
			}
			wantMode := os.FileMode(0o600)
			if file.Metadata.Executable {
				wantMode = 0o700
			}
			if info.Mode().Perm() != wantMode {
				t.Fatalf("%s mode = %o, want %o", file.Metadata.Filename, info.Mode().Perm(), wantMode)
			}
		}
	}
	if err := os.WriteFile(executable, []byte("corrupt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if regenerated, err := EnsureAt(root, payload, nil); err != nil || regenerated != executable || !validRuntime(wantDirectory, payload) {
		t.Fatalf("EnsureAt() after corruption = %q, %v", regenerated, err)
	}
}

func TestEnsureAtSerializesConcurrentMaterialization(t *testing.T) {
	root := t.TempDir()
	payload := Current()
	results := make(chan string, 8)
	errors := make(chan error, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			executable, err := EnsureAt(root, payload, nil)
			results <- executable
			errors <- err
		})
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var executable string
	for result := range results {
		if executable == "" {
			executable = result
		} else if result != executable {
			t.Fatalf("concurrent paths = %q and %q", executable, result)
		}
	}
	if !validRuntime(filepath.Dir(executable), payload) {
		t.Fatal("concurrent runtime did not verify")
	}
}

func TestEnsureAtRegeneratesSymlinkedRuntimeFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symbolic-link privilege is not guaranteed in the test environment")
	}
	root := t.TempDir()
	payload := Current()
	executable, err := EnsureAt(root, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", executable); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAt(root, payload, nil); err != nil || !validRuntime(filepath.Dir(executable), payload) {
		t.Fatalf("EnsureAt() after symlink = %v", err)
	}
}

func TestEnsureAtUsesSourceFallbackOnlyWithoutEmbeddedPayload(t *testing.T) {
	lookups := make([]string, 0, 2)
	executable, err := EnsureAt(t.TempDir(), Payload{}, func(name string) (string, error) {
		lookups = append(lookups, name)
		if name == "7zz" {
			return "", os.ErrNotExist
		}
		return "/opt/tools/7z", nil
	})
	if err != nil || executable != "/opt/tools/7z" || len(lookups) != 2 {
		t.Fatalf("fallback = %q, %v, %#v", executable, err, lookups)
	}
	payload := Current()
	broken := append([]byte(nil), payload.Files[0].Bytes...)
	broken[0] ^= 1
	payload.Files[0].Bytes = broken
	called := false
	if _, err := EnsureAt(t.TempDir(), payload, func(string) (string, error) {
		called = true
		return "/opt/tools/7zz", nil
	}); err == nil || called {
		t.Fatalf("corrupt payload fallback: error = %v, called = %t", err, called)
	}
}
