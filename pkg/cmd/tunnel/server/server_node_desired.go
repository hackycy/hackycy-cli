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
		var current int64
		var token, lifecycle string
		var stagedToken sql.NullString
		err := connection.QueryRowContext(ctx, `SELECT r.desired_revision,r.active_token,n.lifecycle,r.staged_token FROM remote_nodes r JOIN nodes n ON n.node_id=r.node_id WHERE r.node_id=?`, nodeID).Scan(&current, &token, &lifecycle, &stagedToken)
		if err == sql.ErrNoRows {
			return 0, serverDomainError("NOT_FOUND", "Remote Node not found")
		}
		if err != nil {
			return 0, err
		}
		if lifecycle != "active" {
			return 0, serverDomainError("NODE_REMOVE_PENDING", "Node is pending removal")
		}
		if current != expectedRevision {
			return 0, serverDomainError("REVISION_CONFLICT", "Node configuration changed; refresh before saving")
		}
		var occupied int64
		err = connection.QueryRowContext(ctx, `SELECT server_port FROM tunnels WHERE node_id=? AND protocol IN ('tcp','udp') AND (server_port < ? OR server_port > ?) LIMIT 1`, nodeID, settings.PortRangeStart, settings.PortRangeEnd).Scan(&occupied)
		if err == nil {
			return 0, serverDomainError("NODE_RESOURCE_CONFLICT", "Node port pool excludes an occupied Tunnel port")
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
		revision := current + 1
		if stagedToken.Valid {
			token = stagedToken.String
		}
		snapshot := serverDesiredNodeSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: nodeID, Revision: revision, State: "running", BindAddress: settings.BindAddress, BindPort: settings.BindPort, VhostHTTPPort: settings.VhostHTTPPort, PortRangeStart: settings.PortRangeStart, PortRangeEnd: settings.PortRangeEnd, Token: token, Custom404Page: settings.Custom404Page}
		contents, err := json.Marshal(snapshot)
		if err != nil || len(contents) > nodeSnapshotLimit {
			return 0, serverDomainError("NODE_SNAPSHOT_TOO_LARGE", "Node configuration is too large")
		}
		digest := sha256.Sum256(contents)
		if _, err := connection.ExecContext(ctx, `UPDATE remote_nodes SET frp_bind_port=?,http_vhost_port=?,port_start=?,port_end=?,desired_revision=?,desired_hash=?,desired_snapshot=?,staged_token_revision=CASE WHEN staged_token IS NOT NULL THEN ? ELSE staged_token_revision END WHERE node_id=?`, settings.BindPort, settings.VhostHTTPPort, settings.PortRangeStart, settings.PortRangeEnd, revision, hex.EncodeToString(digest[:]), string(contents), revision, nodeID); err != nil {
			return 0, fmt.Errorf("save Node desired snapshot: %w", err)
		}
		if _, err := connection.ExecContext(ctx, `UPDATE node_port_pools SET port_start=?,port_end=? WHERE node_id=?`, settings.PortRangeStart, settings.PortRangeEnd, nodeID); err != nil {
			return 0, err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE nodes SET updated_at=? WHERE node_id=?`, formatServerTimestamp(time.Now()), nodeID); err != nil {
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
