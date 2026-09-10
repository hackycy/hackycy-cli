//go:build acceptance

package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitPulseStandaloneBinaryScansContainedWorkspaceActivity(t *testing.T) {
	binary := buildGitPulseStandalone(t)
	workspace := t.TempDir()
	alpha := filepath.Join(workspace, "alpha")
	beta := filepath.Join(workspace, "projects", "beta")
	ignored := filepath.Join(workspace, "node_modules", "ignored")
	unborn := filepath.Join(workspace, "unborn")
	linked := filepath.Join(workspace, "linked-worktree")
	initializeStandalonePulseRepository(t, alpha, "Ada", "ada@example.test", "alpha commit")
	initializeStandalonePulseRepository(t, beta, "Ben", "ben@example.test", "beta commit")
	initializeStandalonePulseRepository(t, ignored, "Ignored", "ignored@example.test", "ignored commit")
	if err := os.MkdirAll(unborn, 0o700); err != nil {
		t.Fatalf("create unborn repository: %v", err)
	}
	runStandalonePulseGit(t, unborn, "init", "-q")
	runStandalonePulseGit(t, alpha, "worktree", "add", "-q", linked)

	environment := environmentWith(map[string]string{
		"HOME":        t.TempDir(),
		"USERPROFILE": "",
		"NO_COLOR":    "1",
	})
	output, err := runGitPulseStandalone(binary, t.TempDir(), environment, "1,2\n", "git", "pulse", workspace, "--days", "3tail")
	if exitCode(err) != 1 || string(output) != "Skipped 1 repositories while reading commits: unborn\nerror: git pulse requires an interactive terminal\n" {
		t.Fatalf("redirected author selection = (%v, %q)", err, output)
	}

	emptyOutput, err := runGitPulseStandalone(binary, t.TempDir(), environment, "", "git", "pulse", workspace, "--days=0")
	if err != nil || !strings.Contains(string(emptyOutput), "No commits found in the specified date range.") || strings.Contains(string(emptyOutput), "\x1b[") {
		t.Fatalf("zero-day git pulse = (%v, %q)", err, emptyOutput)
	}

	noRepositoryWorkspace := t.TempDir()
	noRepositoryOutput, err := runGitPulseStandalone(binary, t.TempDir(), environment, "", "git", "pulse", noRepositoryWorkspace, "--days", "1")
	if err != nil || !strings.Contains(string(noRepositoryOutput), "No Git repositories found.") {
		t.Fatalf("no-repository git pulse = (%v, %q)", err, noRepositoryOutput)
	}

	helpOutput, err := runGitPulseStandalone(binary, t.TempDir(), environment, "", "git", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "heat") || !strings.Contains(string(helpOutput), "pulse") {
		t.Fatalf("git help = (%v, %q)", err, helpOutput)
	}
	invalidOutput, err := runGitPulseStandalone(binary, t.TempDir(), environment, "", "git", "pulse", workspace, "unexpected")
	if exitCode(err) != 1 || !strings.Contains(string(invalidOutput), "accepts at most 1 arg(s)") {
		t.Fatalf("too many pulse directories = (%v, %q)", err, invalidOutput)
	}
	missingOutput, err := runGitPulseStandalone(binary, t.TempDir(), environment, "", "git", "pulse", filepath.Join(workspace, "missing"), "--days", "1")
	if exitCode(err) != 1 || !strings.Contains(string(missingOutput), "Directory not found:") {
		t.Fatalf("missing pulse workspace = (%v, %q)", err, missingOutput)
	}
}

func TestGitPulseStandaloneBinaryReportsUnavailableGitBeforeScanning(t *testing.T) {
	binary := buildGitPulseStandalone(t)
	workspace := t.TempDir()
	environment := environmentWith(map[string]string{
		"HOME":        t.TempDir(),
		"USERPROFILE": "",
		"PATH":        t.TempDir(),
	})
	output, err := runGitPulseStandalone(binary, workspace, environment, "", "git", "pulse", "--days", "1")
	if exitCode(err) != 1 || !strings.Contains(string(output), "Git is not installed or not available in the system PATH.") {
		t.Fatalf("unavailable Git pulse = (%v, %q)", err, output)
	}
}

func buildGitPulseStandalone(t *testing.T) string {
	t.Helper()
	binary := standaloneBinaryOutputPath(filepath.Join(t.TempDir(), "ycy"))
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	build.Dir = repositoryRoot(t)
	build.Env = environmentWith(map[string]string{
		"CGO_ENABLED": "0",
		"GOTOOLCHAIN": "go1.26.7",
		"GOWORK":      "off",
	})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}
	return binary
}

func initializeStandalonePulseRepository(t *testing.T, directory, name, email, message string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create repository directory: %v", err)
	}
	runStandalonePulseGit(t, directory, "init", "-q")
	runStandalonePulseGit(t, directory, "config", "user.name", name)
	runStandalonePulseGit(t, directory, "config", "user.email", email)
	writeStandalonePulseFile(t, directory, "README.md", message+"\n")
	runStandalonePulseGit(t, directory, "add", ".")
	runStandalonePulseGit(t, directory, "commit", "-qm", message)
}

func runGitPulseStandalone(binary, directory string, environment []string, input string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = environment
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}

func runStandalonePulseGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, err, output)
	}
}

func writeStandalonePulseFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %q: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}
