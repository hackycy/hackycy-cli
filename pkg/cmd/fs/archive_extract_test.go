package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateArchiveTreeRejectsEscapingLinksAndSpecialFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("special-file fixture is POSIX-specific")
	}
	root := filepath.Join(t.TempDir(), "staging")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "ok.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nested/ok.txt", filepath.Join(root, "good-link")); err != nil {
		t.Fatal(err)
	}
	if err := validateArchiveTree(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "good-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside", filepath.Join(root, "bad-link")); err != nil {
		t.Fatal(err)
	}
	if err := validateArchiveTree(root); !serviceErrorIs(err, "UNAVAILABLE") {
		t.Fatalf("validateArchiveTree() = %v, want unsafe link", err)
	}
}

func TestExtractArchivePublishesCollisionSafeDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	root := t.TempDir()
	archive := filepath.Join(root, "backup.zip")
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	executable := fakeSevenZip(t, `
case "$1" in
  l) printf '%s\n' '----------' 'Path = hello.txt' 'Size = 3' ;;
  x)
    for argument in "$@"; do
      case "$argument" in -o*) output=${argument#-o} ;; esac
    done
    printf 'hello' > "$output/hello.txt"
    printf '42%%\r'
    ;;
esac`, "", 0)
	var progress []int
	result, err := workspace.ExtractArchive(context.Background(), mustWorkspacePath(t, "backup.zip"), ArchiveExtractionOptions{
		Inspector: newSevenZipArchiveInspector(func() (string, error) { return executable, nil }),
		Capacity: func(string) (ArchiveCapacity, error) {
			return ArchiveCapacity{AvailableBytes: 1000, FreeEntries: 1000}, nil
		},
		Progress: func(value int) { progress = append(progress, value) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Destination.String() != "backup (1)" || result.Inspection != (ArchiveInspection{UncompressedBytes: 3, EntryCount: 1}) {
		t.Fatalf("ExtractArchive() = %#v", result)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "backup (1)", "hello.txt")); err != nil || string(contents) != "hello" {
		t.Fatalf("published contents = %q, %v", contents, err)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 100 {
		t.Fatalf("progress = %v", progress)
	}
	entries, err := workspace.List(mustWorkspacePath(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, ".extract-") {
			t.Fatalf("staging entry leaked into listing: %q", entry.Name)
		}
	}
}

func TestExtractArchiveFailsBeforePublicationWhenCapacityOrTreeValidationFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	for _, test := range []struct {
		name     string
		body     string
		capacity ArchiveCapacityProvider
	}{
		{
			name: "capacity",
			body: "case \"$1\" in l) printf '%s\\n' '----------' 'Path = hello.txt' 'Size = 3' ;; esac",
			capacity: func(string) (ArchiveCapacity, error) {
				return ArchiveCapacity{}, os.ErrPermission
			},
		},
		{
			name: "unsafe link",
			body: `
case "$1" in
  l) printf '%s\n' '----------' 'Path = outside' 'Size = 3' ;;
  x)
    for argument in "$@"; do case "$argument" in -o*) output=${argument#-o} ;; esac; done
    ln -s / "$output/outside"
    ;;
esac`,
			capacity: func(string) (ArchiveCapacity, error) {
				return ArchiveCapacity{AvailableBytes: 1000, FreeEntries: 1000}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "backup.zip"), []byte("archive"), 0o600); err != nil {
				t.Fatal(err)
			}
			executable := fakeSevenZip(t, test.body, "", 0)
			workspace := openReadOnlyWorkspace(t, root)
			_, err := workspace.ExtractArchive(context.Background(), mustWorkspacePath(t, "backup.zip"), ArchiveExtractionOptions{
				Inspector: newSevenZipArchiveInspector(func() (string, error) { return executable, nil }),
				Capacity:  test.capacity,
			})
			if test.name == "capacity" {
				if !errors.Is(err, ErrWorkspaceUnavailable) {
					t.Fatalf("ExtractArchive() error = %v, want capacity failure", err)
				}
			} else if !serviceErrorIs(err, "UNAVAILABLE") {
				t.Fatalf("ExtractArchive() error = %v, want unsafe tree failure", err)
			}
			entries, listErr := workspace.List(mustWorkspacePath(t, ""))
			if listErr != nil || len(entries) != 1 || entries[0].Name != "backup.zip" {
				t.Fatalf("staging or output remained: %#v, %v", entries, listErr)
			}
		})
	}
}
