//go:build acceptance

package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newGitCMRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runGitCM(t, repository, "init")
	runGitCM(t, repository, "config", "user.email", "fixture@example.test")
	runGitCM(t, repository, "config", "user.name", "Fixture")
	writeGitCMFile(t, filepath.Join(repository, "package.json"), "{\"name\":\"before\"}\n")
	runGitCM(t, repository, "add", "package.json")
	runGitCM(t, repository, "commit", "-m", "chore: initialize fixture")
	return repository
}

func runGitCM(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = environmentWith(map[string]string{"GIT_CONFIG_NOSYSTEM": "1"})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func writeGitCMFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func withGitCMWorkingDirectory(t *testing.T, directory string) {
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
