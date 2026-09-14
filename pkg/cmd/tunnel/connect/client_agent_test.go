package connect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestClientAgentConnectProbesBeforeV4HelloAndPinnedWelcome(t *testing.T) {
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	target, err := tunnelruntime.CurrentWireTarget()
	if err != nil {
		t.Fatalf("CurrentWireTarget() error = %v", err)
	}
	var mu sync.Mutex
	probes := 0
	upgrades := 0
	hellos := make(chan tunnelruntime.AgentHello, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/agent" {
			http.NotFound(writer, request)
			return
		}
		if !websocket.IsWebSocketUpgrade(request) {
			if got, want := request.Header.Get("Authorization"), "Bearer client-token"; got != want {
				t.Errorf("probe authorization = %q, want %q", got, want)
			}
			mu.Lock()
			probes++
			mu.Unlock()
			writer.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		mu.Lock()
		if probes == 0 {
			t.Error("WebSocket upgrade occurred before authentication probe")
		}
		upgrades++
		mu.Unlock()
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
		hellos <- hello
		if writeErr := socket.WriteJSON(tunnelruntime.AgentWelcome{
			Type:                  "welcome",
			TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
			RequiredFRPVersion:    tunnelruntime.FRPVersion,
			Artifact:              artifact.Description,
			AdvertisedFRPHost:     "frp.example.test",
			AdvertisedFRPPort:     7000,
			InternalFRPToken:      "internal-token",
			Snapshot:              tunnelruntime.TunnelSnapshot{ClientKey: "client-id", Revision: 3},
		}); writeErr != nil {
			t.Errorf("write welcome: %v", writeErr)
		}
	}))
	defer server.Close()

	controlServer, err := normalizeControlPlaneURL(server.URL)
	if err != nil {
		t.Fatalf("normalize test server: %v", err)
	}
	authenticated := 0
	agent, err := NewClientAgent(ClientAgentOptions{
		Config:              ClientConfig{Server: controlServer, Token: "client-token"},
		YCYVersion:          "0.0.0-test",
		LastAppliedRevision: 3,
		OnAuthenticated: func() error {
			authenticated++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewClientAgent() error = %v", err)
	}

	connection, err := agent.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if connection == nil || connection.Welcome.Snapshot.Revision != 3 {
		t.Fatalf("Connect() = %#v, want validated welcome", connection)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	hello := <-hellos
	if hello.Type != "hello" || hello.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion || hello.YCYVersion != "0.0.0-test" || hello.LastAppliedRevision != 3 || hello.Platform != string(target.Platform) || hello.Architecture != string(target.Architecture) {
		t.Fatalf("hello = %#v", hello)
	}
	if authenticated != 1 {
		t.Fatalf("authenticated callbacks = %d, want 1", authenticated)
	}

	connection, err = agent.Connect(context.Background())
	if err != nil {
		t.Fatalf("second Connect() error = %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	<-hellos
	mu.Lock()
	gotProbes, gotUpgrades := probes, upgrades
	mu.Unlock()
	if gotProbes != 2 || gotUpgrades != 2 || authenticated != 1 {
		t.Fatalf("probe/upgrades/authenticated = %d/%d/%d, want 2/2/1", gotProbes, gotUpgrades, authenticated)
	}
}

func TestClientControlConnectionTimesOutWhenPongIsMissing(t *testing.T) {
	previousPing := clientControlPingInterval
	previousRead := clientControlReadTimeout
	clientControlPingInterval = 20 * time.Millisecond
	clientControlReadTimeout = 60 * time.Millisecond
	t.Cleanup(func() {
		clientControlPingInterval = previousPing
		clientControlReadTimeout = previousRead
	})
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
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
		socket.SetPingHandler(func(string) error { return nil })
		var hello tunnelruntime.AgentHello
		if err := socket.ReadJSON(&hello); err != nil {
			return
		}
		if err := socket.WriteJSON(tunnelruntime.AgentWelcome{
			Type: "welcome", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion, RequiredFRPVersion: tunnelruntime.FRPVersion,
			Artifact: artifact.Description, AdvertisedFRPHost: "frp.example.test", AdvertisedFRPPort: 7000, InternalFRPToken: "token",
			Snapshot: tunnelruntime.TunnelSnapshot{ClientKey: "client"},
		}); err != nil {
			return
		}
		for {
			if _, _, err := socket.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	controlServer, err := normalizeControlPlaneURL(server.URL)
	if err != nil {
		t.Fatalf("normalize server: %v", err)
	}
	agent, err := NewClientAgent(ClientAgentOptions{Config: ClientConfig{Server: controlServer, Token: "client-token"}})
	if err != nil {
		t.Fatalf("NewClientAgent() error = %v", err)
	}
	connection, err := agent.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer connection.Close()
	started := time.Now()
	if _, err := connection.ReadMessage(); err == nil {
		t.Fatal("ReadMessage() error = nil")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("liveness timeout took %s", elapsed)
	}
}

func TestClientAgentConnectTimesOutWhenWelcomeIsMissing(t *testing.T) {
	previousTimeout := clientControlAttemptTimeout
	clientControlAttemptTimeout = 80 * time.Millisecond
	t.Cleanup(func() { clientControlAttemptTimeout = previousTimeout })
	helloReceived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !websocket.IsWebSocketUpgrade(request) {
			writer.WriteHeader(http.StatusUpgradeRequired)
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
			return
		}
		close(helloReceived)
		_, _, _ = socket.ReadMessage()
	}))
	defer server.Close()
	controlServer, err := normalizeControlPlaneURL(server.URL)
	if err != nil {
		t.Fatalf("normalize server: %v", err)
	}
	agent, err := NewClientAgent(ClientAgentOptions{Config: ClientConfig{Server: controlServer, Token: "client-token"}})
	if err != nil {
		t.Fatalf("NewClientAgent() error = %v", err)
	}
	started := time.Now()
	if _, err := agent.Connect(context.Background()); err == nil {
		t.Fatal("Connect() error = nil")
	}
	select {
	case <-helloReceived:
	default:
		t.Fatal("client did not send hello before timing out")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("missing-welcome timeout took %s", elapsed)
	}
}

func TestClientControlReadErrorTreatsInvalidMessageCloseAsFatalProtocolError(t *testing.T) {
	err := clientControlReadError(&websocket.CloseError{Code: 4400, Text: "Invalid Restart Result"})
	if !errors.Is(err, ErrClientProtocol) {
		t.Fatalf("clientControlReadError() = %v, want ErrClientProtocol", err)
	}
}

func TestClientAgentConnectStopsBeforeUpgradeForRejectedToken(t *testing.T) {
	upgrades := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if websocket.IsWebSocketUpgrade(request) {
			upgrades++
		}
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	controlServer, err := normalizeControlPlaneURL(server.URL)
	if err != nil {
		t.Fatalf("normalize test server: %v", err)
	}
	agent, err := NewClientAgent(ClientAgentOptions{Config: ClientConfig{Server: controlServer, Token: "rejected-token"}, YCYVersion: "test"})
	if err != nil {
		t.Fatalf("NewClientAgent() error = %v", err)
	}
	if _, err := agent.Connect(context.Background()); !errors.Is(err, ErrClientAuthentication) {
		t.Fatalf("Connect() error = %v, want ErrClientAuthentication", err)
	}
	if upgrades != 0 {
		t.Fatalf("upgrades = %d, want 0", upgrades)
	}
}

func TestClientAgentConnectRejectsUnpinnedOrIncompatibleFirstFrame(t *testing.T) {
	for _, test := range []struct {
		name  string
		frame func(t *testing.T) any
		want  error
	}{
		{
			name: "unpinned artifact",
			frame: func(t *testing.T) any {
				artifact, err := tunnelruntime.CurrentFRPArtifact()
				if err != nil {
					t.Fatal(err)
				}
				return tunnelruntime.AgentWelcome{
					Type:                  "welcome",
					TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
					RequiredFRPVersion:    tunnelruntime.FRPVersion,
					Artifact: tunnelruntime.FRPArtifactDescription{
						Version: artifact.Description.Version, Archive: artifact.Description.Archive, URL: artifact.Description.URL,
						SHA256: "0" + artifact.Description.SHA256[1:], FRPCSHA256: artifact.Description.FRPCSHA256,
					},
					AdvertisedFRPHost: "frp.example.test", AdvertisedFRPPort: 7000, InternalFRPToken: "internal-token",
					Snapshot: tunnelruntime.TunnelSnapshot{ClientKey: "client-id", Revision: 0},
				}
			},
			want: ErrClientIncompatible,
		},
		{
			name: "incompatible frame",
			frame: func(*testing.T) any {
				return tunnelruntime.Incompatible{Type: "incompatible", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion, Message: "upgrade ycy"}
			},
			want: ErrClientIncompatible,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := clientAgentFirstFrameServer(t, test.frame(t))
			defer server.Close()
			controlServer, err := normalizeControlPlaneURL(server.URL)
			if err != nil {
				t.Fatalf("normalize test server: %v", err)
			}
			authenticated := 0
			agent, err := NewClientAgent(ClientAgentOptions{
				Config:     ClientConfig{Server: controlServer, Token: "client-token"},
				YCYVersion: "test",
				OnAuthenticated: func() error {
					authenticated++
					return nil
				},
			})
			if err != nil {
				t.Fatalf("NewClientAgent() error = %v", err)
			}
			if _, err := agent.Connect(context.Background()); !errors.Is(err, test.want) {
				t.Fatalf("Connect() error = %v, want %v", err, test.want)
			}
			if authenticated != 0 {
				t.Fatalf("authenticated callbacks = %d, want 0", authenticated)
			}
		})
	}
}

func clientAgentFirstFrameServer(t *testing.T, frame any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !websocket.IsWebSocketUpgrade(request) {
			writer.WriteHeader(http.StatusUpgradeRequired)
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
		if err := socket.WriteJSON(frame); err != nil {
			t.Errorf("write frame: %v", err)
		}
	}))
}
