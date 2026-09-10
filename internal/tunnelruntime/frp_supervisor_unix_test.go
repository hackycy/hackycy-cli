//go:build darwin || linux

package tunnelruntime

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
)

const (
	frpSupervisorFixtureStartupTimeout  = 15 * time.Second
	frpSupervisorFixtureShutdownTimeout = 15 * time.Second
)

func TestFRPSupervisorStreamsOutputAndStopsItsUnixProcessGroup(t *testing.T) {
	root := t.TempDir()
	grandchild := writeFRPSupervisorScript(t, root, "grandchild", "#!/bin/sh\nwhile :; do sleep 1; done\n")
	pidPath := filepath.Join(root, "grandchild.pid")
	parent := writeFRPSupervisorScript(t, root, "parent", "#!/bin/sh\nprintf 'frpc ready\\n'\nprintf 'frpc warning\\n' >&2\n\"$FRP_GRANDCHILD\" &\nchild=$!\nprintf '%s\\n' \"$child\" > \"$FRP_GRANDCHILD_PID\"\nwait \"$child\"\n")
	t.Setenv("FRP_GRANDCHILD", grandchild)
	t.Setenv("FRP_GRANDCHILD_PID", pidPath)
	var output lockedFRPLogBuffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output})
	runtime.SetLevel(logging.Debug)
	supervisor := newTestFRPSupervisor(t, FRPSupervisorOptions{
		BinaryPath: parent, Role: FRPRoleClient, ActivationGrace: 20 * time.Millisecond, Logger: runtime.Logger("tunnel.client.frpc"),
	})
	if err := supervisor.Start(filepath.Join(root, "frpc.toml")); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	pid := waitForFRPSupervisorPID(t, pidPath)
	waitForFRPSupervisor(t, frpSupervisorFixtureStartupTimeout, func() bool {
		return strings.Contains(output.String(), "frpc ready") && strings.Contains(output.String(), "frpc warning")
	})
	state := supervisor.State()
	if state.State != FRPProcessRunning || state.PID == nil {
		t.Fatalf("running state = %#v", state)
	}
	if err := supervisor.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if state := supervisor.State(); state.State != FRPProcessStopped || state.PID != nil {
		t.Fatalf("stopped state = %#v", state)
	}
	if !waitForFRPSupervisorGone(pid, frpSupervisorFixtureShutdownTimeout) {
		t.Fatalf("grandchild %d remained after supervisor stop", pid)
	}
}

func TestFRPSupervisorRecoversUnexpectedExitAndSuppressesRecoveryAfterStop(t *testing.T) {
	root := t.TempDir()
	counterPath := filepath.Join(root, "starts")
	child := writeFRPSupervisorScript(t, root, "recover", "#!/bin/sh\ncount=0\nif [ -f \"$FRP_COUNTER\" ]; then count=$(cat \"$FRP_COUNTER\"); fi\ncount=$((count + 1))\nprintf '%s\\n' \"$count\" > \"$FRP_COUNTER\"\nif [ \"$count\" -eq 1 ]; then sleep 0.1; exit 23; fi\nwhile :; do sleep 1; done\n")
	t.Setenv("FRP_COUNTER", counterPath)
	supervisor := newTestFRPSupervisor(t, FRPSupervisorOptions{
		BinaryPath: child, Role: FRPRoleClient, ActivationGrace: 20 * time.Millisecond,
		Backoff: []time.Duration{20 * time.Millisecond}, StableAfter: time.Second,
	})
	if err := supervisor.Start(filepath.Join(root, "frpc.toml")); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForFRPSupervisor(t, frpSupervisorFixtureStartupTimeout, func() bool {
		return readFRPSupervisorCounter(counterPath) == 2 && supervisor.State().State == FRPProcessRunning
	})
	if err := supervisor.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if count := readFRPSupervisorCounter(counterPath); count != 2 {
		t.Fatalf("starts after manual stop = %d, want 2", count)
	}
}

