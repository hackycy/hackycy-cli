package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestNodeRunningSnapshotVerifyStartAndRollbackWithPinnedFRPS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := tunnelruntime.DefaultFRPRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := tunnelruntime.EnsureFRPRuntimeAt(ctx, directory, artifact)
	if err != nil {
		t.Fatal(err)
	}
	state, err := OpenState(filepath.Join(t.TempDir(), "node"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	runtime := newNodeRuntime(state)
	runtime.ensureBinary = func(context.Context) (string, error) { return paths.FRPS, nil }
	defer func() {
		if runtime.supervisor != nil {
			_ = runtime.supervisor.Stop()
		}
	}()
	ports := make([]int, 0, 6)
	seen := make(map[int]bool)
	for len(ports) < 6 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		if !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	encode := func(revision int64, bind, http, pool int) []byte {
		t.Helper()
		contents, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: revision, State: "running", BindAddress: "127.0.0.1", BindPort: int64(bind), VhostHTTPPort: int64(http), PortRangeStart: int64(pool), PortRangeEnd: int64(pool), Token: "private-test-token"})
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	apply := func(contents []byte) (runtimeRecord, string) {
		t.Helper()
		if _, err := state.acceptCandidate(ctx, contents); err != nil {
			t.Fatal(err)
		}
		return runtime.applyAccepted(ctx)
	}
	first := encode(1, ports[0], ports[1], ports[2])
	record, code := apply(first)
	if code != "" || record.AppliedRevision != 1 || record.Phase != "applied" {
		t.Fatalf("first apply = (%+v, %s)", record, code)
	}
	checkOpen := func(port int) {
		t.Helper()
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(port)), time.Second)
		if err != nil {
			t.Fatalf("FRPS port %d unavailable: %v", port, err)
		}
		_ = connection.Close()
	}
	checkOpen(ports[0])
	firstPID := runtime.processState().PID
	if firstPID == nil {
		t.Fatal("first FRPS PID missing")
	}
	if _, err := state.acceptCandidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	if repeated, code := runtime.applyAccepted(ctx); code != "" || repeated.AppliedRevision != 1 || runtime.processState().PID == nil || *runtime.processState().PID != *firstPID {
		t.Fatalf("idempotent retry restarted FRPS: %+v, %s", repeated, code)
	}
	runtime.verifyConfig = func(context.Context, string) error { return errors.New("injected verify failure") }
	record, code = apply(encode(2, ports[3], ports[4], ports[5]))
	if code != "NODE_CONFIG_REJECTED" || record.AppliedRevision != 1 || record.FailureCode != code {
		t.Fatalf("verify failure = (%+v, %s)", record, code)
	}
	handler := newManagementHandler(state)
	handler.runtime = runtime
	server := httptest.NewServer(handler)
	defer server.Close()
	controller := newTestController(t, server.URL)
	if _, err := state.Claim(ctx, controller.key.Public); err != nil {
		t.Fatal(err)
	}
	id, _ := controller.start(t)
	controller.finish(t, id)
	status, _ := controller.message(t, "status", struct{}{})
	if status.Error != "" || !strings.Contains(string(status.Body), `"failedRevision":2`) || !strings.Contains(string(status.Body), `"appliedRevision":1`) || strings.Contains(string(status.Body), "private-test-token") {
		t.Fatalf("failed status: %+v", status)
	}
	checkOpen(ports[0])
	runtime.verifyConfig = nil
	occupied, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(ports[3])))
	if err != nil {
		t.Fatal(err)
	}
	record, code = apply(encode(3, ports[3], ports[4], ports[5]))
	_ = occupied.Close()
	if code != "NODE_APPLY_FAILED" || record.AppliedRevision != 1 || record.FailureCode != code {
		t.Fatalf("start rollback = (%+v, %s)", record, code)
	}
	checkOpen(ports[0])
	occupied, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(ports[3])))
	if err != nil {
		t.Fatal(err)
	}
	var oldPortOwner net.Listener
	runtime.beforeCandidateStart = func() {
		var listenErr error
		oldPortOwner, listenErr = net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(ports[0])))
		if listenErr != nil {
			t.Fatal(listenErr)
		}
	}
	record, code = apply(encode(4, ports[3], ports[4], ports[5]))
	_ = occupied.Close()
	if oldPortOwner != nil {
		_ = oldPortOwner.Close()
	}
	if code != "NODE_ROLLBACK_FAILED" || record.AppliedRevision != 1 || record.FailureCode != code || runtime.processState().State != tunnelruntime.FRPProcessStopped {
		t.Fatalf("rollback failure = (%+v, %s, %+v)", record, code, runtime.processState())
	}
}

func itoa(value int) string { return fmt.Sprint(value) }

