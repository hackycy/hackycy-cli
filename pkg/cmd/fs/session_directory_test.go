package fs

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
)

func TestDefaultSessionDirectoryUsesTheLexicalAbsoluteCommandDirectory(t *testing.T) {
	stateRoot := t.TempDir()
	directory := filepath.Join(t.TempDir(), "browse")
	result, err := defaultSessionDirectory(directory, func() (string, error) { return stateRoot, nil })
	if err != nil {
		t.Fatalf("defaultSessionDirectory returned an error: %v", err)
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(abs))
	want := filepath.Join(stateRoot, "ycy", "fs", "sessions", fmt.Sprintf("%x", digest))
	if result != want {
		t.Fatalf("defaultSessionDirectory = %q, want %q", result, want)
	}
}
