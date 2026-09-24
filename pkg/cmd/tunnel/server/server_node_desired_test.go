package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestServerNodeDesiredSaveUpdatesPoolAtomically(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	if _, err := registry.register(context.Background(), id, "Remote", "http://127.0.0.1:7600", publicKey, 0); err != nil {
		t.Fatal(err)
	}
	settings := serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 30000}
	revision, err := registry.saveDesired(context.Background(), id, 0, settings)
	if err != nil || revision != 1 {
		t.Fatalf("save desired = (%d, %v)", revision, err)
	}
	record, err := registry.get(context.Background(), id)
	if err != nil || !record.DesiredSnapshot.Valid || record.DesiredRevision != 1 || record.PortEnd != 30000 {
		t.Fatalf("saved desired = (%+v, %v)", record, err)
	}
	var snapshot serverDesiredNodeSnapshot
	if err := json.Unmarshal([]byte(record.DesiredSnapshot.String), &snapshot); err != nil || snapshot.NodeID != id || snapshot.Revision != 1 || snapshot.Token == "" {
		t.Fatalf("complete desired snapshot = (%+v, %v)", snapshot, err)
	}
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), "environment-admin", "Remote client")
	if err != nil {
		t.Fatal(err)
	}
	port := int64(20001)
	if _, err := plane.CreateTunnel(context.Background(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 9000}); err != nil {
		t.Fatal(err)
	}
	if _, err := withImmediateTransaction(context.Background(), state.database, func(connection *sql.Conn) (struct{}, error) {
		if _, err := connection.ExecContext(context.Background(), `UPDATE clients SET node_id=? WHERE internal_id=?`, id, client.ID); err != nil {
			return struct{}{}, err
		}
		_, err := connection.ExecContext(context.Background(), `UPDATE tunnels SET node_id=? WHERE client_internal_id=?`, id, client.ID)
		return struct{}{}, err
	}); err != nil {
		t.Fatal(err)
	}
	settings.PortRangeStart = 20002
	if _, err := registry.saveDesired(context.Background(), id, 1, settings); err == nil {
		t.Fatal("pool shrink excluded occupied Tunnel")
	}
	if _, err := registry.saveDesired(context.Background(), id, 0, serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7001, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 30000}); err == nil {
		t.Fatal("stale expected revision overwrote desired state")
	}
	record, err = registry.get(context.Background(), id)
	if err != nil || record.DesiredRevision != 1 || record.PortEnd != 30000 {
		t.Fatalf("failed writes changed desired state: (%+v, %v)", record, err)
	}
}

func TestServerNodeReapplyAdvancesRevisionWithoutChangingSettings(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	id := "abcdef0123456789abcdef0123456789"
	if _, err := registry.register(context.Background(), id, "Remote", "http://127.0.0.1:7600", publicKey, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.reapply(context.Background(), id); err == nil {
		t.Fatal("unconfigured Node reapply succeeded")
	}
	settings := serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 20100, Custom404Page: "missing"}
	if _, err := registry.saveDesired(context.Background(), id, 0, settings); err != nil {
		t.Fatal(err)
	}
	first, err := registry.get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := registry.reapply(context.Background(), id)
	if err != nil || revision != 2 {
		t.Fatalf("reapply = (%d, %v)", revision, err)
	}
	second, err := registry.get(context.Background(), id)
	if err != nil || second.DesiredRevision != 2 || first.DesiredHash == second.DesiredHash {
		t.Fatalf("reapply did not save a new snapshot: (%+v, %v)", second, err)
	}
	var before, after serverDesiredNodeSnapshot
	if err := json.Unmarshal([]byte(first.DesiredSnapshot.String), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(second.DesiredSnapshot.String), &after); err != nil {
		t.Fatal(err)
	}
	before.Revision = 0
	after.Revision = 0
	if before != after {
		t.Fatalf("reapply changed settings: before=%+v after=%+v", before, after)
	}
}
