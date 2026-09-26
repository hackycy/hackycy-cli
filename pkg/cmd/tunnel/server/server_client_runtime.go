package server

import (
	"context"
	"database/sql"
	"fmt"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/serverclient"
	"github.com/hackycy/hackycy-cli/ent/server/tunnel"
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
	tx, err := serverEntForQueryer(database).Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	previous, err := tx.Meta.Get(ctx, serverLocalClientRuntimeMetaKey)
	if err != nil && !serverent.IsNotFound(err) {
		return fmt.Errorf("read Local Client runtime signature: %w", err)
	}
	if err == nil && previous.Value != signature {
		if _, err := tx.ServerClient.Update().Where(serverclient.NodeIDEQ("local")).AddDesiredRevision(1).Save(ctx); err != nil {
			return fmt.Errorf("advance Local Client runtime revision: %w", err)
		}
	}
	if previous == nil {
		_, err = tx.Meta.Create().SetID(serverLocalClientRuntimeMetaKey).SetValue(signature).Save(ctx)
	} else {
		_, err = tx.Meta.UpdateOne(previous).SetValue(signature).Save(ctx)
	}
	if err != nil {
		return fmt.Errorf("save Local Client runtime signature: %w", err)
	}
	return tx.Commit()
}

// BuildClientRuntime projects the complete Client runtime from one consistent
// database read. Endpoint and token are supplied by the owning FRPS runtime;
// all Client, Node, and Tunnel identity/version fields come from this read.
func (plane *ServerControlPlane) BuildClientRuntime(ctx context.Context, clientID, advertisedHost string, advertisedPort int64, frpToken string) (tunnelruntime.ClientRuntime, error) {
	if plane == nil || plane.database == nil {
		return tunnelruntime.ClientRuntime{}, fmt.Errorf("Tunnel server control plane is unavailable")
	}
	tx, err := serverEntForQueryer(plane.database).Tx(ctx)
	if err != nil {
		return tunnelruntime.ClientRuntime{}, err
	}
	defer tx.Rollback()
	client, err := tx.ServerClient.Get(ctx, clientID)
	if serverent.IsNotFound(err) {
		return tunnelruntime.ClientRuntime{}, serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	if err != nil {
		return tunnelruntime.ClientRuntime{}, err
	}
	nodeID := client.NodeID
	revision, clientKey := client.DesiredRevision, client.ID
	if nodeID != "local" {
		node, err := tx.Node.Get(ctx, nodeID)
		if err != nil || node.Lifecycle != "active" || node.AdvertisedFrpHost == nil || node.AdvertisedFrpPort == nil {
			return tunnelruntime.ClientRuntime{}, serverDomainError("NODE_TARGET_UNAVAILABLE", "Assigned Node has no complete FRP runtime projection")
		}
		remote, err := node.QueryRemoteNode().Only(ctx)
		if err != nil {
			return tunnelruntime.ClientRuntime{}, serverDomainError("NODE_TARGET_UNAVAILABLE", "Assigned Node has no complete FRP runtime projection")
		}
		advertisedHost, advertisedPort, frpToken = *node.AdvertisedFrpHost, int64(*node.AdvertisedFrpPort), remote.ActiveToken
	}
	items, err := tx.Tunnel.Query().Where(tunnel.ClientInternalIDEQ(clientID)).Order(tunnel.ByCreatedAt(), tunnel.ByID()).All(ctx)
	if err != nil {
		return tunnelruntime.ClientRuntime{}, fmt.Errorf("list Client Tunnel Definitions: %w", err)
	}
	tunnels := make([]tunnelruntime.TunnelDefinition, 0, len(items))
	for _, item := range items {
		mapped, err := serverTunnelFromEnt(item)
		if err != nil {
			return tunnelruntime.ClientRuntime{}, err
		}
		tunnels = append(tunnels, mapped.TunnelDefinition)
	}
	if err := tx.Commit(); err != nil {
		return tunnelruntime.ClientRuntime{}, err
	}
	runtime := tunnelruntime.ClientRuntime{
		Revision: revision, NodeID: nodeID,
		AdvertisedFRPHost: advertisedHost, AdvertisedFRPPort: advertisedPort,
		FRPToken: frpToken, ClientKey: clientKey, Tunnels: tunnels,
	}
	runtime.Digest, err = tunnelruntime.RuntimeDigest(runtime)
	return runtime, err
}
