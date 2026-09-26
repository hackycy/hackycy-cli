package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"time"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/nodeportpool"
	"github.com/hackycy/hackycy-cli/ent/server/remotenode"
	"github.com/hackycy/hackycy-cli/ent/server/tunnel"
	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

type serverNodeSettings struct {
	BindAddress    string `json:"bindAddress"`
	BindPort       int64  `json:"bindPort"`
	VhostHTTPPort  int64  `json:"vhostHTTPPort"`
	PortRangeStart int64  `json:"portRangeStart"`
	PortRangeEnd   int64  `json:"portRangeEnd"`
	Custom404Page  string `json:"custom404Page"`
}

type serverDesiredNodeSnapshot struct {
	FormatVersion  int    `json:"formatVersion"`
	FRPVersion     string `json:"frpVersion"`
	NodeID         string `json:"nodeId"`
	Revision       int64  `json:"revision"`
	State          string `json:"state"`
	BindAddress    string `json:"bindAddress"`
	BindPort       int64  `json:"bindPort"`
	VhostHTTPPort  int64  `json:"vhostHTTPPort"`
	PortRangeStart int64  `json:"portRangeStart"`
	PortRangeEnd   int64  `json:"portRangeEnd"`
	Token          string `json:"token"`
	Custom404Page  string `json:"custom404Page,omitempty"`
}

func (registry *serverNodeRegistry) saveDesired(ctx context.Context, nodeID string, expectedRevision int64, settings serverNodeSettings) (int64, error) {
	if net.ParseIP(settings.BindAddress) == nil || !validServerNodePort(settings.BindPort) || !validServerNodePort(settings.VhostHTTPPort) || !validServerNodePort(settings.PortRangeStart) || !validServerNodePort(settings.PortRangeEnd) || settings.PortRangeStart > settings.PortRangeEnd || settings.BindPort == settings.VhostHTTPPort || settings.BindPort >= settings.PortRangeStart && settings.BindPort <= settings.PortRangeEnd || settings.VhostHTTPPort >= settings.PortRangeStart && settings.VhostHTTPPort <= settings.PortRangeEnd || len(settings.Custom404Page) > 512<<10 {
		return 0, serverDomainError("INVALID_NODE_SETTINGS", "Node FRPS settings are invalid")
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
		if remote.DesiredRevision != expectedRevision {
			return 0, serverDomainError("REVISION_CONFLICT", "Node configuration changed; refresh before saving")
		}
		occupied, err := client.Tunnel.Query().Where(
			tunnel.NodeIDEQ(nodeID), tunnel.ProtocolIn(tunnel.ProtocolTCP, tunnel.ProtocolUDP),
			tunnel.Or(tunnel.ServerPortLT(int(settings.PortRangeStart)), tunnel.ServerPortGT(int(settings.PortRangeEnd))),
		).Exist(ctx)
		if err != nil {
			return 0, err
		}
		if occupied {
			return 0, serverDomainError("NODE_RESOURCE_CONFLICT", "Node port pool excludes an occupied Tunnel port")
		}
		revision := remote.DesiredRevision + 1
		token := remote.ActiveToken
		if remote.StagedToken != nil {
			token = *remote.StagedToken
		}
		snapshot := serverDesiredNodeSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: nodeID, Revision: revision, State: "running", BindAddress: settings.BindAddress, BindPort: settings.BindPort, VhostHTTPPort: settings.VhostHTTPPort, PortRangeStart: settings.PortRangeStart, PortRangeEnd: settings.PortRangeEnd, Token: token, Custom404Page: settings.Custom404Page}
		contents, err := json.Marshal(snapshot)
		if err != nil || len(contents) > nodeSnapshotLimit {
			return 0, serverDomainError("NODE_SNAPSHOT_TOO_LARGE", "Node configuration is too large")
		}
		digest := sha256.Sum256(contents)
		update := client.RemoteNode.UpdateOne(remote).SetFrpBindPort(int(settings.BindPort)).
			SetHTTPVhostPort(int(settings.VhostHTTPPort)).SetPortStart(int(settings.PortRangeStart)).
			SetPortEnd(int(settings.PortRangeEnd)).SetDesiredRevision(revision).
			SetDesiredHash(hex.EncodeToString(digest[:])).SetDesiredSnapshot(string(contents))
		if remote.StagedToken != nil {
			update.SetStagedTokenRevision(revision)
		}
		if _, err := update.Save(ctx); err != nil {
			return 0, fmt.Errorf("save Node desired snapshot: %w", err)
		}
		pool, err := client.NodePortPool.Query().Where(nodeportpool.NodeIDEQ(nodeID)).Only(ctx)
		if err != nil {
			return 0, err
		}
		if _, err := client.NodePortPool.UpdateOne(pool).SetPortStart(int(settings.PortRangeStart)).SetPortEnd(int(settings.PortRangeEnd)).Save(ctx); err != nil {
			return 0, err
		}
		if _, err := client.Node.UpdateOneID(nodeID).SetUpdatedAt(formatServerTimestamp(time.Now())).Save(ctx); err != nil {
			return 0, err
		}
		return revision, nil
	})
}

func (registry *serverNodeRegistry) reapply(ctx context.Context, nodeID string) (int64, error) {
	record, err := registry.get(ctx, nodeID)
	if err != nil {
		return 0, err
	}
	if !record.DesiredSnapshot.Valid {
		return 0, serverDomainError("NODE_CONFIG_REJECTED", "Node has no running configuration to reapply")
	}
	var snapshot serverDesiredNodeSnapshot
	if err := json.Unmarshal([]byte(record.DesiredSnapshot.String), &snapshot); err != nil || snapshot.State != "running" || snapshot.NodeID != nodeID || snapshot.Revision != record.DesiredRevision {
		return 0, serverDomainError("NODE_CONFIG_REJECTED", "Saved Node configuration is invalid")
	}
	return registry.saveDesired(ctx, nodeID, record.DesiredRevision, serverNodeSettings{
		BindAddress: snapshot.BindAddress, BindPort: snapshot.BindPort,
		VhostHTTPPort: snapshot.VhostHTTPPort, PortRangeStart: snapshot.PortRangeStart,
		PortRangeEnd: snapshot.PortRangeEnd, Custom404Page: snapshot.Custom404Page,
	})
}

func validServerNodePort(port int64) bool { return port >= 1 && port <= 65535 }
