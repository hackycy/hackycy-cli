package connect

import (
	"context"
	"encoding/json"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hackycy/hackycy-cli/internal/logging"
)

type clientRunRuntimeStub struct {
	mu         sync.Mutex
	state      tunnelruntime.FRPSupervisorState
	verified   int
	started    int
	stopped    int
	restarted  int
	restartErr error
	listeners  map[uint64]func(tunnelruntime.FRPSupervisorState)
	next       uint64
}

func init() {
	if os.Getenv("YCY_TEST_FRPC_VERIFY") != "1" {
		return
	}
	arguments := os.Args[1:]
	if len(arguments) != 3 || arguments[0] != "verify" || arguments[1] != "-c" {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("YCY_TEST_FRPC_ARGUMENTS"), []byte(strings.Join(arguments, "\n")), 0o600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func newClientRunRuntimeStub() *clientRunRuntimeStub {
	return &clientRunRuntimeStub{state: tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessStopped}, listeners: make(map[uint64]func(tunnelruntime.FRPSupervisorState))}
}

func (runtime *clientRunRuntimeStub) Verify(context.Context, string) error {
	runtime.mu.Lock()
	runtime.verified++
	runtime.mu.Unlock()
	return nil
}

func (runtime *clientRunRuntimeStub) Start(string) error {
	runtime.setState(tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessRunning})
	runtime.mu.Lock()
	runtime.started++
	runtime.mu.Unlock()
	return nil
}

func (runtime *clientRunRuntimeStub) Stop() error {
	runtime.setState(tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessStopped})
	runtime.mu.Lock()
	runtime.stopped++
	runtime.mu.Unlock()
	return nil
}

func (runtime *clientRunRuntimeStub) Restart() error {
	runtime.mu.Lock()
	err := runtime.restartErr
	runtime.restarted++
	runtime.mu.Unlock()
	if err == nil {
		// Mirror the supervisor's observable stop/start transition so callers
		// can distinguish this restart from the initial apply state report.
		runtime.setState(tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessStopped})
		runtime.setState(tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessRunning})
	}
	return err
}

func (runtime *clientRunRuntimeStub) State() tunnelruntime.FRPSupervisorState {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.state
}

func (runtime *clientRunRuntimeStub) Observe(listener func(tunnelruntime.FRPSupervisorState)) func() {
	if listener == nil {
		return func() {}
	}
	runtime.mu.Lock()
	id := runtime.next
	runtime.next++
	runtime.listeners[id] = listener
	state := runtime.state
	runtime.mu.Unlock()
	listener(state)
	return func() {
		runtime.mu.Lock()
		delete(runtime.listeners, id)
		runtime.mu.Unlock()
	}
}

func (runtime *clientRunRuntimeStub) setState(state tunnelruntime.FRPSupervisorState) {
	runtime.mu.Lock()
	runtime.state = state
	listeners := make([]func(tunnelruntime.FRPSupervisorState), 0, len(runtime.listeners))
	for _, listener := range runtime.listeners {
		listeners = append(listeners, listener)
	}
	runtime.mu.Unlock()
	for _, listener := range listeners {
		listener(state)
	}
}

func (runtime *clientRunRuntimeStub) counts() (verified, started, stopped, restarted int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.verified, runtime.started, runtime.stopped, runtime.restarted
}

func TestRunClientReconnectsWithoutRestartingAnAppliedFRPC(t *testing.T) {
	desired := clientDesiredState(1, true)
	server, hellos := clientRunControlServer(t, desired, func(index int, socket *websocket.Conn) {
		awaitClientApplyResult(t, socket, desired.Snapshot.Revision)
		if index == 1 {
			_ = socket.Close()
			return
		}
		if err := socket.WriteJSON(tunnelruntime.Revoke{Type: "revoke", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion, Reason: "deleted"}); err != nil {
			t.Errorf("write revoke: %v", err)
		}
	})
	defer server.Close()
	runtime := newClientRunRuntimeStub()
	authenticated := 0
	err := runClientAgainstTestServer(t, context.Background(), server, runtime, func() error {
		authenticated++
		return nil
	})
	if err != nil {
		t.Fatalf("RunClient() error = %v", err)
	}
	if got := len(hellos()); got != 2 {
		t.Fatalf("hello count = %d, want 2", got)
	}
	values := hellos()
	if values[0].LastAppliedRevision != 0 || values[1].LastAppliedRevision != 1 {
		t.Fatalf("hello applied revisions = %#v", values)
	}
	verified, started, _, restarted := runtime.counts()
	if verified != 1 || started != 1 || restarted != 0 || authenticated != 1 {
		t.Fatalf("runtime/authenticated = verify:%d start:%d restart:%d auth:%d", verified, started, restarted, authenticated)
	}
}

