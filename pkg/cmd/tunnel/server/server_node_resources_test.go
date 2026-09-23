package server

import (
	"context"
	"database/sql"
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
		`INSERT INTO clients(internal_id, owner_account_id, node_id, token, created_at) VALUES('remote-client', 'owner', 'remote', 'remote-token', 'now')`,
		`INSERT INTO tunnels(id, client_internal_id, node_id, protocol, server_port, local_host, local_port, created_at, updated_at) VALUES('remote-tcp', 'remote-client', 'remote', 'tcp', 20001, '127.0.0.1', 9000, 'now', 'now')`,
	} {
		if _, err := state.database.Exec(statement); err != nil {
			t.Fatalf("cross-Node port reuse: %v", err)
		}
	}
	host := "one.example.test"
	if _, err := plane.CreateTunnel(t.Context(), first.ID, TunnelMutationInput{
		Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{host}, LocalPort: 9000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := withResourceTransaction(t.Context(), state.database, func(connection *sql.Conn) error {
		if _, err := connection.ExecContext(t.Context(), `
			INSERT INTO tunnels(id, client_internal_id, node_id, protocol, custom_domains, local_host, local_port, created_at, updated_at)
			VALUES('remote-http', 'remote-client', 'remote', 'http', '["one.example.test"]', '127.0.0.1', 9000, 'now', 'now')
		`); err != nil {
			return err
		}
		_, err := connection.ExecContext(t.Context(), `INSERT INTO tunnel_http_routes(tunnel_id, node_id, hostname, location) VALUES('remote-http', 'remote', ?, '/other')`, host)
		return err
	}); err == nil {
		t.Fatal("cross-Node hostname split was accepted")
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM tunnels WHERE id = 'remote-http'`).Scan(&tunnelCount); err != nil || tunnelCount != 0 {
		t.Fatalf("hostname conflict left partial Tunnel = (%d, %v)", tunnelCount, err)
	}
}

func withResourceTransaction(ctx context.Context, database *sql.DB, action func(*sql.Conn) error) error {
	_, err := withImmediateTransaction(ctx, database, func(connection *sql.Conn) (struct{}, error) {
		return struct{}{}, action(connection)
	})
	return err
}
