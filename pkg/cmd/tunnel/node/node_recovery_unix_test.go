//go:build darwin || linux

package node

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestNodeCrashRecoveryHelper(t *testing.T) {
	target := os.Getenv("YCY_NODE_CRASH_PHASE")
	if target == "" {
		return
	}
	state, err := OpenState(os.Getenv("YCY_NODE_CRASH_DIRECTORY"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.Claim(context.Background(), bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	ports := strings.Split(os.Getenv("YCY_NODE_CRASH_PORTS"), ",")
	if len(ports) != 6 {
		t.Fatal("invalid crash ports")
	}
	port := func(index int) int64 {
		value, err := strconv.ParseInt(ports[index], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	encode := func(revision int64, stateName string, start int) []byte {
		t.Helper()
		snapshot := desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: revision, State: stateName}
		if stateName == "running" {
			snapshot.BindAddress, snapshot.BindPort, snapshot.VhostHTTPPort, snapshot.PortRangeStart, snapshot.PortRangeEnd, snapshot.Token = "127.0.0.1", port(start), port(start+1), port(start+2), port(start+2), "secret"
		}
		contents, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	runtime := newNodeRuntime(state)
	runtime.checkpoint = func(phase string) {
		if phase != target {
			return
		}
		record, err := state.readRuntime(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if record.HighestRevision == 2 {
			fmt.Fprintln(os.Stdout, "READY")
			time.Sleep(time.Hour)
		}
	}
	first := encode(1, "running", 0)
	if _, err := state.acceptCandidate(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if record, code := runtime.applyAccepted(context.Background()); code != "" || record.AppliedRevision != 1 {
		t.Fatalf("first apply: %+v, %s", record, code)
	}
	secondState := "running"
	if strings.HasPrefix(target, "disabled") || target == "disabling" {
		secondState = "disabled"
	}
	second := encode(2, secondState, 3)
	if _, err := state.acceptCandidate(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if target == "accepted" {
		fmt.Fprintln(os.Stdout, "READY")
		time.Sleep(time.Hour)
	}
	if record, code := runtime.applyAccepted(context.Background()); code != "" {
		t.Fatalf("second apply: %+v, %s", record, code)
	}
	t.Fatal("crash checkpoint was not reached")
}

func TestNodeSIGKILLRecoveryAtRuntimeCommitBoundaries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatal(err)
	}
	frpDirectory, err := tunnelruntime.DefaultFRPRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := tunnelruntime.EnsureFRPRuntimeAt(ctx, frpDirectory, artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"accepted", "switching", "old-stopped", "candidate-started", "applied", "disabling", "disabled-stopped", "disabled-complete"} {
		t.Run(phase, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "node")
			ports := make([]int, 0, 6)
			seen := map[int]bool{}
			for len(ports) < 6 {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				value := listener.Addr().(*net.TCPAddr).Port
				_ = listener.Close()
				if !seen[value] {
					seen[value] = true
					ports = append(ports, value)
				}
			}
			textPorts := make([]string, len(ports))
			for index, value := range ports {
				textPorts[index] = strconv.Itoa(value)
			}
			child := exec.Command(os.Args[0], "-test.run=^TestNodeCrashRecoveryHelper$")
			child.Env = append(os.Environ(), "YCY_NODE_CRASH_PHASE="+phase, "YCY_NODE_CRASH_DIRECTORY="+directory, "YCY_NODE_CRASH_PORTS="+strings.Join(textPorts, ","))
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			child.Stderr = &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			ready := make(chan string, 1)
			go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
			select {
			case line := <-ready:
				if line != "READY\n" {
					_ = child.Process.Kill()
					_ = child.Wait()
					t.Fatalf("child did not reach %s: %q, %s", phase, line, stderr.String())
				}
			case <-time.After(30 * time.Second):
				_ = child.Process.Kill()
				_ = child.Wait()
				t.Fatalf("child timed out at %s: %s", phase, stderr.String())
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			staleTransfer := filepath.Join(directory, "node-state-v1", "snapshot-abandoned")
			if err := os.WriteFile(staleTransfer, []byte("partial"), 0o600); err != nil {
				t.Fatal(err)
			}
			state, err := OpenState(directory)
			if err != nil {
				t.Fatal(err)
			}
			runtime := newNodeRuntime(state)
			runtime.ensureBinary = func(context.Context) (string, error) { return paths.FRPS, nil }
			code := runtime.recover(ctx)
			if code != "" {
				_ = state.Close()
				t.Fatalf("recover %s: %s", phase, code)
			}
			if _, err := os.Stat(staleTransfer); !os.IsNotExist(err) {
				t.Fatalf("abandoned snapshot remained: %v", err)
			}
			record, err := state.readRuntime(ctx)
			if err != nil {
				t.Fatal(err)
			}
			disabled := phase == "disabling" || strings.HasPrefix(phase, "disabled")
			if disabled {
				if !record.BootDisabled || !record.DisabledComplete || record.AppliedRevision != 2 || runtime.processState().State != tunnelruntime.FRPProcessStopped {
					t.Fatalf("disabled recovery at %s: %+v, %+v", phase, record, runtime.processState())
				}
			} else {
				wantRevision := int64(1)
				wantPort := ports[0]
				if phase == "applied" {
					wantRevision, wantPort = 2, ports[3]
				}
				if record.AppliedRevision != wantRevision || runtime.processState().State != tunnelruntime.FRPProcessRunning {
					t.Fatalf("running recovery at %s: %+v, %+v", phase, record, runtime.processState())
				}
				connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(wantPort)), time.Second)
				if err != nil {
					t.Fatalf("recovered FRPS port %d: %v", wantPort, err)
				}
				_ = connection.Close()
			}
			if runtime.supervisor != nil {
				_ = runtime.supervisor.Stop()
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNodeIgnoresUnrecordedResidualFRPS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatal(err)
	}
	frpDirectory, err := tunnelruntime.DefaultFRPRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := tunnelruntime.EnsureFRPRuntimeAt(ctx, frpDirectory, artifact)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "node")
	state, err := OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.Claim(ctx, bytes.Repeat([]byte{8}, 32)); err != nil {
		t.Fatal(err)
	}
	ports := make([]int, 3)
	for index := range ports {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ports[index] = listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
	}
	snapshot := desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: 1, State: "running", BindAddress: "127.0.0.1", BindPort: int64(ports[0]), VhostHTTPPort: int64(ports[1]), PortRangeStart: int64(ports[2]), PortRangeEnd: int64(ports[2]), Token: "secret"}
	contents, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.acceptCandidate(ctx, contents); err != nil {
		t.Fatal(err)
	}
	runtime := newNodeRuntime(state)
	path, err := runtime.render(snapshot, snapshotDigest(contents))
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(paths.FRPS, "-c", path)
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0])), 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			break
		}
	}
	if code := runtime.recover(ctx); code != "" {
		t.Fatalf("unrecorded residual recovery = %s", code)
	}
	if runtime.processState().State != tunnelruntime.FRPProcessStopped {
		t.Fatalf("unrecorded residual state = %+v", runtime.processState())
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0])), time.Second)
	if err != nil {
		t.Fatalf("unknown process was disturbed: %v", err)
	}
	_ = connection.Close()
}

