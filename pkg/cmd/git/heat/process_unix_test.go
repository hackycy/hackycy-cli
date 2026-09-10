//go:build !windows

package heat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
)

const heatGitFixtureTimeout = 15 * time.Second

type heatTestSignalCause struct {
	signal os.Signal
}

func (cause heatTestSignalCause) Error() string {
	return cause.signal.String()
}

func (cause heatTestSignalCause) Signal() os.Signal {
	return cause.signal
}

func TestGitRunnerAdapterStopsTheChildProcessGroupWithoutAnOrphan(t *testing.T) {
	root := t.TempDir()
	grandchild := writeHeatGitScript(t, root, "grandchild", "#!/bin/sh\nwhile :; do\n  sleep 1\ndone\n")
	pidPath := filepath.Join(root, "grandchild-pid")
	parent := writeHeatGitScript(t, root, "parent", "#!/bin/sh\n\"$HEAT_GIT_GRANDCHILD\" &\nchild=$!\nprintf '%s\\n' \"$child\" > \"$HEAT_GIT_GRANDCHILD_PID\"\nwait \"$child\"\n")
	t.Setenv("HEAT_GIT_GRANDCHILD", grandchild)
	t.Setenv("HEAT_GIT_GRANDCHILD_PID", pidPath)

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: parent}}
	results := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, nil)
		results <- err
	}()

	pid := waitForHeatGitPID(t, pidPath)
	cancel(heatTestSignalCause{signal: syscall.SIGTERM})
	select {
	case err := <-results:
		var outcome *heatGitSignalOutcome
		if !errors.As(err, &outcome) || !errors.Is(err, context.Canceled) || outcome.ExitCode() != 143 {
			t.Fatalf("Run() error = %v, want SIGTERM outcome with code 143", err)
		}
	case <-time.After(heatGitFixtureTimeout):
		t.Fatal("Git child process group did not stop")
	}
	if !waitForHeatGitGone(pid, heatGitFixtureTimeout) {
		t.Fatalf("grandchild %d remained after Git cancellation", pid)
	}
}

func waitForHeatGitPID(t *testing.T, path string) int {
	return waitForHeatGitPIDWithin(t, path, heatGitFixtureTimeout)
}

func waitForHeatGitPIDWithin(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			value := strings.TrimSpace(string(contents))
			if value == "" {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			pid, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				t.Fatalf("parse Git child pid %q: %v", contents, parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read Git child pid: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Git child pid was not recorded")
	return 0
}

func waitForHeatGitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
