package sevenzipmanifest

import (
	"strings"
	"testing"
)

func TestAllDefinesCompletePinnedPayloads(t *testing.T) {
	artifacts := All()
	if len(artifacts) != 6 {
		t.Fatalf("All() returned %d artifacts", len(artifacts))
	}
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		if seen[artifact.Target] || artifact.Target == "" || artifact.Asset == "" || artifact.Format == "" || !sha256String(artifact.SHA256) {
			t.Fatalf("invalid artifact %#v", artifact)
		}
		seen[artifact.Target] = true
		if len(artifact.Files) < 2 {
			t.Fatalf("artifact %s files = %#v", artifact.Target, artifact.Files)
		}
		for _, file := range artifact.Files {
			if file.SourceName == "" || file.Filename == "" || !sha256String(file.SHA256) {
				t.Fatalf("artifact %s file = %#v", artifact.Target, file)
			}
		}
	}
	for _, target := range []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"} {
		if !seen[target] {
			t.Fatalf("missing target %s", target)
		}
	}
}

func TestForCurrentTarget(t *testing.T) {
	artifact, found := Current()
	if !found || artifact.Target == "" {
		t.Fatalf("Current() = %#v, %t", artifact, found)
	}
}

func sha256String(value string) bool {
	if len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}
