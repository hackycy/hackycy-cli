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

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

// requestNodeRemoval records the durable removal intent and its disabled
// snapshot in one transaction. The coordinator will apply that snapshot in a
// later slice after the lifecycle/API contract is wired.
func (registry *serverNodeRegistry) requestNodeRemoval(ctx context.Context, nodeID string) (int64, error) {
	return withImmediateTransaction(ctx, registry.database, func(connection *sql.Conn) (int64, error) {
		var kind, lifecycle string
		var currentRevision sql.NullInt64
		if err := connection.QueryRowContext(ctx, `
			SELECT n.kind,n.lifecycle,r.desired_revision
			FROM nodes n LEFT JOIN remote_nodes r ON r.node_id=n.node_id
			WHERE n.node_id=?`, nodeID).Scan(&kind, &lifecycle, &currentRevision); err != nil {
			if err == sql.ErrNoRows {
				return 0, serverDomainError("NOT_FOUND", "Node not found")
			}
			return 0, err
		}
		if kind == "local" {
			return 0, serverDomainError("NODE_REMOVE_FORBIDDEN", "Local Node cannot be removed")
		}
		if !currentRevision.Valid {
			return 0, fmt.Errorf("Remote Node record is incomplete")
		}
		if lifecycle == "removing" {
			return currentRevision.Int64, nil
		}
		var dependencyCount int
		if err := connection.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM clients
			WHERE node_id=? OR pending_node_id=?`, nodeID, nodeID).Scan(&dependencyCount); err != nil {
			return 0, err
		}
		if dependencyCount != 0 {
			return 0, serverDomainError("NODE_HAS_CLIENTS", "Node has assigned or pending Clients")
		}
		if currentRevision.Int64 < 0 || currentRevision.Int64 == math.MaxInt64 {
			return 0, serverDomainError("NODE_REVISION_CONFLICT", "Node revision cannot advance")
		}
		revision := currentRevision.Int64 + 1
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
		if _, err := connection.ExecContext(ctx, `UPDATE nodes SET lifecycle='removing',updated_at=? WHERE node_id=?`, formatServerTimestamp(time.Now()), nodeID); err != nil {
			return 0, fmt.Errorf("mark Node removing: %w", err)
		}
		if _, err := connection.ExecContext(ctx, `
			UPDATE remote_nodes
			SET desired_revision=?,desired_hash=?,desired_snapshot=?
			WHERE node_id=?`, revision, hex.EncodeToString(digest[:]), string(contents), nodeID); err != nil {
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
		var lifecycle, digest string
		var revision int64
		if err := connection.QueryRowContext(ctx, `
			SELECT n.lifecycle,r.desired_revision,r.desired_hash
			FROM nodes n JOIN remote_nodes r ON r.node_id=n.node_id
			WHERE n.node_id=?`, record.ID).Scan(&lifecycle, &revision, &digest); err != nil {
			if err == sql.ErrNoRows {
				return false, nil
			}
			return false, err
		}
		if lifecycle != "removing" || revision != record.DesiredRevision || digest != record.DesiredHash.String {
			return false, nil
		}
		var dependencies int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients WHERE node_id=? OR pending_node_id=?`, record.ID, record.ID).Scan(&dependencies); err != nil {
			return false, err
		}
		if dependencies != 0 {
			return false, nil
		}
		if _, err := connection.ExecContext(ctx, `DELETE FROM nodes WHERE node_id=?`, record.ID); err != nil {
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
		var kind, lifecycle string
		if err := connection.QueryRowContext(ctx, `SELECT kind,lifecycle FROM nodes WHERE node_id=?`, nodeID).Scan(&kind, &lifecycle); err != nil {
			if err == sql.ErrNoRows {
				return struct{}{}, serverDomainError("NOT_FOUND", "Node not found")
			}
			return struct{}{}, err
		}
		if kind == "local" {
			return struct{}{}, serverDomainError("NODE_REMOVE_FORBIDDEN", "Local Node cannot be forgotten")
		}
		var dependencies int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM clients WHERE node_id=? OR pending_node_id=?`, nodeID, nodeID).Scan(&dependencies); err != nil {
			return struct{}{}, err
		}
		if dependencies != 0 {
			return struct{}{}, serverDomainError("NODE_HAS_CLIENTS", "Node has assigned or pending Clients")
		}
		if lifecycle != "removing" && !managementFault {
			return struct{}{}, serverDomainError("NODE_FORCE_FORGET_UNAVAILABLE", "Node can be forgotten only while removing or management is failing")
		}
		if _, err := connection.ExecContext(ctx, `DELETE FROM nodes WHERE node_id=?`, nodeID); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nil
	})
	return err
}
