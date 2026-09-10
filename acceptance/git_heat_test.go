//go:build acceptance

package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitHeatStandaloneBinaryUsesAContainedGitRepository(t *testing.T) {
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

	gitRepository := t.TempDir()
	runStandaloneHeatGit(t, gitRepository, "init", "-q")
	runStandaloneHeatGit(t, gitRepository, "config", "user.name", "Heat Test")
	runStandaloneHeatGit(t, gitRepository, "config", "user.email", "heat@example.test")
	writeStandaloneHeatFile(t, gitRepository, "README.md", "initial\n")
	writeStandaloneHeatFile(t, gitRepository, "src/main.go", "package main\n")
	runStandaloneHeatGit(t, gitRepository, "add", ".")
	runStandaloneHeatGit(t, gitRepository, "commit", "-qm", "initial")
	writeStandaloneHeatFile(t, gitRepository, "src/main.go", "package main\n// changed\n")
	specialPath := "src/tab\tname.txt"
	if runtime.GOOS == "windows" {
		specialPath = "src/tab-name.txt"
	}
	writeStandaloneHeatFile(t, gitRepository, specialPath, "special\n")
	runStandaloneHeatGit(t, gitRepository, "add", ".")
	runStandaloneHeatGit(t, gitRepository, "commit", "-qm", "second")

	nested := filepath.Join(gitRepository, "nested", "directory")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("create nested invocation directory: %v", err)
	}
	environment := environmentWith(map[string]string{
		"HOME":        t.TempDir(),
		"USERPROFILE": "",
		"NO_COLOR":    "1",
	})
	output, err := runGitHeatStandalone(binary, nested, environment, "git", "heat", "-n", "2tail", "-t", "files", "-s", "path", "-q", "MAIN")
	if err != nil {
		t.Fatalf("git heat = (%v, %q)", err, output)
	}
	text := string(output)
	for _, expected := range []string{"HACKYCY CLI", "last 2 commits", "README.md", "src/main.go", specialPath, "File"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("git heat output does not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("NO_COLOR output contains ANSI: %q", text)
	}

	runStandaloneHeatGit(t, gitRepository, "commit", "--allow-empty", "-qm", "empty")
	emptyOutput, err := runGitHeatStandalone(binary, nested, environment, "git", "heat", "-n", "1")
	if err != nil || string(emptyOutput) != "No changed files found in the selected range.\n" {
		t.Fatalf("empty git heat = (%v, %q)", err, emptyOutput)
	}

	helpOutput, err := runGitHeatStandalone(binary, nested, environment, "git", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "heat") || !strings.Contains(string(helpOutput), "pulse") {
		t.Fatalf("git help = (%v, %q)", err, helpOutput)
	}
	invalidOutput, err := runGitHeatStandalone(binary, nested, environment, "git", "heat", "-n", "1", "-d", "1")
	if exitCode(err) != 1 || !strings.Contains(string(invalidOutput), "Please use either -n/--limit or -d/--days, not both.") {
		t.Fatalf("mutually exclusive ranges = (%v, %q)", err, invalidOutput)
	}
}

func TestGitHeatStandaloneBinaryReportsRepositoryFailures(t *testing.T) {
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
	output, err := runGitHeatStandalone(binary, t.TempDir(), environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""}), "git", "heat")
	if exitCode(err) != 1 || !strings.Contains(string(output), "not a git repository") {
		t.Fatalf("non-repository git heat = (%v, %q)", err, output)
	}
}

func runGitHeatStandalone(binary, directory string, environment []string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = environment
	return command.CombinedOutput()
}

func runStandaloneHeatGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, err, output)
	}
}

func writeStandaloneHeatFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %q: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}