func TestNodeSupervisorRestartUpdatesDurableOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatal(err)
	}
	frpDirectory, err := tunnelruntime.DefaultFRPRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := tunnelruntime.EnsureFRPRuntimeAt(ctx, frpDirectory, artifact)
	if err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(filepath.Join(t.TempDir(), "node"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ports := make([]int, 0, 3)
	for len(ports) < 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		value := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		unique := true
		for _, previous := range ports {
			if previous == value {
				unique = false
			}
		}
		if unique {
			ports = append(ports, value)
		}
	}
	contents, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: 1, State: "running", BindAddress: "127.0.0.1", BindPort: int64(ports[0]), VhostHTTPPort: int64(ports[1]), PortRangeStart: int64(ports[2]), PortRangeEnd: int64(ports[2]), Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.acceptCandidate(ctx, contents); err != nil {
		t.Fatal(err)
	}
	runtime := newNodeRuntime(state)
	runtime.ensureBinary = func(context.Context) (string, error) { return paths.FRPS, nil }
	defer func() {
		if runtime.supervisor != nil {
			_ = runtime.supervisor.Stop()
		}
	}()
	if record, code := runtime.applyAccepted(ctx); code != "" || record.OwnerPID == 0 {
		t.Fatalf("initial owner: %+v, %s", record, code)
	}
	initial := runtime.processState()
	if initial.PID == nil {
		t.Fatal("initial PID missing")
	}
	if err := syscall.Kill(-*initial.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		current := runtime.processState()
		if current.PID == nil || *current.PID == *initial.PID || current.State != tunnelruntime.FRPProcessRunning {
			continue
		}
		record, err := state.readRuntime(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if record.OwnerPID == *current.PID {
			return
		}
	}
	t.Fatalf("restarted FRPS owner not persisted: process=%+v", runtime.processState())
}
