//go:build !windows

package gitprocess

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
)

const (
	gitProcessFixtureStartupTimeout  = 15 * time.Second
	gitProcessFixtureShutdownTimeout = 15 * time.Second
)

type testSignalCause struct {
	signal os.Signal
}

func (cause testSignalCause) Error() string {
	return cause.signal.String()
}

func (cause testSignalCause) Signal() os.Signal {
	return cause.signal
}

func TestRunnerStopsTheChildProcessGroupWithoutAnOrphan(t *testing.T) {
	root := t.TempDir()
	grandchild := writeScript(t, root, "grandchild", "#!/bin/sh\nwhile :; do\n  sleep 1\ndone\n")
	pidPath := filepath.Join(root, "grandchild-pid")
	parent := writeScript(t, root, "parent", "#!/bin/sh\n\"$GITPROCESS_GRANDCHILD\" &\nchild=$!\nprintf '%s\\n' \"$child\" > \"$GITPROCESS_GRANDCHILD_PID\"\nwait \"$child\"\n")
	t.Setenv("GITPROCESS_GRANDCHILD", grandchild)
	t.Setenv("GITPROCESS_GRANDCHILD_PID", pidPath)

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	results := make(chan error, 1)
	go func() {
		_, err := (&Runner{Executable: parent}).Run(ctx, nil)
		results <- err
	}()

	pid := waitForPID(t, pidPath, gitProcessFixtureStartupTimeout)
	cancel(testSignalCause{signal: syscall.SIGTERM})
	select {
	case err := <-results:
		var outcome *SignalOutcome
		if !errors.As(err, &outcome) || !errors.Is(err, context.Canceled) || outcome.ExitCode() != 143 {
			t.Fatalf("Run() error = %v, want SIGTERM outcome with code 143", err)
		}
	case <-time.After(gitProcessFixtureShutdownTimeout):
		t.Fatal("Git child process group did not stop")
	}
	if !waitForGone(pid, gitProcessFixtureShutdownTimeout) {
		t.Fatalf("grandchild %d remained after Git cancellation", pid)
	}
}

func waitForPID(t *testing.T, path string, timeout time.Duration) int {
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

func waitForGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