func TestFRPSupervisorRejectsAnActivationExitAndKeepsConfigurationFailuresStopped(t *testing.T) {
	root := t.TempDir()
	earlyExit := writeFRPSupervisorScript(t, root, "early-exit", "#!/bin/sh\nexit 7\n")
	supervisor := newTestFRPSupervisor(t, FRPSupervisorOptions{
		BinaryPath: earlyExit, Role: FRPRoleServer, ActivationGrace: frpSupervisorFixtureStartupTimeout, Backoff: []time.Duration{10 * time.Millisecond},
	})
	if err := supervisor.Start(filepath.Join(root, "frps.toml")); err == nil || !strings.Contains(err.Error(), "exited during startup") {
		t.Fatalf("early Start() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if state := supervisor.State(); state.State != FRPProcessStopped {
		t.Fatalf("early-exit state = %#v", state)
	}

	counterPath := filepath.Join(root, "configuration-failure-starts")
	longRunning := writeFRPSupervisorScript(t, root, "configuration-failure", "#!/bin/sh\nprintf '1\\n' >> \"$FRP_COUNTER\"\nwhile :; do sleep 1; done\n")
	t.Setenv("FRP_COUNTER", counterPath)
	configured := newTestFRPSupervisor(t, FRPSupervisorOptions{
		BinaryPath: longRunning, Role: FRPRoleClient, ActivationGrace: 20 * time.Millisecond, Backoff: []time.Duration{10 * time.Millisecond},
	})
	if err := configured.Start(filepath.Join(root, "frpc.toml")); err != nil {
		t.Fatalf("configuration-failure Start() error = %v", err)
	}
	// Start confirms liveness, not that the fixture has completed its first write.
	waitForFRPSupervisor(t, frpSupervisorFixtureStartupTimeout, func() bool { return readFRPSupervisorCounter(counterPath) == 1 })
	if err := configured.ConfigurationFailed(StructuredRuntimeError{Code: "INVALID_CONFIG", Message: "candidate rejected"}); err != nil {
		t.Fatalf("ConfigurationFailed() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	state := configured.State()
	if state.State != FRPProcessConfigurationFailed || state.Error == nil || state.Error.Code != "INVALID_CONFIG" || readFRPSupervisorCounter(counterPath) != 1 {
		t.Fatalf("configuration failure state = %#v, starts = %d", state, readFRPSupervisorCounter(counterPath))
	}
}

func TestFRPSupervisorForceKillsAStubbornUnixProcessGroup(t *testing.T) {
	root := t.TempDir()
	grandchild := writeFRPSupervisorScript(t, root, "stubborn-grandchild", "#!/bin/sh\ntrap '' TERM\nwhile :; do sleep 1; done\n")
	pidPath := filepath.Join(root, "stubborn-grandchild.pid")
	parent := writeFRPSupervisorScript(t, root, "stubborn-parent", "#!/bin/sh\ntrap '' TERM\n\"$FRP_GRANDCHILD\" &\nchild=$!\nprintf '%s\\n' \"$child\" > \"$FRP_GRANDCHILD_PID\"\nwait \"$child\"\n")
	t.Setenv("FRP_GRANDCHILD", grandchild)
	t.Setenv("FRP_GRANDCHILD_PID", pidPath)
	supervisor := newTestFRPSupervisor(t, FRPSupervisorOptions{
		BinaryPath: parent, Role: FRPRoleServer, ActivationGrace: 20 * time.Millisecond, StopTimeout: 25 * time.Millisecond,
	})
	if err := supervisor.Start(filepath.Join(root, "frps.toml")); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	pid := waitForFRPSupervisorPID(t, pidPath)
	started := time.Now()
	if err := supervisor.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("forced stop took %s", elapsed)
	}
	if !waitForFRPSupervisorGone(pid, frpSupervisorFixtureShutdownTimeout) {
		t.Fatalf("stubborn grandchild %d remained after force stop", pid)
	}
}

func TestParseFRPSupervisorPIDDistinguishesAnEmptyPublicationFromInvalidData(t *testing.T) {
	for _, test := range []struct {
		name      string
		contents  string
		wantPID   int
		wantReady bool
		wantError bool
	}{
		{name: "empty file", contents: "", wantReady: false},
		{name: "whitespace file", contents: " \n\t", wantReady: false},
		{name: "valid PID", contents: "12345\n", wantPID: 12345, wantReady: true},
		{name: "invalid PID", contents: "not-a-pid", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			pid, ready, err := parseFRPSupervisorPID([]byte(test.contents))
			if (err != nil) != test.wantError || pid != test.wantPID || ready != test.wantReady {
				t.Fatalf("parseFRPSupervisorPID(%q) = (%d, %t, %v)", test.contents, pid, ready, err)
			}
		})
	}
}

func TestNewFRPSupervisorRejectsIncompleteConfiguration(t *testing.T) {
	if _, err := NewFRPSupervisor(FRPSupervisorOptions{Role: FRPRoleClient}); !errors.Is(err, ErrFRPSupervisorConfiguration) {
		t.Fatalf("empty binary error = %v", err)
	}
	if _, err := NewFRPSupervisor(FRPSupervisorOptions{BinaryPath: "/frpc", Role: "other"}); !errors.Is(err, ErrFRPSupervisorConfiguration) {
		t.Fatalf("invalid role error = %v", err)
	}
}

func newTestFRPSupervisor(t *testing.T, options FRPSupervisorOptions) *FRPSupervisor {
	t.Helper()
	supervisor, err := NewFRPSupervisor(options)
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

func waitForFRPSupervisorPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitForFRPSupervisor(t, frpSupervisorFixtureStartupTimeout, func() bool {
		contents, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		parsed, ready, err := parseFRPSupervisorPID(contents)
		if err != nil {
			t.Fatalf("parse child PID %q: %v", contents, err)
		}
		if !ready {
			return false
		}
		pid = parsed
		return true
	})
	return pid
}

func parseFRPSupervisorPID(contents []byte) (int, bool, error) {
	value := strings.TrimSpace(string(contents))
	if value == "" {
		return 0, false, nil
	}
	pid, err := strconv.Atoi(value)
	if err != nil {
		return 0, false, err
	}
	if pid < 1 {
		return 0, false, errors.New("PID must be positive")
	}
	return pid, true, nil
}

func waitForFRPSupervisorGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
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

func readFRPSupervisorCounter(path string) int {
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil {
		return -1
	}
	return count
}

type lockedFRPLogBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (buffer *lockedFRPLogBuffer) Write(contents []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.Write(contents)
}

func (buffer *lockedFRPLogBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}
