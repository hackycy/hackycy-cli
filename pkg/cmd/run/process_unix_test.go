//go:build !windows

package run

import (
	"bytes"
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

func TestOSRunChildRunnerMapsExitCodesAndSignals(t *testing.T) {
	testCases := []struct {
		name     string
		contents string
		wantCode int
	}{
		{name: "exit code", contents: "#!/bin/sh\nexit 7\n", wantCode: 7},
		{name: "interrupt", contents: "#!/bin/sh\nkill -INT $$\n", wantCode: 130},
		{name: "terminate", contents: "#!/bin/sh\nkill -TERM $$\n", wantCode: 143},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			script := writeUnixRunProcessScript(t, t.TempDir(), "child", testCase.contents)
			runner := newOSRunChildRunner(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			result, err := runner.Run(context.Background(), ChildRequest{Executable: script})
			if err != nil || result.ExitCode != testCase.wantCode {
				t.Fatalf("Run() = (%#v, %v), want code %d", result, err, testCase.wantCode)
			}
		})
	}
}

func TestOSRunChildRunnerStopsTheChildProcessGroupWithoutAnOrphan(t *testing.T) {
	root := t.TempDir()
	grandchild := writeUnixRunProcessScript(t, root, "grandchild", "#!/bin/sh\nwhile :; do\n  sleep 1\ndone\n")
	pidPath := filepath.Join(root, "grandchild-pid")
	parent := writeUnixRunProcessScript(t, root, "parent", "#!/bin/sh\n\"$RUN_GRANDCHILD\" &\nchild=$!\nprintf '%s\\n' \"$child\" > \"$RUN_GRANDCHILD_PID\"\nwait \"$child\"\n")
	t.Setenv("RUN_GRANDCHILD", grandchild)
	t.Setenv("RUN_GRANDCHILD_PID", pidPath)

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	runner := newOSRunChildRunner(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	results := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := runner.Run(ctx, ChildRequest{Executable: parent})
		results <- struct {
			result Result
			err    error
		}{result: result, err: err}
	}()

	pid := waitForRunProcessPID(t, pidPath)
	cancel(runSignalCause{signal: syscall.SIGTERM})
	select {
	case outcome := <-results:
		if outcome.err != nil || outcome.result.ExitCode != 143 {
			t.Fatalf("Run() = (%#v, %v)", outcome.result, outcome.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child process group did not stop")
	}
	if !waitForRunProcessGone(pid, 2*time.Second) {
		t.Fatalf("grandchild %d remained after parent cancellation", pid)
	}
}

func writeUnixRunProcessScript(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name+".sh")
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func waitForRunProcessPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr != nil {
				t.Fatalf("parse child pid %q: %v", contents, parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read child pid: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child pid was not recorded")
	return 0
}

func waitForRunProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

type runSignalCause struct {
	signal os.Signal
}

func (cause runSignalCause) Error() string {
	return cause.signal.String()
}

func (cause runSignalCause) Signal() os.Signal {
	return cause.signal
}