func TestRunClientStopsOnRejectedReconnectionProbe(t *testing.T) {
	desired := clientDesiredState(1, true)
	server, _ := clientRunControlServerWithProbe(t, desired, func(probe int) int {
		if probe == 1 {
			return http.StatusUpgradeRequired
		}
		return http.StatusUnauthorized
	}, func(_ int, socket *websocket.Conn) {
		awaitClientApplyResult(t, socket, desired.Snapshot.Revision)
		_ = socket.Close()
	})
	defer server.Close()
	runtime := newClientRunRuntimeStub()
	err := runClientAgainstTestServer(t, context.Background(), server, runtime, nil)
	if !errors.Is(err, ErrClientAuthentication) {
		t.Fatalf("RunClient() error = %v, want ErrClientAuthentication", err)
	}
	_, started, stopped, _ := runtime.counts()
	if started != 1 || stopped < 2 {
		t.Fatalf("runtime starts/stops = %d/%d, want one applied start and final stop", started, stopped)
	}
}

func TestRunClientStopsAndReleasesItsInstanceOnContextCancellation(t *testing.T) {
	desired := clientDesiredState(1, true)
	started := make(chan struct{})
	server, _ := clientRunControlServer(t, desired, func(_ int, socket *websocket.Conn) {
		awaitClientApplyResult(t, socket, desired.Snapshot.Revision)
		close(started)
		for {
			if _, _, err := socket.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer server.Close()
	context, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := newClientRunRuntimeStub()
	root := t.TempDir()
	result := make(chan error, 1)
	go func() {
		result <- runClientAgainstTestServerAtRoot(context, server, runtime, root, nil)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not apply its initial desired state")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("RunClient() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunClient() did not stop after context cancellation")
	}
	if _, _, stopped, _ := runtime.counts(); stopped < 2 {
		t.Fatalf("runtime stops = %d, want initial replacement plus final shutdown", stopped)
	}
	if _, err := os.Stat(filepath.Join(root, clientTestInstanceID('r'), ".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("instance lock remains after cancellation: %v", err)
	}
}

func TestRunClientDispatchesRestartFRPC(t *testing.T) {
	desired := clientDesiredState(1, true)
	server, _ := clientRunControlServer(t, desired, func(_ int, socket *websocket.Conn) {
		awaitClientApplyResult(t, socket, desired.Snapshot.Revision)
		if err := socket.WriteJSON(tunnelruntime.RestartFRPC{Type: "restart_frpc", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion}); err != nil {
			t.Errorf("write restart: %v", err)
			return
		}
		awaitClientRestartTransition(t, socket)
		if err := socket.WriteJSON(tunnelruntime.Revoke{Type: "revoke", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion, Reason: "deleted"}); err != nil {
			t.Errorf("write revoke: %v", err)
		}
	})
	defer server.Close()
	runtime := newClientRunRuntimeStub()
	if err := runClientAgainstTestServer(t, context.Background(), server, runtime, nil); err != nil {
		t.Fatalf("RunClient() error = %v", err)
	}
	_, started, _, restarted := runtime.counts()
	if started != 1 || restarted != 1 {
		t.Fatalf("runtime start/restart = %d/%d, want 1/1", started, restarted)
	}
}

func TestRunClientStopsAfterRestartFailure(t *testing.T) {
	desired := clientDesiredState(1, true)
	server, _ := clientRunControlServer(t, desired, func(_ int, socket *websocket.Conn) {
		awaitClientApplyResult(t, socket, desired.Snapshot.Revision)
		if err := socket.WriteJSON(tunnelruntime.RestartFRPC{Type: "restart_frpc", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion}); err != nil {
			t.Errorf("write restart: %v", err)
			return
		}
		_, _, _ = socket.ReadMessage()
	})
	defer server.Close()
	runtime := newClientRunRuntimeStub()
	runtime.restartErr = errors.New("restart fixture failed")
	err := runClientAgainstTestServer(t, context.Background(), server, runtime, nil)
	if !errors.Is(err, errClientControlFatal) {
		t.Fatalf("RunClient() error = %v, want fatal restart failure", err)
	}
	_, _, stopped, restarted := runtime.counts()
	if restarted != 1 || stopped < 2 {
		t.Fatalf("runtime restart/stops = %d/%d, want 1 and final stop", restarted, stopped)
	}
}

func TestRunClientUsesCacheOnlyForHelloUntilWelcome(t *testing.T) {
	desired := clientDesiredState(7, true)
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	root := t.TempDir()
	instanceID := clientTestInstanceID('c')
	if err := WriteClientAppliedState(filepath.Join(root, instanceID), ClientAppliedState{ClientDesiredConfiguration: desired, Revision: desired.Snapshot.Revision}); err != nil {
		t.Fatalf("WriteClientAppliedState() error = %v", err)
	}
	helloReceived := make(chan tunnelruntime.AgentHello, 1)
	releaseWelcome := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !websocket.IsWebSocketUpgrade(request) {
			writer.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		socket, upgradeErr := websocket.Upgrade(writer, request, nil, 0, 0)
		if upgradeErr != nil {
			t.Errorf("upgrade: %v", upgradeErr)
			return
		}
		defer socket.Close()
		var hello tunnelruntime.AgentHello
		if readErr := socket.ReadJSON(&hello); readErr != nil {
			t.Errorf("read hello: %v", readErr)
			return
		}
		helloReceived <- hello
		<-releaseWelcome
		if writeErr := socket.WriteJSON(tunnelruntime.AgentWelcome{
			Type:                  "welcome",
			TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
			RequiredFRPVersion:    tunnelruntime.FRPVersion,
			Artifact:              artifact.Description,
			AdvertisedFRPHost:     desired.AdvertisedFRPHost,
			AdvertisedFRPPort:     desired.AdvertisedFRPPort,
			InternalFRPToken:      desired.InternalFRPToken,
			Snapshot:              desired.Snapshot,
		}); writeErr != nil {
			t.Errorf("write welcome: %v", writeErr)
			return
		}
		awaitClientApplyResult(t, socket, desired.Snapshot.Revision)
		if writeErr := socket.WriteJSON(tunnelruntime.Revoke{Type: "revoke", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion, Reason: "deleted"}); writeErr != nil {
			t.Errorf("write revoke: %v", writeErr)
		}
	}))
	defer server.Close()
	controlServer, err := normalizeControlPlaneURL(server.URL)
	if err != nil {
		t.Fatalf("normalize test server: %v", err)
	}
	runtime := newClientRunRuntimeStub()
	var runtimeMu sync.Mutex
	runtimeCreations := 0
	result := make(chan error, 1)
	go func() {
		result <- RunClient(context.Background(), ClientConfig{Server: controlServer, Token: "client-token"}, ClientRunOptions{
			InstanceIdentity: clientInstanceIdentityStub{id: instanceID},
			StateRoot:        root,
			Logger:           logging.Logger{},
			Backoff:          []time.Duration{time.Millisecond},
			newRuntime: func(context.Context, logging.Logger) (ClientFRPRuntime, error) {
				runtimeMu.Lock()
				runtimeCreations++
				runtimeMu.Unlock()
				return runtime, nil
			},
		})
	}()
	select {
	case hello := <-helloReceived:
		if hello.LastAppliedRevision != desired.Snapshot.Revision {
			t.Fatalf("hello revision = %d, want %d", hello.LastAppliedRevision, desired.Snapshot.Revision)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not send hello")
	}
	runtimeMu.Lock()
	createdBeforeWelcome := runtimeCreations
	runtimeMu.Unlock()
	if createdBeforeWelcome != 0 {
		t.Fatalf("FRPC runtime creations before welcome = %d, want 0", createdBeforeWelcome)
	}
	if _, started, _, _ := runtime.counts(); started != 0 {
		t.Fatalf("FRPC starts before welcome = %d, want 0", started)
	}
	close(releaseWelcome)
	select {
	case runErr := <-result:
		if runErr != nil {
			t.Fatalf("RunClient() error = %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunClient() did not finish after revoke")
	}
	runtimeMu.Lock()
	createdAfterWelcome := runtimeCreations
	runtimeMu.Unlock()
	if createdAfterWelcome != 1 {
		t.Fatalf("FRPC runtime creations after welcome = %d, want 1", createdAfterWelcome)
	}
}

func TestRunClientDoesNotAcquireFRPCForAnIncompatibleWelcome(t *testing.T) {
	server := clientAgentFirstFrameServer(t, tunnelruntime.Incompatible{Type: "incompatible", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion, Message: "upgrade ycy"})
	defer server.Close()
	controlServer, err := normalizeControlPlaneURL(server.URL)
	if err != nil {
		t.Fatalf("normalize test server: %v", err)
	}
	created := 0
	err = RunClient(context.Background(), ClientConfig{Server: controlServer, Token: "client-token"}, ClientRunOptions{
		InstanceIdentity: clientInstanceIdentityStub{id: clientTestInstanceID('i')},
		StateRoot:        t.TempDir(),
		Logger:           logging.Logger{},
		newRuntime: func(context.Context, logging.Logger) (ClientFRPRuntime, error) {
			created++
			return newClientRunRuntimeStub(), nil
		},
	})
	if !errors.Is(err, ErrClientIncompatible) {
		t.Fatalf("RunClient() error = %v, want ErrClientIncompatible", err)
	}
	if created != 0 {
		t.Fatalf("FRPC runtime creations = %d, want 0", created)
	}
}

func TestManagedClientFRPRuntimeUsesOnlyPinnedRuntimePaths(t *testing.T) {
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	directory := filepath.Join(t.TempDir(), "frp", tunnelruntime.FRPVersion)
	expected := tunnelruntime.FRPRuntimePathsFor(directory, artifact.Target)
	called := false
	runtime, err := newManagedClientFRPRuntime(context.Background(), managedClientFRPRuntimeOptions{
		frpArtifact:         &artifact,
		frpRuntimeDirectory: directory,
		ensureFRPRuntime: func(_ context.Context, receivedDirectory string, receivedArtifact tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error) {
			called = true
			if receivedDirectory != directory || receivedArtifact != artifact {
				t.Fatalf("pinned runtime input = (%q, %#v)", receivedDirectory, receivedArtifact)
			}
			return expected, nil
		},
	})
	if err != nil {
		t.Fatalf("newManagedClientFRPRuntime() error = %v", err)
	}
	if !called || runtime.binaryPath != expected.FRPC || runtime.supervisor.Role() != tunnelruntime.FRPRoleClient {
		t.Fatalf("managed runtime = %#v, called = %t", runtime, called)
	}
	if err := runtime.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestManagedClientFRPRuntimeVerifiesCandidateWithFRPC(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "frpc-arguments")
	candidate := filepath.Join(t.TempDir(), "frpc.toml")
	t.Setenv("YCY_TEST_FRPC_VERIFY", "1")
	t.Setenv("YCY_TEST_FRPC_ARGUMENTS", marker)
	runtime := &managedClientFRPRuntime{binaryPath: os.Args[0]}
	if err := runtime.Verify(context.Background(), candidate); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	arguments, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read frpc arguments: %v", err)
	}
	if got, want := string(arguments), strings.Join([]string{"verify", "-c", candidate}, "\n"); got != want {
		t.Fatalf("frpc arguments = %q, want %q", got, want)
	}
}

func clientRunControlServer(t *testing.T, desired ClientDesiredConfiguration, onConnection func(int, *websocket.Conn)) (*httptest.Server, func() []tunnelruntime.AgentHello) {
	t.Helper()
	return clientRunControlServerWithProbe(t, desired, func(int) int { return http.StatusUpgradeRequired }, onConnection)
}

func clientRunControlServerWithProbe(t *testing.T, desired ClientDesiredConfiguration, probeStatus func(int) int, onConnection func(int, *websocket.Conn)) (*httptest.Server, func() []tunnelruntime.AgentHello) {
	t.Helper()
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	var mu sync.Mutex
	probes := 0
	connections := 0
	hellos := []tunnelruntime.AgentHello{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !websocket.IsWebSocketUpgrade(request) {
			mu.Lock()
			probes++
			status := probeStatus(probes)
			mu.Unlock()
			writer.WriteHeader(status)
			return
		}
		socket, err := websocket.Upgrade(writer, request, nil, 0, 0)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer socket.Close()
		var hello tunnelruntime.AgentHello
		if err := socket.ReadJSON(&hello); err != nil {
			t.Errorf("read hello: %v", err)
			return
		}
		mu.Lock()
		connections++
		index := connections
		hellos = append(hellos, hello)
		mu.Unlock()
		if err := socket.WriteJSON(tunnelruntime.AgentWelcome{
			Type:                  "welcome",
			TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
			RequiredFRPVersion:    tunnelruntime.FRPVersion,
			Artifact:              artifact.Description,
			AdvertisedFRPHost:     desired.AdvertisedFRPHost,
			AdvertisedFRPPort:     desired.AdvertisedFRPPort,
			InternalFRPToken:      desired.InternalFRPToken,
			Snapshot:              desired.Snapshot,
		}); err != nil {
			t.Errorf("write welcome: %v", err)
			return
		}
		onConnection(index, socket)
	}))
	return server, func() []tunnelruntime.AgentHello {
		mu.Lock()
		defer mu.Unlock()
		return append([]tunnelruntime.AgentHello(nil), hellos...)
	}
}

func awaitClientApplyResult(t *testing.T, socket *websocket.Conn, revision int64) {
	t.Helper()
	if err := socket.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer socket.SetReadDeadline(time.Time{})
	for {
		_, source, err := socket.ReadMessage()
		if err != nil {
			t.Fatalf("read client frame: %v", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(source, &envelope); err != nil {
			t.Fatalf("decode client frame: %v", err)
		}
		if envelope.Type != "apply_result" {
			continue
		}
		var result tunnelruntime.ApplyResult
		if err := json.Unmarshal(source, &result); err != nil {
			t.Fatalf("decode apply result: %v", err)
		}
		if result.Success && result.Revision == revision {
			return
		}
	}
}

func awaitClientProcessState(t *testing.T, socket *websocket.Conn, want tunnelruntime.FRPProcessState) {
	t.Helper()
	if err := socket.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	defer socket.SetReadDeadline(time.Time{})
	for {
		_, source, err := socket.ReadMessage()
		if err != nil {
			t.Fatalf("read client frame: %v", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(source, &envelope); err != nil {
			t.Fatalf("decode client frame: %v", err)
		}
		if envelope.Type != "process_state" {
			continue
		}
		var state tunnelruntime.ProcessState
		if err := json.Unmarshal(source, &state); err != nil {
			t.Fatalf("decode process state: %v", err)
		}
		if state.State == want {
			return
		}
	}
}

func awaitClientRestartTransition(t *testing.T, socket *websocket.Conn) {
	t.Helper()
	awaitClientProcessState(t, socket, tunnelruntime.FRPProcessStopped)
	awaitClientProcessState(t, socket, tunnelruntime.FRPProcessRunning)
}

func runClientAgainstTestServer(t *testing.T, ctx context.Context, server *httptest.Server, runtime *clientRunRuntimeStub, onAuthenticated func() error) error {
	t.Helper()
	return runClientAgainstTestServerAtRoot(ctx, server, runtime, t.TempDir(), onAuthenticated)
}

func runClientAgainstTestServerAtRoot(ctx context.Context, server *httptest.Server, runtime *clientRunRuntimeStub, root string, onAuthenticated func() error) error {
	controlServer, err := normalizeControlPlaneURL(server.URL)
	if err != nil {
		return err
	}
	return RunClient(ctx, ClientConfig{Server: controlServer, Token: "client-token"}, ClientRunOptions{
		InstanceIdentity: clientInstanceIdentityStub{id: clientTestInstanceID('r')},
		StateRoot:        root,
		Logger:           logging.Logger{},
		OnAuthenticated:  onAuthenticated,
		Backoff:          []time.Duration{time.Millisecond},
		newRuntime: func(context.Context, logging.Logger) (ClientFRPRuntime, error) {
			return runtime, nil
		},
	})
}
