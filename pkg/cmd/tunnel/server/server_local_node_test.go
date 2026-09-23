package server

import (
	"context"
	"testing"
)

func TestSyncLocalNodeProjectionUsesStartupSettingsAndRollsBackExcludedPorts(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	settings := ServerHTTPServerSettings{
		Address: "127.0.0.1", ControlPort: 7500, FRPPort: 7000, HTTPPort: 8080,
		PortRange:        ServerHTTPPortRange{Start: 20000, End: 20010},
		AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "LOCAL.EXAMPLE", Port: 7000},
	}
	if err := syncLocalNodeProjection(t.Context(), state.database, settings); err != nil {
		t.Fatal(err)
	}
	var host string
	var start, end int
	if err := state.database.QueryRow(`
		SELECT nodes.advertised_frp_host, node_port_pools.port_start, node_port_pools.port_end
		FROM nodes JOIN node_port_pools USING(node_id) WHERE node_id = 'local'
	`).Scan(&host, &start, &end); err != nil || host != "local.example" || start != 20000 || end != 20010 {
		t.Fatalf("Local projection = (%q, %d-%d, %v)", host, start, end, err)
	}
	for _, statement := range []string{
		`INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at) VALUES('owner', 'local', 'owner', 'owner', 'user', 'hash', 'now', 'now')`,
		`INSERT INTO clients(internal_id, owner_account_id, token, created_at) VALUES('client', 'owner', 'token', 'now')`,
		`INSERT INTO tunnels(id, client_internal_id, protocol, server_port, local_host, local_port, created_at, updated_at) VALUES('tunnel', 'client', 'tcp', 20010, '127.0.0.1', 9000, 'now', 'now')`,
	} {
		if _, err := state.database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	settings.PortRange.End = 20009
	settings.AdvertiseFRPAddr.Host = "changed.example"
	if err := syncLocalNodeProjection(context.Background(), state.database, settings); err == nil {
		t.Fatal("syncLocalNodeProjection() accepted occupied port outside the new pool")
	}
	if err := state.database.QueryRow(`
		SELECT nodes.advertised_frp_host, node_port_pools.port_start, node_port_pools.port_end
		FROM nodes JOIN node_port_pools USING(node_id) WHERE node_id = 'local'
	`).Scan(&host, &start, &end); err != nil || host != "local.example" || start != 20000 || end != 20010 {
		t.Fatalf("Local projection changed after rejection = (%q, %d-%d, %v)", host, start, end, err)
	}
}

func TestSyncLocalNodeProjectionRejectsDuplicateNodeEndpoint(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.database.Exec(`
		INSERT INTO nodes(node_id, kind, name, advertised_frp_host, advertised_frp_port, created_at, updated_at)
		VALUES('remote', 'remote', 'Remote', 'shared.example', 7000, 'now', 'now')
	`); err != nil {
		t.Fatal(err)
	}
	settings := ServerHTTPServerSettings{
		Address: "127.0.0.1", ControlPort: 7500, FRPPort: 7000, HTTPPort: 8080,
		PortRange:        ServerHTTPPortRange{Start: 20000, End: 20010},
		AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "SHARED.EXAMPLE", Port: 7000},
	}
	if err := syncLocalNodeProjection(t.Context(), state.database, settings); err == nil {
		t.Fatal("syncLocalNodeProjection() accepted duplicate Node endpoint")
	}
	var count int
	if err := state.database.QueryRow(`SELECT count(*) FROM node_port_pools WHERE node_id = 'local'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("Local pool after endpoint conflict = (%d, %v)", count, err)
	}
}
