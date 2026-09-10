//go:build acceptance && !windows

package acceptance

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestDiffStandaloneBinaryTreatsSIGTERMAsNormalExit(t *testing.T) {
	binary := buildDiffStandaloneBinary(t)
	root := t.TempDir()
	baseline := filepath.Join(root, "baseline")
	target := filepath.Join(root, "target")
	writeStandaloneDiffFile(t, baseline, "same.txt", "same")
	writeStandaloneDiffFile(t, target, "same.txt", "same")
	process := startDiffStandalone(t, binary, root, environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""}), "diff", "--port", "0", baseline, target)
	_, _ = waitForDiffStartup(t, process)
	if err := process.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatalf("diff exit after SIGTERM: %v\nstderr:\n%s", err, process.stderr.String())
	}
	if process.command.ProcessState == nil || !process.command.ProcessState.Success() {
		t.Fatalf("SIGTERM process state = %#v", process.command.ProcessState)
	}
}

func TestDiffStandaloneBinaryPublicBindingPrintsLocalhostURL(t *testing.T) {
	binary := buildDiffStandaloneBinary(t)
	root := t.TempDir()
	baseline := filepath.Join(root, "baseline")
	target := filepath.Join(root, "target")
	writeStandaloneDiffFile(t, baseline, "same.txt", "same")
	writeStandaloneDiffFile(t, target, "same.txt", "same")
	process := startDiffStandalone(t, binary, root, environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""}), "diff", "--public", "--port", "0", baseline, target)
	startup, localURL := waitForDiffStartup(t, process)
	if !strings.HasPrefix(localURL, "http://localhost:") || !strings.Contains(startup, "MCP endpoint:   "+localURL+"/mcp") {
		t.Fatalf("public startup = %q", startup)
	}
	if err := process.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatalf("public diff exit: %v\nstderr:\n%s", err, process.stderr.String())
	}
}
