package server

import (
	"context"
	"sync"
	"testing"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestNodeResourceTransactionsEnforcePortAndHostnameOwnership(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	plane, err := NewServerControlPlane(ServerControlPlaneOptions{
		Database: state.database, PortRange: ServerPortRange{Start: 20000, End: 20002},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`
		INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at)
		VALUES('owner', 'local', 'owner', 'owner', 'user', 'hash', 'now', 'now')
	`); err != nil {
		t.Fatal(err)
	}
	first, err := plane.CreateClient(t.Context(), "owner", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := plane.CreateClient(t.Context(), "owner", "second")
	if err != nil {
		t.Fatal(err)
	}
	port := int64(20001)
	input := TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 9000}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, client := range []TrustedTunnelClient{first, second} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			_, err := plane.CreateTunnel(context.Background(), id, input)
			results <- err
		}(client.ID)
	}
	wait.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("same-Node concurrent TCP port = %d success, %d failure", successes, failures)
	}
	var tunnelCount, revisionSum int
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels`).Scan(&tunnelCount); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT sum(desired_revision) FROM clients`).Scan(&revisionSum); err != nil {
		t.Fatal(err)
	}
	if tunnelCount != 1 || revisionSum != 1 {
		t.Fatalf("failed port transaction left partial writes: tunnels=%d revisions=%d", tunnelCount, revisionSum)
	}
	if _, err := state.database.Exec(`UPDATE node_port_pools SET port_start = 20002, port_end = 20002 WHERE node_id = 'local'`); err != nil {
		t.Fatal(err)
	}
	if _, err := plane.CreateTunnel(t.Context(), first.ID, TunnelMutationInput{
		Protocol: tunnelruntime.TunnelProtocolUDP, ServerPort: &port, LocalPort: 9000,
	}); err == nil {
		t.Fatal("Tunnel write ignored the newly saved Local port pool")
	}
	if _, err := plane.CreateTunnel(t.Context(), first.ID, TunnelMutationInput{
		Protocol: tunnelruntime.TunnelProtocolUDP, LocalPort: 9000,
	}); err != nil {
		t.Fatalf("allocate from newly saved Local port pool: %v", err)
	}
	if err := state.database.QueryRow(`SELECT server_port FROM tunnels WHERE protocol = 'udp'`).Scan(&port); err != nil || port != 20002 {
		t.Fatalf("UDP port after saved pool update = (%d, %v)", port, err)
	}
	for _, statement := range []string{
		`INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('remote', 'remote', 'Remote', 'now', 'now')`,
		`INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('remote', 20001, 20001)`,
		`INSERT INTO clients(internal_id, owner_account_id, node_id, token, created_at) VALUES('remote-client', 'owner', 'remote', 'remote-token', 'now')`,
		`INSERT INTO tunnels(id, client_internal_id, node_id, protocol, server_port, local_host, local_port, created_at, updated_at) VALUES('remote-tcp', 'remote-client', 'remote', 'tcp', 20001, '127.0.0.1', 9000, 'now', 'now')`,
	} {
		if _, err := state.database.Exec(statement); err != nil {
			t.Fatalf("cross-Node port reuse: %v", err)
		}
	}
	if _, err := plane.CreateTunnel(t.Context(), "remote-client", TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolUDP, LocalPort: 9001}); err != nil {
		t.Fatalf("Remote Node port pool allocation: %v", err)
	}
	var remotePort int64
	if err := state.database.QueryRow(`SELECT server_port FROM tunnels WHERE client_internal_id = 'remote-client' AND protocol = 'udp'`).Scan(&remotePort); err != nil || remotePort != 20001 {
		t.Fatalf("Remote Node allocated port = (%d, %v)", remotePort, err)
	}
	host := "one.example.test"
	if _, err := plane.CreateTunnel(t.Context(), first.ID, TunnelMutationInput{
		Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{host}, LocalPort: 9000,
	}); err != nil {
		t.Fatal(err)
	}
	otherLocation := "/other"
	if _, err := plane.CreateTunnel(t.Context(), "remote-client", TunnelMutationInput{
		Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{host}, Location: &otherLocation, LocalPort: 9000,
	}); err == nil {
		t.Fatal("cross-Node hostname split was accepted")
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels WHERE client_internal_id = 'remote-client' AND protocol = 'http'`).Scan(&tunnelCount); err != nil || tunnelCount != 0 {
		t.Fatalf("hostname conflict left partial Tunnel = (%d, %v)", tunnelCount, err)
	}
}

func TestRemoteNodeConcurrentAllocationKeepsPortNamespacesSeparate(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	if _, err := state.database.Exec(`
		INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('remote', 'remote', 'Remote', 'now', 'now');
		INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('remote', 20000, 20000);
		UPDATE node_port_pools SET port_start=20000, port_end=20000 WHERE node_id='local';
	`); err != nil {
		t.Fatal(err)
	}
	first, err := plane.CreateClient(t.Context(), environmentAdministratorID, "remote first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := plane.CreateClient(t.Context(), environmentAdministratorID, "remote second")
	if err != nil {
		t.Fatal(err)
	}
	local, err := plane.CreateClient(t.Context(), environmentAdministratorID, "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []TrustedTunnelClient{first, second} {
		if _, err := plane.AssignClientNode(t.Context(), client.ID, "remote", true, true); err != nil {
			t.Fatal(err)
		}
	}
	port := int64(20000)
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, client := range []TrustedTunnelClient{first, second} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			_, err := plane.CreateTunnel(context.Background(), id, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, LocalPort: 9000})
			results <- err
		}(client.ID)
	}
	wait.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent Remote TCP allocation = %d successes, %d failures", successes, failures)
	}
	if _, err := plane.CreateTunnel(t.Context(), local.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 9001}); err != nil {
		t.Fatalf("same TCP number on Local Node: %v", err)
	}
	if _, err := plane.CreateTunnel(t.Context(), first.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolUDP, ServerPort: &port, LocalPort: 9002}); err != nil {
		t.Fatalf("same number for Remote UDP: %v", err)
	}
	var remoteTCP, remoteUDP, localTCP, remoteRevisionSum int
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels WHERE node_id='remote' AND protocol='tcp' AND server_port=20000`).Scan(&remoteTCP); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels WHERE node_id='remote' AND protocol='udp' AND server_port=20000`).Scan(&remoteUDP); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels WHERE node_id='local' AND protocol='tcp' AND server_port=20000`).Scan(&localTCP); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT sum(desired_revision) FROM clients WHERE node_id='remote'`).Scan(&remoteRevisionSum); err != nil {
		t.Fatal(err)
	}
	if remoteTCP != 1 || remoteUDP != 1 || localTCP != 1 || remoteRevisionSum != 4 {
		t.Fatalf("port namespace or rollback: remote TCP=%d UDP=%d Local TCP=%d remote revisions=%d", remoteTCP, remoteUDP, localTCP, remoteRevisionSum)
	}
}

func TestConcurrentHTTPRoutesCannotSplitHostnameAcrossNodes(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	if _, err := state.database.Exec(`
		INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('remote', 'remote', 'Remote', 'now', 'now');
		INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('remote', 20000, 20100);
	`); err != nil {
		t.Fatal(err)
	}
	local, err := plane.CreateClient(t.Context(), environmentAdministratorID, "local route")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := plane.CreateClient(t.Context(), environmentAdministratorID, "remote route")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plane.AssignClientNode(t.Context(), remote.ID, "remote", true, true); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for i, client := range []TrustedTunnelClient{local, remote} {
		location := []string{"/local", "/remote"}[i]
		wait.Add(1)
		go func(id, location string) {
			defer wait.Done()
			_, err := plane.CreateTunnel(context.Background(), id, TunnelMutationInput{
				Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"shared.example.test"}, Location: &location, LocalPort: 9000,
			})
			results <- err
		}(client.ID, location)
	}
	wait.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent cross-Node hostname = %d successes, %d failures", successes, failures)
	}
	var owner string
	if err := state.database.QueryRow(`SELECT DISTINCT t.node_id FROM tunnel_http_routes r JOIN tunnels t ON t.id=r.tunnel_id WHERE r.hostname='shared.example.test'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var tunnelCount, routeCount, revisionSum int
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels WHERE protocol='http'`).Scan(&tunnelCount); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnel_http_routes r JOIN tunnels t ON t.id=r.tunnel_id WHERE r.hostname='shared.example.test' AND t.node_id=?`, owner).Scan(&routeCount); err != nil {
		t.Fatal(err)
	}
	if err := state.database.QueryRow(`SELECT sum(desired_revision) FROM clients WHERE internal_id IN (?, ?)`, local.ID, remote.ID).Scan(&revisionSum); err != nil {
		t.Fatal(err)
	}
	if tunnelCount != 1 || routeCount != 1 || revisionSum != 2 {
		t.Fatalf("hostname race left partial resources: owner=%q tunnels=%d routes=%d revisions=%d", owner, tunnelCount, routeCount, revisionSum)
	}
}
