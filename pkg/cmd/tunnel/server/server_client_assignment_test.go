package server

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestMigrateClientNodeUpdatesV1OwnershipAtomically(t *testing.T) {
	state := openServerDomainState(t)
	ctx := t.Context()
	if _, err := state.database.ExecContext(ctx, `
		INSERT INTO nodes(node_id,kind,name,created_at,updated_at) VALUES('remote','remote','Remote','now','now');
		INSERT INTO node_port_pools(node_id,port_start,port_end) VALUES('remote',20000,20010);
		INSERT INTO clients(internal_id,owner_account_id,node_id,token,created_at) VALUES('client-1','environment-admin','local','token-1','now');
		INSERT INTO tunnels(id,client_internal_id,node_id,protocol,custom_domains,local_host,local_port,created_at,updated_at)
		VALUES('tunnel-1','client-1','local','http','["example.test"]','localhost',8080,'now','now');
		INSERT INTO tunnel_http_routes(id,tunnel_id,hostname,location) VALUES('route-1','tunnel-1','example.test','');
	`); err != nil {
		t.Fatal(err)
	}
	client, err := selectClient(ctx, state.database, "client-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = withImmediateTransaction(ctx, state.database, func(connection *sql.Conn) (struct{}, error) {
		return struct{}{}, migrateClientNode(ctx, connection, client, "remote")
	})
	if err != nil {
		t.Fatal(err)
	}
	var clientNode, tunnelNode string
	if err := state.database.QueryRowContext(ctx, "SELECT node_id FROM clients WHERE internal_id='client-1'").Scan(&clientNode); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRowContext(ctx, "SELECT node_id FROM tunnels WHERE id='tunnel-1'").Scan(&tunnelNode); err != nil {
		t.Fatal(err)
	}
	if clientNode != "remote" || tunnelNode != "remote" {
		t.Fatalf("Node ownership = (%q, %q), want remote", clientNode, tunnelNode)
	}
}

