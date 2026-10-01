package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreCommitGofmt(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	controller, err := New(repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	lefthook, err := controller.lefthookPath(repository)
	if err != nil {
		t.Skip("pinned Lefthook is unavailable; run make bootstrap")
	}

	tests := []struct {
		name        string
		files       map[string]string
		wantFailure bool
		wantOutput  string
	}{
		{
			name: "formatted files with spaces",
			files: map[string]string{
				"source/formatted.go":        "package fixture\n\nvar value = 1\n",
				"source/file with spaces.go": "package fixture\n\nvar other = 2\n",
			},
		},
		{
			name:        "unformatted file",
			files:       map[string]string{"source/unformatted.go": "package fixture\nvar value=1\n"},
			wantFailure: true,
			wantOutput:  "source/unformatted.go",
		},
		{
			name:        "invalid Go file",
			files:       map[string]string{"source/invalid.go": "invalid Go source\n"},
			wantFailure: true,
			wantOutput:  "expected 'package'",
		},
		{
			name:       "no staged Go files",
			files:      map[string]string{"README.md": "fixture\n"},
			wantOutput: "no files for inspection",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := testRepository(t)
			for _, path := range []string{"lefthook.yml", "tools/hookctl/check-gofmt.sh"} {
				contents, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(path)))
				if err != nil {
					t.Fatal(err)
				}
				writePreCommitFixture(t, root, path, contents)
			}
			for path, contents := range test.files {
				writePreCommitFixture(t, root, path, []byte(contents))
				runGit(t, root, "add", "--", path)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, lefthook, "run", "pre-commit", "--command", "gofmt", "--no-tty")
			command.Dir = root
			output, err := command.CombinedOutput()
			if (err != nil) != test.wantFailure {
				t.Fatalf("pre-commit error = %v, want failure %t\n%s", err, test.wantFailure, output)
			}
			if !strings.Contains(string(output), test.wantOutput) {
				t.Fatalf("pre-commit output does not contain %q:\n%s", test.wantOutput, output)
			}
			for path, contents := range test.files {
				actual, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
				if err != nil || !bytes.Equal(actual, []byte(contents)) {
					t.Fatalf("pre-commit modified %s: %q, %v", path, actual, err)
				}
			}
		})
	}
}

func writePreCommitFixture(t *testing.T, root, path string, contents []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}
