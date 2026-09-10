//go:build darwin || linux

package diff

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenComparisonFileRejectsSymbolicLinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if file, err := openComparisonFile(link); err == nil {
		_ = file.Close()
		t.Fatal("openComparisonFile() accepted a symbolic link")
	}
}
