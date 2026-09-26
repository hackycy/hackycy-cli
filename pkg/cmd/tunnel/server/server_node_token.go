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

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/remotenode"
	"github.com/hackycy/hackycy-cli/ent/server/serverclient"
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
		client := serverEntOnConnection(connection)
		remote, err := client.RemoteNode.Query().Where(remotenode.NodeIDEQ(nodeID)).WithNode().Only(ctx)
		if serverent.IsNotFound(err) {
			return 0, serverDomainError("NOT_FOUND", "Remote Node not found")
		}
		if err != nil {
			return 0, err
		}
		if remote.Edges.Node == nil || remote.Edges.Node.Lifecycle != "active" {
			return 0, serverDomainError("NODE_REMOVE_PENDING", "Node is pending removal")
		}
		if remote.StagedToken != nil {
			return 0, serverDomainError("NODE_TOKEN_ROTATION_PENDING", "Node Token rotation is already in progress")
		}
		if remote.DesiredRevision != expectedRevision {
			return 0, serverDomainError("REVISION_CONFLICT", "Node configuration changed; refresh before rotating Token")
		}
		var snapshot serverDesiredNodeSnapshot
		if remote.DesiredSnapshot == nil || json.Unmarshal([]byte(*remote.DesiredSnapshot), &snapshot) != nil || snapshot.State != "running" || snapshot.NodeID != nodeID || snapshot.Revision != remote.DesiredRevision {
			return 0, serverDomainError("NODE_CONFIG_REJECTED", "Node has no valid running configuration")
		}
		revision := remote.DesiredRevision + 1
		snapshot.Revision = revision
		snapshot.Token = token
		contents, err := json.Marshal(snapshot)
		if err != nil || len(contents) > nodeSnapshotLimit {
			return 0, serverDomainError("NODE_SNAPSHOT_TOO_LARGE", "Node configuration is too large")
		}
		digest := sha256.Sum256(contents)
		if _, err := client.RemoteNode.UpdateOne(remote).SetDesiredRevision(revision).
			SetDesiredHash(hex.EncodeToString(digest[:])).SetDesiredSnapshot(string(contents)).
			SetStagedToken(token).SetStagedTokenRevision(revision).Save(ctx); err != nil {
			return 0, err
		}
		_, err = client.Node.UpdateOneID(nodeID).SetUpdatedAt(formatServerTimestamp(time.Now())).Save(ctx)
		return revision, err
	})
}

func (registry *serverNodeRegistry) promoteTokenRotation(ctx context.Context, nodeID string, status nodeStatus) (serverNodeTokenPromotion, error) {
	return withImmediateTransaction(ctx, registry.database, func(connection *sql.Conn) (serverNodeTokenPromotion, error) {
		client := serverEntOnConnection(connection)
		remote, err := client.RemoteNode.Query().Where(remotenode.NodeIDEQ(nodeID)).Only(ctx)
		if serverent.IsNotFound(err) {
			return serverNodeTokenPromotion{}, nil
		}
		if err != nil {
			return serverNodeTokenPromotion{}, err
		}
		revision := remote.DesiredRevision
		if remote.StagedToken == nil || remote.StagedTokenRevision == nil || remote.DesiredHash == nil || remote.DesiredSnapshot == nil || !status.Claimed || status.Phase != "applied" || status.HighestAcceptedRevision != revision || status.AppliedRevision != revision || status.SHA256 != *remote.DesiredHash || *remote.StagedTokenRevision != revision {
			return serverNodeTokenPromotion{}, nil
		}
		digest, savedSnapshot, stagedToken := *remote.DesiredHash, *remote.DesiredSnapshot, *remote.StagedToken
		var snapshot serverDesiredNodeSnapshot
		if err := json.Unmarshal([]byte(savedSnapshot), &snapshot); err != nil || snapshot.NodeID != nodeID || snapshot.Revision != revision || snapshot.Token != stagedToken || snapshot.State != "running" {
			return serverNodeTokenPromotion{}, fmt.Errorf("saved staged Node snapshot is inconsistent")
		}
		hash := sha256.Sum256([]byte(savedSnapshot))
		if hex.EncodeToString(hash[:]) != digest {
			return serverNodeTokenPromotion{}, fmt.Errorf("saved staged Node digest is inconsistent")
		}
		assigned, err := client.ServerClient.Query().Where(serverclient.NodeIDEQ(nodeID)).All(ctx)
		if err != nil {
			return serverNodeTokenPromotion{}, err
		}
		var events []ServerControlPlaneEvent
		for _, item := range assigned {
			if item.DesiredRevision >= serverMaximumSafeInteger {
				return serverNodeTokenPromotion{}, serverDomainError("REVISION_CONFLICT", "Assigned Client revision cannot advance for Token rotation")
			}
			events = append(events, ServerControlPlaneEvent{Type: serverDesiredState, ClientID: item.ID, OwnerAccountID: item.OwnerAccountID})
		}
		if _, err := client.RemoteNode.UpdateOne(remote).SetActiveToken(stagedToken).ClearStagedToken().ClearStagedTokenRevision().Save(ctx); err != nil {
			return serverNodeTokenPromotion{}, err
		}
		if _, err := client.ServerClient.Update().Where(serverclient.NodeIDEQ(nodeID)).AddDesiredRevision(1).Save(ctx); err != nil {
			return serverNodeTokenPromotion{}, err
		}
		return serverNodeTokenPromotion{Promoted: true, Events: events}, nil
	})
}
