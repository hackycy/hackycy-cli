package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/node"
	"github.com/hackycy/hackycy-cli/ent/server/serverclient"
	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

// requestNodeRemoval records the durable removal intent and its disabled
// snapshot in one transaction. The coordinator will apply that snapshot in a
// later slice after the lifecycle/API contract is wired.
func (registry *serverNodeRegistry) requestNodeRemoval(ctx context.Context, nodeID string) (int64, error) {
	return withImmediateTransaction(ctx, registry.database, func(connection *sql.Conn) (int64, error) {
		client := serverEntOnConnection(connection)
		current, err := client.Node.Query().Where(node.IDEQ(nodeID)).WithRemoteNode().Only(ctx)
		if serverent.IsNotFound(err) {
			return 0, serverDomainError("NOT_FOUND", "Node not found")
		}
		if err != nil {
			return 0, err
		}
		if current.Kind == "local" {
			return 0, serverDomainError("NODE_REMOVE_FORBIDDEN", "Local Node cannot be removed")
		}
		if current.Edges.RemoteNode == nil {
			return 0, fmt.Errorf("Remote Node record is incomplete")
		}
		remote := current.Edges.RemoteNode
		if current.Lifecycle == "removing" {
			return remote.DesiredRevision, nil
		}
		dependencyCount, err := nodeClientDependencyCount(ctx, client, nodeID)
		if err != nil {
			return 0, err
		}
		if dependencyCount != 0 {
			return 0, serverDomainError("NODE_HAS_CLIENTS", "Node has assigned or pending Clients")
		}
		if remote.DesiredRevision < 0 || remote.DesiredRevision == math.MaxInt64 {
			return 0, serverDomainError("NODE_REVISION_CONFLICT", "Node revision cannot advance")
		}
		revision := remote.DesiredRevision + 1
		// Disabled snapshots intentionally contain no Token. Applying one must
		// clear the Node's effective runtime credentials before completion.
		snapshot := serverDesiredNodeSnapshot{
			FormatVersion: 1,
			FRPVersion:    tunnelruntime.FRPVersion,
			NodeID:        nodeID,
			Revision:      revision,
			State:         "disabled",
		}
		contents, err := json.Marshal(snapshot)
		if err != nil {
			return 0, fmt.Errorf("encode Node disabled snapshot: %w", err)
		}
		digest := sha256.Sum256(contents)
		if _, err := client.Node.UpdateOne(current).SetLifecycle(node.LifecycleRemoving).SetUpdatedAt(formatServerTimestamp(time.Now())).Save(ctx); err != nil {
			return 0, fmt.Errorf("mark Node removing: %w", err)
		}
		if _, err := client.RemoteNode.UpdateOne(remote).SetDesiredRevision(revision).SetDesiredHash(hex.EncodeToString(digest[:])).SetDesiredSnapshot(string(contents)).Save(ctx); err != nil {
			return 0, fmt.Errorf("save Node disabled snapshot: %w", err)
		}
		return revision, nil
	})
}

func (coordinator *serverNodeCoordinator) finishNodeRemoval(ctx context.Context, record serverNodeRecord, status nodeStatus) bool {
	if !status.Claimed || status.HighestAcceptedRevision != record.DesiredRevision || status.AppliedRevision != record.DesiredRevision || status.SHA256 != record.DesiredHash.String || status.Phase != "disabled" || !status.BootDisabled || !status.DisabledComplete || status.FRPSProcess != "stopped" || status.FailureCode != "" || status.RecoveryError != "" {
		return false
	}
	removed, err := withImmediateTransaction(ctx, coordinator.registry.database, func(connection *sql.Conn) (bool, error) {
		client := serverEntOnConnection(connection)
		current, err := client.Node.Query().Where(node.IDEQ(record.ID)).WithRemoteNode().Only(ctx)
		if serverent.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if current.Lifecycle != "removing" || current.Edges.RemoteNode == nil || current.Edges.RemoteNode.DesiredHash == nil || current.Edges.RemoteNode.DesiredRevision != record.DesiredRevision || *current.Edges.RemoteNode.DesiredHash != record.DesiredHash.String {
			return false, nil
		}
		dependencies, err := nodeClientDependencyCount(ctx, client, record.ID)
		if err != nil {
			return false, err
		}
		if dependencies != 0 {
			return false, nil
		}
		if err := client.Node.DeleteOne(current).Exec(ctx); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil && removed {
		coordinator.observations.mu.Lock()
		delete(coordinator.observations.fresh, record.ID)
		coordinator.observations.mu.Unlock()
		coordinator.observations.notify()
	}
	return err == nil && removed
}

func (registry *serverNodeRegistry) forceForget(ctx context.Context, nodeID string, managementFault bool) error {
	_, err := withImmediateTransaction(ctx, registry.database, func(connection *sql.Conn) (struct{}, error) {
		client := serverEntOnConnection(connection)
		current, err := client.Node.Get(ctx, nodeID)
		if serverent.IsNotFound(err) {
			return struct{}{}, serverDomainError("NOT_FOUND", "Node not found")
		}
		if err != nil {
			return struct{}{}, err
		}
		if current.Kind == "local" {
			return struct{}{}, serverDomainError("NODE_REMOVE_FORBIDDEN", "Local Node cannot be forgotten")
		}
		dependencies, err := nodeClientDependencyCount(ctx, client, nodeID)
		if err != nil {
			return struct{}{}, err
		}
		if dependencies != 0 {
			return struct{}{}, serverDomainError("NODE_HAS_CLIENTS", "Node has assigned or pending Clients")
		}
		if current.Lifecycle != "removing" && !managementFault {
			return struct{}{}, serverDomainError("NODE_FORCE_FORGET_UNAVAILABLE", "Node can be forgotten only while removing or management is failing")
		}
		if err := client.Node.DeleteOne(current).Exec(ctx); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}

func nodeClientDependencyCount(ctx context.Context, client *serverent.Client, nodeID string) (int, error) {
	return client.ServerClient.Query().Where(serverclient.Or(serverclient.NodeIDEQ(nodeID), serverclient.PendingNodeIDEQ(nodeID))).Count(ctx)
}
