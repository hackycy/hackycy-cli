package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

const serverLocalClientRuntimeMetaKey = "local_client_runtime_digest"

func syncLocalClientRuntimeRevision(ctx context.Context, database *sql.DB, advertisedHost string, advertisedPort int64, frpToken string) error {
	signature, err := tunnelruntime.RuntimeDigest(tunnelruntime.ClientRuntime{
		NodeID: "local", AdvertisedFRPHost: advertisedHost, AdvertisedFRPPort: advertisedPort, FRPToken: frpToken,
	})
	if err != nil {
		return err
	}
	_, err = withImmediateTransaction(ctx, database, func(connection *sql.Conn) (struct{}, error) {
		var previous string
		lookupErr := connection.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, serverLocalClientRuntimeMetaKey).Scan(&previous)
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			return struct{}{}, fmt.Errorf("read Local Client runtime signature: %w", lookupErr)
		}
		if lookupErr == nil && previous != signature {
			if _, err := connection.ExecContext(ctx, `UPDATE clients SET desired_revision = desired_revision + 1 WHERE node_id = 'local'`); err != nil {
				return struct{}{}, fmt.Errorf("advance Local Client runtime revision: %w", err)
			}
		}
		if _, err := connection.ExecContext(ctx, `
			INSERT INTO meta(key, value) VALUES(?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value
		`, serverLocalClientRuntimeMetaKey, signature); err != nil {
			return struct{}{}, fmt.Errorf("save Local Client runtime signature: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

// BuildClientRuntime projects the complete Client runtime from one consistent
// database read. Endpoint and token are supplied by the owning FRPS runtime;
// all Client, Node, and Tunnel identity/version fields come from this read.
func (plane *ServerControlPlane) BuildClientRuntime(ctx context.Context, clientID, advertisedHost string, advertisedPort int64, frpToken string) (tunnelruntime.ClientRuntime, error) {
	if plane == nil || plane.database == nil {
		return tunnelruntime.ClientRuntime{}, fmt.Errorf("Tunnel server control plane is unavailable")
	}
	return withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (tunnelruntime.ClientRuntime, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return tunnelruntime.ClientRuntime{}, err
		}
		var nodeID string
		if err := connection.QueryRowContext(ctx, `SELECT node_id FROM clients WHERE internal_id = ?`, clientID).Scan(&nodeID); err != nil {
			return tunnelruntime.ClientRuntime{}, fmt.Errorf("read Client Node: %w", err)
		}
		if nodeID != "local" {
			var nodeHost string
			var nodePort int64
			var nodeToken string
			if err := connection.QueryRowContext(ctx, `
				SELECT n.advertised_frp_host, n.advertised_frp_port, r.active_token
				FROM nodes n JOIN remote_nodes r ON r.node_id = n.node_id
				WHERE n.node_id = ? AND n.lifecycle = 'active'`, nodeID).Scan(&nodeHost, &nodePort, &nodeToken); err != nil {
				return tunnelruntime.ClientRuntime{}, serverDomainError("NODE_TARGET_UNAVAILABLE", "Assigned Node has no complete FRP runtime projection")
			}
			advertisedHost, advertisedPort, frpToken = nodeHost, nodePort, nodeToken
		}
		rows, err := connection.QueryContext(ctx, `
			SELECT id, client_internal_id, node_id, label, protocol, custom_domains, location, server_port,
			       local_host, local_port, enabled, options_json, created_at, updated_at
			FROM tunnels WHERE client_internal_id = ? ORDER BY created_at, id
		`, clientID)
		if err != nil {
			return tunnelruntime.ClientRuntime{}, fmt.Errorf("list Client Tunnel Definitions: %w", err)
		}
		defer rows.Close()
		tunnels := make([]tunnelruntime.TunnelDefinition, 0)
		for rows.Next() {
			tunnel, err := scanTunnel(rows)
			if err != nil {
				return tunnelruntime.ClientRuntime{}, err
			}
			tunnels = append(tunnels, tunnel.TunnelDefinition)
		}
		if err := rows.Err(); err != nil {
			return tunnelruntime.ClientRuntime{}, fmt.Errorf("iterate Client Tunnel Definitions: %w", err)
		}
		runtime := tunnelruntime.ClientRuntime{
			Revision:          client.DesiredRevision,
			NodeID:            nodeID,
			AdvertisedFRPHost: advertisedHost,
			AdvertisedFRPPort: advertisedPort,
			FRPToken:          frpToken,
			ClientKey:         client.ID,
			Tunnels:           tunnels,
		}
		runtime.Digest, err = tunnelruntime.RuntimeDigest(runtime)
		if err != nil {
			return tunnelruntime.ClientRuntime{}, err
		}
		return runtime, nil
	})
}
