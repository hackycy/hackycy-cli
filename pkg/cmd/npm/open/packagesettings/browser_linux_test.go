package packagesettings

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestXDGOpenReportsStartupErrors(t *testing.T) {
	if err := runXDGOpen(t.Context(), exec.Command(filepath.Join(t.TempDir(), "missing")), time.Second); err == nil {
		t.Fatal("runXDGOpen() succeeded for a missing launcher")
	}
	var exit *exec.ExitError
	if err := runXDGOpen(t.Context(), exec.Command("sh", "-c", "exit 4"), time.Second); !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Fatalf("runXDGOpen() = %v, want exit 4", err)
	}
}

func TestXDGOpenReturnsWhileLauncherIsStillRunning(t *testing.T) {
	command := exec.Command("sleep", "15")
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})
	started := time.Now()
	if err := runXDGOpen(t.Context(), command, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("runXDGOpen waited for the browser lifecycle: %s", elapsed)
	}
}
