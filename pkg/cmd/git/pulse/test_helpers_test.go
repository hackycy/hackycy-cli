package pulse

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
