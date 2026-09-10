//go:build acceptance

package acceptance

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRMStandaloneBinary(t *testing.T) {
	repository := repositoryRoot(t)
	binary := standaloneBinaryOutputPath(filepath.Join(t.TempDir(), "ycy"))
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	build.Dir = repository
	build.Env = environmentWith(map[string]string{
		"CGO_ENABLED": "0",
		"GOTOOLCHAIN": "go1.26.7",
		"GOWORK":      "off",
	})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}

	root := newStandaloneRMRoot(t)
	workingDirectory := filepath.Join(root, "project")
	if err := os.Mkdir(workingDirectory, 0o700); err != nil {
		t.Fatalf("create working directory: %v", err)
	}
	environment := environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""})

	explicitFile := writeStandaloneRMFile(t, workingDirectory, "file.txt")
	explicitDirectory := filepath.Join(workingDirectory, "directory")
	if err := os.MkdirAll(filepath.Join(explicitDirectory, "nested"), 0o700); err != nil {
		t.Fatalf("create explicit directory: %v", err)
	}
	writeStandaloneRMFile(t, explicitDirectory, "nested/file.txt")
	output, err := runRMStandalone(binary, workingDirectory, environment, "", "rm", "--force", "file.txt", "directory", "missing")
	if err != nil || !strings.Contains(string(output), "not found, skipping:") || strings.Contains(string(output), "Delete 2 items?") || !strings.Contains(string(output), "Deleted 2 items") || !strings.Contains(string(output), "Done!") {
		t.Fatalf("explicit rm = (%v, %q)", err, output)
	}
	for _, target := range []string{explicitFile, explicitDirectory} {
		if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("explicit target %s = %v, want missing", target, statErr)
		}
	}

	cancelledTarget := writeStandaloneRMFile(t, workingDirectory, "cancelled.txt")
	output, err = runRMStandalone(binary, workingDirectory, environment, "\n", "rm", "cancelled.txt")
	if err == nil || string(output) != "error: rm requires an interactive terminal\n" {
		t.Fatalf("redirected confirmation rm = (%v, %q)", err, output)
	}
	if _, statErr := os.Stat(cancelledTarget); statErr != nil {
		t.Fatalf("cancelled rm changed target: %v", statErr)
	}

	forcedTarget := writeStandaloneRMFile(t, workingDirectory, "forced.txt")
	output, err = runRMStandalone(binary, workingDirectory, environment, "", "rm", "--force", "forced.txt")
	if err != nil || strings.Contains(string(output), "Delete 1 item?") || !strings.Contains(string(output), "Done!") {
		t.Fatalf("forced rm = (%v, %q)", err, output)
	}
	if _, statErr := os.Stat(forcedTarget); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("forced target = %v, want missing", statErr)
	}

	output, err = runRMStandalone(binary, workingDirectory, environment, "", "rm", "still-missing")
	if err != nil || !strings.Contains(string(output), "No valid paths to delete.") {
		t.Fatalf("redirected no-target rm = (%v, %q)", err, output)
	}

	smartDist := filepath.Join(workingDirectory, "dist")
	if err := os.MkdirAll(smartDist, 0o700); err != nil {
		t.Fatalf("create smart dist: %v", err)
	}
	output, err = runRMStandalone(binary, workingDirectory, environment, "1\n", "rm")
	if err == nil || string(output) != "error: rm requires an interactive terminal\n" {
		t.Fatalf("redirected smart rm = (%v, %q)", err, output)
	}
	if _, statErr := os.Stat(smartDist); statErr != nil {
		t.Fatalf("redirected smart rm changed dist: %v", statErr)
	}

	helpOutput, err := runRMStandalone(binary, workingDirectory, environment, "", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "rm") {
		t.Fatalf("root help = (%v, %q)", err, helpOutput)
	}
}

func runRMStandalone(binary, directory string, environment []string, input string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = environment
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}

func withRMWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func writeStandaloneRMFile(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("contents"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func newStandaloneRMRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("get user home: %v", err)
	}
	for _, forbidden := range []string{workingDirectory, home, repositoryRoot(t)} {
		if standaloneRMPathsOverlap(root, forbidden) {
			t.Fatalf("disposable root %s overlaps forbidden path %s", root, forbidden)
		}
	}
	return root
}

func standaloneRMPathsOverlap(first, second string) bool {
	return standaloneRMPathContains(first, second) || standaloneRMPathContains(second, first)
}

func standaloneRMPathContains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}

type panicRMReader struct{}

func (panicRMReader) Read([]byte) (int, error) {
	panic("rm attempted to read Automation input")
}
