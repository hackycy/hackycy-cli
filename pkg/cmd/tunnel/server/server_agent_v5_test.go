package server

import (
	"context"
	"testing"
)

func TestServerAgentRejectsV4HelloWithExplicitUpgradeClose(t *testing.T) {
	connection, _ := openServerAgentProtocolConnection(t)
	err := connection.AcceptHello(context.Background(), []byte(`{"type":"hello","tunnelProtocolVersion":4,"ycyVersion":"0.0.0-old","platform":"linux","architecture":"x64","lastAppliedRevision":0}`))
	if err == nil || err.CloseCode != serverAgentCloseIncompatible {
		t.Fatalf("AcceptHello(v4) = %v, want close code %d", err, serverAgentCloseIncompatible)
	}
}
