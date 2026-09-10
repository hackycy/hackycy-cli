//go:build darwin || linux

package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func newTestFRPSupervisor(t *testing.T, options tunnelruntime.FRPSupervisorOptions) *tunnelruntime.FRPSupervisor {
	t.Helper()
	supervisor, err := tunnelruntime.NewFRPSupervisor(options)
	if err != nil {
		t.Fatalf("NewFRPSupervisor() error = %v", err)
	}
	t.Cleanup(func() { _ = supervisor.Stop() })
	return supervisor
}

func writeFRPSupervisorScript(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func waitForFRPSupervisor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition did not become true within %s", timeout)
}
