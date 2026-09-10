package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareArchiveStagingCopiesContainedSourceAndCleansUp(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "archives"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "archives", "source.zip"), []byte("archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	staging, err := workspace.prepareArchiveStaging(mustWorkspacePath(t, "archives/source.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(staging.source.baseName(), ".extract-") || !strings.HasSuffix(staging.source.baseName(), ".tmp.source") {
		t.Fatalf("source staging name = %q", staging.source.String())
	}
	if bytes, err := os.ReadFile(staging.sourcePath()); err != nil || string(bytes) != "archive bytes" {
		t.Fatalf("staged source = %q, %v", bytes, err)
	}
	if info, err := os.Stat(staging.destinationPath()); err != nil || !info.IsDir() {
		t.Fatalf("staged destination = %v, %v", info, err)
	}
	entries, err := workspace.List(mustWorkspacePath(t, "archives"))
	if err != nil || len(entries) != 1 || entries[0].Name != "source.zip" {
		t.Fatalf("List() includes staging = %#v, %v", entries, err)
	}
	if err := staging.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staging.sourcePath()); !os.IsNotExist(err) {
		t.Fatalf("source staging remains: %v", err)
	}
	if _, err := os.Stat(staging.destinationPath()); !os.IsNotExist(err) {
		t.Fatalf("destination staging remains: %v", err)
	}
}

func TestPrepareArchiveStagingRefusesRootAndUnsupportedNames(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	for _, path := range []string{"", "notes.txt"} {
		if _, err := workspace.prepareArchiveStaging(mustWorkspacePath(t, path)); !serviceErrorIs(err, "INVALID_ARCHIVE") {
			t.Fatalf("prepareArchiveStaging(%q) error = %v", path, err)
		}
	}
}