func TestClientNodeAssignmentMigratesResourcesAndKeepsPendingSeparate(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	plane, err := NewServerControlPlane(ServerControlPlaneOptions{Database: state.database, PortRange: ServerPortRange{Start: 20000, End: 20010}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`
		INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at)
		VALUES('owner', 'local', 'owner', 'owner', 'user', 'hash', 'now', 'now');
		INSERT INTO nodes(node_id, kind, name, advertised_frp_host, advertised_frp_port, created_at, updated_at)
		VALUES('remote', 'remote', 'Remote', 'remote.example.test', 7000, 'now', 'now');
		INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('remote', 20000, 20010);
	`); err != nil {
		t.Fatal(err)
	}
	client, err := plane.CreateClient(t.Context(), "owner", "assignment")
	if err != nil {
		t.Fatal(err)
	}
	port := int64(20001)
	if _, err := plane.CreateTunnel(t.Context(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 9000}); err != nil {
		t.Fatal(err)
	}
	host := "app.example.test"
	if _, err := plane.CreateTunnel(t.Context(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{host}, LocalPort: 9001}); err != nil {
		t.Fatal(err)
	}
	if _, err := plane.AssignClientNode(t.Context(), client.ID, "remote", true, false); err == nil {
		t.Fatal("online assignment accepted unavailable target")
	}
	unchanged, err := plane.GetClient(t.Context(), client.ID)
	if err != nil || unchanged.NodeID != "local" || unchanged.DesiredRevision != 2 {
		t.Fatalf("unavailable target mutated assignment = (%+v, %v)", unchanged, err)
	}
	updated, err := plane.AssignClientNode(t.Context(), client.ID, "remote", true, true)
	if err != nil {
		t.Fatalf("online assignment: %v", err)
	}
	if updated.NodeID != "remote" || updated.PendingNodeID != nil || updated.DesiredRevision != 3 {
		t.Fatalf("online assignment = %#v", updated)
	}
	var nodeID, ownerNode string
	if err := state.database.QueryRow(`SELECT node_id FROM tunnels WHERE client_internal_id=? AND protocol='tcp'`, client.ID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if nodeID != "remote" {
		t.Fatalf("migrated TCP node = %q", nodeID)
	}
	if err := state.database.QueryRow(`SELECT DISTINCT t.node_id FROM tunnel_http_routes r JOIN tunnels t ON t.id=r.tunnel_id WHERE r.hostname=?`, host).Scan(&ownerNode); err != nil {
		t.Fatal(err)
	}
	if ownerNode != "remote" {
		t.Fatalf("migrated hostname owner = %q", ownerNode)
	}

	if _, err := plane.AssignClientNode(t.Context(), client.ID, "local", false, false); err != nil {
		t.Fatal(err)
	}
	pending, err := plane.GetClient(t.Context(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.NodeID != "remote" || pending.PendingNodeID == nil || *pending.PendingNodeID != "local" {
		t.Fatalf("offline pending assignment = %#v", pending)
	}
	if _, err := plane.CancelPendingClientNode(t.Context(), client.ID); err != nil {
		t.Fatal(err)
	}
	cleared, err := plane.GetClient(t.Context(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.PendingNodeID != nil || cleared.NodeID != "remote" {
		t.Fatalf("cancelled assignment = %#v", cleared)
	}
}

func TestClientPendingNodeRechecksFreshObservationOnWelcome(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := newServerNodeObservations(state.database)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := strings.Repeat("a", 32)
	publicKey := make([]byte, 32)
	if _, err := registry.register(t.Context(), nodeID, "Remote", "http://127.0.0.1:7600", publicKey, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`UPDATE nodes SET advertised_frp_host='remote.example.test', advertised_frp_port=7000 WHERE node_id=?`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`UPDATE remote_nodes SET desired_snapshot='{}' WHERE node_id=?`, nodeID); err != nil {
		t.Fatal(err)
	}
	client, err := plane.CreateClient(t.Context(), environmentAdministratorID, "pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plane.AssignClientNode(t.Context(), client.ID, nodeID, false, false); err != nil {
		t.Fatal(err)
	}
	availability := &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning}
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane, FRPS: availability,
		WelcomeSource: serverAgentTestWelcomeSource{settings: ServerAgentWelcomeSettings{AdvertisedFRPHost: "local.example.test", AdvertisedFRPPort: 7000, InternalFRPToken: "local-token"}},
		Nodes:         newServerNodeService(registry, observations, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway.tryPendingClientNode(t.Context(), client.ID)
	stillPending, err := plane.GetClient(t.Context(), client.ID)
	if err != nil || stillPending.NodeID != "local" || stillPending.PendingNodeID == nil {
		t.Fatalf("unknown target changed pending = (%+v, %v)", stillPending, err)
	}
	if err := observations.recordStatus(t.Context(), nodeID, nodeStatus{Claimed: true, FRPSProcess: "running", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	reservation, err := gateway.Authorize(context.Background(), "Bearer "+client.Token)
	if err != nil {
		t.Fatal(err)
	}
	connection := reservation.Activate()
	if connection == nil {
		t.Fatal("activate pending Client")
	}
	defer connection.Close()
	hello := []byte(`{"type":"hello","tunnelProtocolVersion":5,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAccepted":{"revision":0,"nodeId":"","digest":""},"lastApplied":{"revision":0,"nodeId":"","digest":""}}`)
	if err := connection.AcceptHello(t.Context(), hello); err != nil {
		t.Fatal(err)
	}
	var welcome tunnelruntime.AgentWelcome
	protocolError := connection.PresentWelcome(t.Context(), "request.example.test", func(frame any) error {
		if first, ok := frame.(tunnelruntime.AgentWelcome); ok {
			welcome = first
		}
		return nil
	})
	if protocolError != nil {
		t.Fatal(protocolError)
	}
	if welcome.Runtime.NodeID != nodeID || welcome.Runtime.AdvertisedFRPHost != "remote.example.test" || welcome.Runtime.FRPToken == "local-token" {
		t.Fatalf("pending reconnect welcome = %+v", welcome.Runtime)
	}
	committed, err := plane.GetClient(t.Context(), client.ID)
	if err != nil || committed.NodeID != nodeID || committed.PendingNodeID != nil || committed.DesiredRevision != 1 {
		t.Fatalf("pending reconnect result = (%+v, %v)", committed, err)
	}
	port := int64(20000)
	if _, err := plane.CreateTunnel(t.Context(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 9000}); err != nil {
		t.Fatal(err)
	}
	second, err := plane.CreateClient(t.Context(), environmentAdministratorID, "conflicting pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plane.CreateTunnel(t.Context(), second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 9001}); err != nil {
		t.Fatal(err)
	}
	if _, err := plane.AssignClientNode(t.Context(), second.ID, nodeID, false, false); err != nil {
		t.Fatal(err)
	}
	gateway.tryPendingClientNode(t.Context(), second.ID)
	conflicted, err := plane.GetClient(t.Context(), second.ID)
	if err != nil || conflicted.NodeID != "local" || conflicted.PendingNodeID == nil || *conflicted.PendingNodeID != nodeID || conflicted.DesiredRevision != 1 {
		t.Fatalf("resource conflict on reconnect changed pending = (%+v, %v)", conflicted, err)
	}
}

func TestClientNodeAssignmentRollsBackOnTargetResourceConflict(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	plane, err := NewServerControlPlane(ServerControlPlaneOptions{Database: state.database, PortRange: ServerPortRange{Start: 20000, End: 20010}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`
		INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at)
		VALUES('owner', 'local', 'owner', 'owner', 'user', 'hash', 'now', 'now');
		INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('remote', 'remote', 'Remote', 'now', 'now');
		INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('remote', 20000, 20000);
	`); err != nil {
		t.Fatal(err)
	}
	client, err := plane.CreateClient(t.Context(), "owner", "assignment")
	if err != nil {
		t.Fatal(err)
	}
	other, err := plane.CreateClient(t.Context(), "owner", "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`UPDATE clients SET node_id='remote' WHERE internal_id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	port := int64(20000)
	if _, err := state.database.Exec(`INSERT INTO tunnels(id, client_internal_id, node_id, protocol, server_port, local_host, local_port, created_at, updated_at) VALUES('occupied', ?, 'remote', 'tcp', ?, '127.0.0.1', 9000, 'now', 'now')`, other.ID, port); err != nil {
		t.Fatal(err)
	}
	if _, err := plane.CreateTunnel(t.Context(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 9001}); err != nil {
		t.Fatal("source allocation should use Local Node: ", err)
	}
	if _, err := plane.AssignClientNode(t.Context(), client.ID, "remote", true, true); err == nil {
		t.Fatal("conflicting target assignment succeeded")
	}
	unchanged, err := plane.GetClient(t.Context(), client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.NodeID != "local" || unchanged.PendingNodeID != nil || unchanged.DesiredRevision != 1 {
		t.Fatalf("failed assignment mutated client = %#v", unchanged)
	}
	var tunnelNode string
	if err := state.database.QueryRow(`SELECT node_id FROM tunnels WHERE client_internal_id=?`, client.ID).Scan(&tunnelNode); err != nil {
		t.Fatal(err)
	}
	if tunnelNode != "local" {
		t.Fatalf("failed assignment migrated tunnel = %q", tunnelNode)
	}
}

func TestClientNodeAssignmentRejectsSplitHostnameAndRollsBack(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	if _, err := state.database.Exec(`
		INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('remote', 'remote', 'Remote', 'now', 'now');
		INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('remote', 20000, 20100);
	`); err != nil {
		t.Fatal(err)
	}
	first, err := plane.CreateClient(t.Context(), environmentAdministratorID, "first route")
	if err != nil {
		t.Fatal(err)
	}
	second, err := plane.CreateClient(t.Context(), environmentAdministratorID, "second route")
	if err != nil {
		t.Fatal(err)
	}
	host := "shared.example.test"
	for i, client := range []TrustedTunnelClient{first, second} {
		location := []string{"/first", "/second"}[i]
		if _, err := plane.CreateTunnel(t.Context(), client.ID, TunnelMutationInput{
			Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{host}, Location: &location, LocalPort: int64(9000 + i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := plane.AssignClientNode(t.Context(), first.ID, "remote", true, true); err == nil {
		t.Fatal("assignment split one hostname across Nodes")
	}
	client, err := plane.GetClient(t.Context(), first.ID)
	if err != nil || client.NodeID != "local" || client.PendingNodeID != nil || client.DesiredRevision != 1 {
		t.Fatalf("failed assignment changed Client = (%+v, %v)", client, err)
	}
	var owner string
	if err := state.database.QueryRow(`SELECT DISTINCT t.node_id FROM tunnel_http_routes r JOIN tunnels t ON t.id=r.tunnel_id WHERE r.hostname=?`, host).Scan(&owner); err != nil || owner != "local" {
		t.Fatalf("failed assignment changed hostname owner = (%q, %v)", owner, err)
	}
	var localTunnels, localRoutes, remoteTunnels, remoteRoutes int
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels WHERE node_id='local' AND protocol='http'`).Scan(&localTunnels); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnel_http_routes r JOIN tunnels t ON t.id=r.tunnel_id WHERE t.node_id='local' AND r.hostname=?`, host).Scan(&localRoutes); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels WHERE node_id='remote' AND protocol='http'`).Scan(&remoteTunnels); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnel_http_routes r JOIN tunnels t ON t.id=r.tunnel_id WHERE t.node_id='remote' AND r.hostname=?`, host).Scan(&remoteRoutes); err != nil {
		t.Fatal(err)
	}
	if localTunnels != 2 || localRoutes != 2 || remoteTunnels != 0 || remoteRoutes != 0 {
		t.Fatalf("failed assignment left partial resources: local=%d/%d remote=%d/%d", localTunnels, localRoutes, remoteTunnels, remoteRoutes)
	}
}

func TestClientPendingNodeCanBeReplacedWithoutMovingResources(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	if _, err := state.database.Exec(`
		INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('remote-a', 'remote', 'A', 'now', 'now');
		INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('remote-b', 'remote', 'B', 'now', 'now');
	`); err != nil {
		t.Fatal(err)
	}
	client, err := plane.CreateClient(t.Context(), environmentAdministratorID, "pending replace")
	if err != nil {
		t.Fatal(err)
	}
	port := int64(20000)
	if _, err := plane.CreateTunnel(t.Context(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 9000}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"remote-a", "remote-b"} {
		updated, err := plane.AssignClientNode(t.Context(), client.ID, target, false, false)
		if err != nil || updated.NodeID != "local" || updated.PendingNodeID == nil || *updated.PendingNodeID != target || updated.DesiredRevision != 1 {
			t.Fatalf("pending target %q = (%+v, %v)", target, updated, err)
		}
		var tunnelNode string
		if err := state.database.QueryRow(`SELECT node_id FROM tunnels WHERE client_internal_id=?`, client.ID).Scan(&tunnelNode); err != nil || tunnelNode != "local" {
			t.Fatalf("pending target %q moved Tunnel = (%q, %v)", target, tunnelNode, err)
		}
	}
	cancelled, err := plane.CancelPendingClientNode(t.Context(), client.ID)
	if err != nil || cancelled.NodeID != "local" || cancelled.PendingNodeID != nil || cancelled.DesiredRevision != 1 {
		t.Fatalf("cancelled replacement = (%+v, %v)", cancelled, err)
	}
}

func TestClientAppliedNodeIsRecordedSeparatelyFromDesiredAssignment(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(t.Context(), environmentAdministratorID, "applied")
	if err != nil {
		t.Fatal(err)
	}
	if err := plane.RecordAppliedRevision(t.Context(), client.ID, 0, "remote"); err == nil {
		t.Fatal("mismatched applied Node was accepted")
	}
	if err := plane.RecordAppliedRevision(t.Context(), client.ID, 0, "local"); err != nil {
		t.Fatal(err)
	}
	updated, err := plane.GetClient(t.Context(), client.ID)
	if err != nil || updated.LastAppliedNodeID == nil || *updated.LastAppliedNodeID != "local" {
		t.Fatalf("applied Node persistence = (%+v, %v)", updated, err)
	}
}
