//go:build !windows

package cm

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

const (
	cmGitFixtureStartupTimeout  = 15 * time.Second
	cmGitFixtureShutdownTimeout = 15 * time.Second
)

type cmTestSignalCause struct {
	signal os.Signal
}

func (cause cmTestSignalCause) Error() string {
	return cause.signal.String()
}

func (cause cmTestSignalCause) Signal() os.Signal {
	return cause.signal
}

func TestGitRunnerAdapterStopsTheChildProcessGroupWithoutAnOrphan(t *testing.T) {
	root := t.TempDir()
	grandchild := writeCMGitScript(t, root, "grandchild", "#!/bin/sh\nwhile :; do\n  sleep 1\ndone\n")
	pidPath := filepath.Join(root, "grandchild-pid")
	parent := writeCMGitScript(t, root, "parent", "#!/bin/sh\n\"$CM_GIT_GRANDCHILD\" &\nchild=$!\nprintf '%s\\n' \"$child\" > \"$CM_GIT_GRANDCHILD_PID\"\nwait \"$child\"\n")
	t.Setenv("CM_GIT_GRANDCHILD", grandchild)
	t.Setenv("CM_GIT_GRANDCHILD_PID", pidPath)

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: parent}}
	results := make(chan error, 1)
	go func() {
		_, err := runner.RunInput(ctx, []string{"cat-file", "--batch"}, []byte("HEAD:package.json\n"))
		results <- err
	}()

	pid := waitForCMGitPID(t, pidPath, cmGitFixtureStartupTimeout)
	cancel(cmTestSignalCause{signal: syscall.SIGTERM})
	select {
	case err := <-results:
		var outcome *gitprocess.SignalOutcome
		if !errors.As(err, &outcome) || !errors.Is(err, context.Canceled) || outcome.ExitCode() != 143 {
			t.Fatalf("RunInput() error = %v, want SIGTERM outcome with code 143", err)
		}
	case <-time.After(cmGitFixtureShutdownTimeout):
		t.Fatal("Git child process group did not stop")
	}
	if !waitForCMGitGone(pid, cmGitFixtureShutdownTimeout) {
		t.Fatalf("grandchild %d remained after Git cancellation", pid)
	}
}

func waitForCMGitPID(t *testing.T, path string, timeout time.Duration) int {
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

func waitForCMGitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
