package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/updater"
)

func TestWriteChecksumManifestUsesEveryFixedArtifactExactlyOnce(t *testing.T) {
	directory := t.TempDir()
	expected, err := expectedArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range expected {
		if err := os.WriteFile(filepath.Join(directory, artifact.name), []byte(artifact.name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := writeChecksumManifest(directory); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(directory, updater.ChecksumManifest))
	if err != nil {
		t.Fatal(err)
	}
	checksums, err := updater.ParseChecksumManifest(string(contents))
	if err != nil {
		t.Fatal(err)
	}
	if len(checksums) != len(expected) {
		t.Fatalf("checksum entries = %d, want %d", len(checksums), len(expected))
	}
	for _, artifact := range expected {
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(artifact.name)))
		if checksums[artifact.name] != want {
			t.Fatalf("checksum for %s = %q, want %q", artifact.name, checksums[artifact.name], want)
		}
	}
}

func TestWriteChecksumManifestRejectsIncompleteAndUnexpectedArtifactSets(t *testing.T) {
	expected, err := expectedArtifacts()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("missing", func(t *testing.T) {
		directory := t.TempDir()
		for index, artifact := range expected {
			if index == 0 {
				continue
			}
			if err := os.WriteFile(filepath.Join(directory, artifact.name), []byte(artifact.name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeChecksumManifest(directory); err == nil || !strings.Contains(err.Error(), "missing artifact") {
			t.Fatalf("writeChecksumManifest error = %v, want missing artifact", err)
		}
	})

	t.Run("unexpected", func(t *testing.T) {
		directory := t.TempDir()
		for _, artifact := range expected {
			if err := os.WriteFile(filepath.Join(directory, artifact.name), []byte(artifact.name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(directory, "extra"), []byte("extra"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeChecksumManifest(directory); err == nil || !strings.Contains(err.Error(), "unexpected artifact") {
			t.Fatalf("writeChecksumManifest error = %v, want unexpected artifact", err)
		}
	})
}

func TestVerifyChecksumManifestRejectsChangedArtifact(t *testing.T) {
	directory := t.TempDir()
	expected, err := expectedArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range expected {
		if err := os.WriteFile(filepath.Join(directory, artifact.name), []byte(artifact.name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeChecksumManifest(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, expected[0].name), []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifacts, err := releaseArtifacts(directory, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksumManifest(directory, artifacts); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("verifyChecksumManifest error = %v, want checksum mismatch", err)
	}
}
