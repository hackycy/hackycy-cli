package ycycmd

import (
	"os"
	"strings"
	"testing"
)

func TestRunReturnsVersionOutcomeWithoutExiting(t *testing.T) {
	input, output, diagnostics := processTestFiles(t)

	if code := run("1.2.3", []string{"--version"}, input, output, diagnostics); code != 0 {
		t.Fatalf("run exit code = %d, want 0", code)
	}
	if got, err := os.ReadFile(output.Name()); err != nil || string(got) != "1.2.3\n" {
		t.Fatalf("version output = %q, %v", got, err)
	}
	if got, err := os.ReadFile(diagnostics.Name()); err != nil || len(got) != 0 {
		t.Fatalf("version diagnostics = %q, %v", got, err)
	}
}

func TestRunReturnsRootErrorOutcomeAndKeepsProcessOwnershipAtCaller(t *testing.T) {
	input, output, diagnostics := processTestFiles(t)

	if code := run("1.2.3", []string{"unknown"}, input, output, diagnostics); code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
	got, err := os.ReadFile(diagnostics.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "error: unknown command 'unknown'") {
		t.Fatalf("diagnostics = %q", got)
	}
}

func processTestFiles(t *testing.T) (input, output, diagnostics *os.File) {
	t.Helper()
	files := make([]*os.File, 3)
	for index, name := range []string{"stdin", "stdout", "stderr"} {
		file, err := os.CreateTemp(t.TempDir(), name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		files[index] = file
		t.Cleanup(func() { _ = file.Close() })
	}
	return files[0], files[1], files[2]
}
