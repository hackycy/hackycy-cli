package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/sevenzipmanifest"
)

func TestValidPayloadAcceptsWindowsExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows file modes do not encode executable bits")
	}

	directory := t.TempDir()
	contents := []byte("windows payload")
	artifact := sevenzipmanifest.Artifact{Files: []sevenzipmanifest.File{{
		Filename:   "7z.exe",
		SHA256:     digest(contents),
		Executable: true,
	}}}
	if err := os.WriteFile(filepath.Join(directory, "7z.exe"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if !validPayload(directory, artifact) {
		t.Fatal("valid Windows executable payload was rejected")
	}
}
