package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestServerAgentFRPCStatusRequiresCurrentRuntimeAndExpires(t *testing.T) {
	connection, _ := openServerAgentProtocolConnection(t)
	connection.gateway.welcomeSource = serverAgentTestWelcomeSource{settings: ServerAgentWelcomeSettings{
		AdvertisedFRPHost: "frp.example.test", AdvertisedFRPPort: 7000, InternalFRPToken: "test-token",
	}}
	hello := []byte(`{"type":"hello","tunnelProtocolVersion":5,"ycyVersion":"test","platform":"linux","architecture":"x64","lastAccepted":{"revision":0,"nodeId":"","digest":""},"lastApplied":{"revision":0,"nodeId":"","digest":""}}`)
	if err := connection.AcceptHello(context.Background(), hello); err != nil {
		t.Fatal(err)
	}
	if err := connection.PresentWelcome(context.Background(), "request.example.test", func(any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	runtime, err := connection.expectedRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	status := tunnelruntime.FRPCStatus{
		Type: "frpc_status", TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
		Revision: runtime.Revision, NodeID: runtime.NodeID, Digest: runtime.Digest,
		ProcessGeneration: "generation-1", Process: tunnelruntime.FRPProcessRunning,
		Connection: "unknown", Proxies: []tunnelruntime.ProxyState{},
	}
	frame, _ := json.Marshal(status)
	if err := connection.AcceptActiveMessage(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
	if observed := connection.gateway.FRPCObservation(connection.ClientID()); observed.Connection != "unknown" || observed.Process != tunnelruntime.FRPProcessRunning || observed.ProcessGeneration != "generation-1" {
		t.Fatalf("accepted FRPC observation = %#v", observed)
	}
	status.Digest = "sha256:wrong"
	frame, _ = json.Marshal(status)
	if err := connection.AcceptActiveMessage(context.Background(), frame); err == nil || err.CloseCode != serverAgentCloseInvalidMessage {
		t.Fatalf("wrong digest result = %v", err)
	}
	status.Digest = runtime.Digest
	status.Connection = "connected"
	frame, _ = json.Marshal(status)
	if err := connection.AcceptActiveMessage(context.Background(), frame); err == nil || err.CloseCode != serverAgentCloseInvalidMessage {
		t.Fatalf("unproven connection result = %v", err)
	}
	status.Connection = "unknown"
	connection.gateway.mu.Lock()
	stored := connection.gateway.runtime[connection.ClientID()]
	stored.frpcReceivedAt = time.Now().Add(-serverAgentFRPCObservationTTL - time.Second)
	connection.gateway.runtime[connection.ClientID()] = stored
	connection.gateway.mu.Unlock()
	if observed := connection.gateway.FRPCObservation(connection.ClientID()); observed.Connection != "unknown" || observed.ProcessGeneration != "" {
		t.Fatalf("expired FRPC observation = %#v", observed)
	}
	status.Digest = runtime.Digest
	frame, _ = json.Marshal(status)
	if err := connection.AcceptActiveMessage(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if observed := connection.gateway.FRPCObservation(connection.ClientID()); observed.Connection != "unknown" || observed.ProcessGeneration != "" {
		t.Fatalf("disconnected FRPC observation = %#v", observed)
	}
}
