package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type serverNodeTokenPromotion struct {
	Promoted bool
	Events   []ServerControlPlaneEvent
}

func (registry *serverNodeRegistry) stageTokenRotation(ctx context.Context, nodeID string, expectedRevision int64) (int64, error) {
	token, err := randomClientToken(rand.Reader)
	if err != nil {
		return 0, err
	}
	return withImmediateTransaction(ctx, registry.database, func(connection *sql.Conn) (int64, error) {
		var revision int64
		var lifecycle string
		var savedSnapshot, stagedToken sql.NullString
		err := connection.QueryRowContext(ctx, `SELECT r.desired_revision,n.lifecycle,r.desired_snapshot,r.staged_token FROM remote_nodes r JOIN nodes n ON n.node_id=r.node_id WHERE r.node_id=?`, nodeID).Scan(&revision, &lifecycle, &savedSnapshot, &stagedToken)
		if err == sql.ErrNoRows {
			return 0, serverDomainError("NOT_FOUND", "Remote Node not found")
		}
		if err != nil {
			return 0, err
		}
		if lifecycle != "active" {
			return 0, serverDomainError("NODE_REMOVE_PENDING", "Node is pending removal")
		}
		if stagedToken.Valid {
			return 0, serverDomainError("NODE_TOKEN_ROTATION_PENDING", "Node Token rotation is already in progress")
		}
		if revision != expectedRevision {
			return 0, serverDomainError("REVISION_CONFLICT", "Node configuration changed; refresh before rotating Token")
		}
		var snapshot serverDesiredNodeSnapshot
		if !savedSnapshot.Valid || json.Unmarshal([]byte(savedSnapshot.String), &snapshot) != nil || snapshot.State != "running" || snapshot.NodeID != nodeID || snapshot.Revision != revision {
			return 0, serverDomainError("NODE_CONFIG_REJECTED", "Node has no valid running configuration")
		}
		revision++
		snapshot.Revision = revision
		snapshot.Token = token
		contents, err := json.Marshal(snapshot)
		if err != nil || len(contents) > nodeSnapshotLimit {
			return 0, serverDomainError("NODE_SNAPSHOT_TOO_LARGE", "Node configuration is too large")
		}
		digest := sha256.Sum256(contents)
		_, err = connection.ExecContext(ctx, `UPDATE remote_nodes SET desired_revision=?,desired_hash=?,desired_snapshot=?,staged_token=?,staged_token_revision=? WHERE node_id=?`, revision, hex.EncodeToString(digest[:]), string(contents), token, revision, nodeID)
		if err != nil {
			return 0, err
		}
		_, err = connection.ExecContext(ctx, `UPDATE nodes SET updated_at=? WHERE node_id=?`, formatServerTimestamp(time.Now()), nodeID)
		return revision, err
	})
}

func (registry *serverNodeRegistry) promoteTokenRotation(ctx context.Context, nodeID string, status nodeStatus) (serverNodeTokenPromotion, error) {
	return withImmediateTransaction(ctx, registry.database, func(connection *sql.Conn) (serverNodeTokenPromotion, error) {
		var revision int64
		var digest, savedSnapshot string
		var stagedToken sql.NullString
		var stagedRevision sql.NullInt64
		err := connection.QueryRowContext(ctx, `SELECT desired_revision,desired_hash,desired_snapshot,staged_token,staged_token_revision FROM remote_nodes WHERE node_id=?`, nodeID).Scan(&revision, &digest, &savedSnapshot, &stagedToken, &stagedRevision)
		if err == sql.ErrNoRows {
			return serverNodeTokenPromotion{}, nil
		}
		if err != nil {
			return serverNodeTokenPromotion{}, err
		}
		if !stagedToken.Valid || !stagedRevision.Valid || !status.Claimed || status.Phase != "applied" || status.HighestAcceptedRevision != revision || status.AppliedRevision != revision || status.SHA256 != digest || stagedRevision.Int64 != revision {
			return serverNodeTokenPromotion{}, nil
		}
		var snapshot serverDesiredNodeSnapshot
		if err := json.Unmarshal([]byte(savedSnapshot), &snapshot); err != nil || snapshot.NodeID != nodeID || snapshot.Revision != revision || snapshot.Token != stagedToken.String || snapshot.State != "running" {
			return serverNodeTokenPromotion{}, fmt.Errorf("saved staged Node snapshot is inconsistent")
		}
		hash := sha256.Sum256([]byte(savedSnapshot))
		if hex.EncodeToString(hash[:]) != digest {
			return serverNodeTokenPromotion{}, fmt.Errorf("saved staged Node digest is inconsistent")
		}
		rows, err := connection.QueryContext(ctx, `SELECT internal_id,owner_account_id,desired_revision FROM clients WHERE node_id=?`, nodeID)
		if err != nil {
			return serverNodeTokenPromotion{}, err
		}
		var events []ServerControlPlaneEvent
		for rows.Next() {
			var clientID, ownerID string
			var clientRevision int64
			if err := rows.Scan(&clientID, &ownerID, &clientRevision); err != nil {
				_ = rows.Close()
				return serverNodeTokenPromotion{}, err
			}
			if clientRevision >= serverMaximumSafeInteger {
				_ = rows.Close()
				return serverNodeTokenPromotion{}, serverDomainError("REVISION_CONFLICT", "Assigned Client revision cannot advance for Token rotation")
			}
			events = append(events, ServerControlPlaneEvent{Type: serverDesiredState, ClientID: clientID, OwnerAccountID: ownerID})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return serverNodeTokenPromotion{}, err
		}
		_ = rows.Close()
		if _, err := connection.ExecContext(ctx, `UPDATE remote_nodes SET active_token=staged_token,staged_token=NULL,staged_token_revision=NULL WHERE node_id=?`, nodeID); err != nil {
			return serverNodeTokenPromotion{}, err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE clients SET desired_revision=desired_revision+1 WHERE node_id=?`, nodeID); err != nil {
			return serverNodeTokenPromotion{}, err
		}
		return serverNodeTokenPromotion{Promoted: true, Events: events}, nil
	})
}