func TestNodeDisabledIntentPrecedesStopAndClearsRuntimeReferences(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := tunnelruntime.DefaultFRPRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := tunnelruntime.EnsureFRPRuntimeAt(ctx, directory, artifact)
	if err != nil {
		t.Fatal(err)
	}
	stateDirectory := filepath.Join(t.TempDir(), "node")
	state, err := OpenState(stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	runtime := newNodeRuntime(state)
	runtime.ensureBinary = func(context.Context) (string, error) { return paths.FRPS, nil }
	defer func() {
		if runtime.supervisor != nil {
			_ = runtime.supervisor.Stop()
		}
		_ = state.Close()
	}()
	freePort := func() int {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		return port
	}
	bind, httpPort, pool := freePort(), freePort(), freePort()
	for bind == httpPort || bind == pool || httpPort == pool {
		bind, httpPort, pool = freePort(), freePort(), freePort()
	}
	running, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: 1, State: "running", BindAddress: "127.0.0.1", BindPort: int64(bind), VhostHTTPPort: int64(httpPort), PortRangeStart: int64(pool), PortRangeEnd: int64(pool), Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.acceptCandidate(ctx, running); err != nil {
		t.Fatal(err)
	}
	if record, code := runtime.applyAccepted(ctx); code != "" || record.AppliedRevision != 1 {
		t.Fatalf("running = (%+v, %s)", record, code)
	}
	disabled, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: 2, State: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.acceptCandidate(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	runtime.beforeDisableStop = func() {
		record, err := state.readRuntime(ctx)
		if err != nil || !record.BootDisabled || record.DisabledComplete {
			t.Fatalf("intent before stop = (%+v, %v)", record, err)
		}
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(bind)), time.Second)
		if err != nil {
			t.Fatalf("FRPS stopped before intent: %v", err)
		}
		_ = connection.Close()
	}
	record, code := runtime.applyAccepted(ctx)
	if code != "" || !record.BootDisabled || !record.DisabledComplete || record.AppliedRevision != 2 || len(record.LastGood) != 0 {
		t.Fatalf("disabled = (%+v, %s)", record, code)
	}
	if connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(bind)), time.Second); err == nil {
		_ = connection.Close()
		t.Fatal("FRPS still listening after disabled")
	}
	files, err := filepath.Glob(filepath.Join(state.directory, "frps-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("effective config files remain: %v, %v", files, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenState(stateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	record, err = state.readRuntime(ctx)
	if err != nil || !record.BootDisabled || !record.DisabledComplete || len(record.LastGood) != 0 {
		t.Fatalf("reopened disabled = (%+v, %v)", record, err)
	}
}

func TestNodeRunningRetryAfterDisabledAndInterruptedAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	directory := filepath.Join(t.TempDir(), "node")
	state, err := OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Claim(ctx, bytes.Repeat([]byte{9}, 32)); err != nil {
		t.Fatal(err)
	}
	disabled, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: 1, State: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.acceptCandidate(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	runtime := newNodeRuntime(state)
	if record, code := runtime.applyAccepted(ctx); code != "" || !record.DisabledComplete {
		t.Fatalf("initial disabled: %+v, %s", record, code)
	}
	ports := make([]int, 0, 3)
	for len(ports) < 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		unique := true
		for _, prior := range ports {
			if prior == port {
				unique = false
			}
		}
		if unique {
			ports = append(ports, port)
		}
	}
	running, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: 2, State: "running", BindAddress: "127.0.0.1", BindPort: int64(ports[0]), VhostHTTPPort: int64(ports[1]), PortRangeStart: int64(ports[2]), PortRangeEnd: int64(ports[2]), Token: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.acceptCandidate(ctx, running); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	runtime = newNodeRuntime(state)
	if code := runtime.recover(ctx); code != "" {
		t.Fatalf("recover pending running: %s", code)
	}
	record, err := state.readRuntime(ctx)
	if err != nil || !record.BootDisabled || record.AppliedRevision != 1 || record.Phase != "interrupted" || runtime.processState().State != tunnelruntime.FRPProcessStopped {
		t.Fatalf("pending running recovery: %+v, %+v, %v", record, runtime.processState(), err)
	}
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
	runtime.ensureBinary = func(context.Context) (string, error) { return paths.FRPS, nil }
	defer func() {
		if runtime.supervisor != nil {
			_ = runtime.supervisor.Stop()
		}
	}()
	if _, err := state.acceptCandidate(ctx, running); err != nil {
		t.Fatalf("same digest retry: %v", err)
	}
	record, code := runtime.applyAccepted(ctx)
	if code != "" || record.BootDisabled || record.AppliedRevision != 2 || record.Phase != "applied" {
		t.Fatalf("running retry: %+v, %s", record, code)
	}
}
