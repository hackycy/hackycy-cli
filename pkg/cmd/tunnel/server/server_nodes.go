package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type serverNodeRecord struct {
	ID                       string
	Kind                     string
	Name                     string
	Lifecycle                string
	ManagementAddress        string
	PendingManagementAddress string
	CandidateError           string
	PublicKey                []byte
	AdvertisedFRPHost        sql.NullString
	AdvertisedFRPPort        sql.NullInt64
	HTTPIngressHost          sql.NullString
	HTTPIngressPort          sql.NullInt64
	DesiredRevision          int64
	DesiredHash              sql.NullString
	DesiredSnapshot          sql.NullString
	StagedTokenRevision      sql.NullInt64
	FRPBindPort              int64
	HTTPVhostPort            int64
	PortStart                int64
	PortEnd                  int64
	CreatedAt                string
	UpdatedAt                string
}

type serverNodeRegistry struct {
	database *sql.DB
}

func newServerNodeRegistry(database *sql.DB) (*serverNodeRegistry, error) {
	if database == nil {
		return nil, fmt.Errorf("Node registry database is required")
	}
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS node_management_candidates (
		node_id TEXT PRIMARY KEY REFERENCES nodes(node_id) ON DELETE CASCADE,
		candidate_address TEXT NOT NULL,
		failure_code TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return nil, fmt.Errorf("initialize Node management-address candidates: %w", err)
	}
	return &serverNodeRegistry{database: database}, nil
}

func (registry *serverNodeRegistry) register(ctx context.Context, nodeID, name, managementAddress string, publicKey []byte, highestRevision int64) (serverNodeRecord, error) {
	name = strings.TrimSpace(name)
	address, err := normalizeNodeManagementAddress(managementAddress)
	if err != nil {
		return serverNodeRecord{}, err
	}
	if len(nodeID) != 32 || nodeID == "local" || len(publicKey) != 32 || name == "" || utf16CodeUnitCount(name) > 100 || highestRevision < 0 {
		return serverNodeRecord{}, serverDomainError("INVALID_NODE", "Node identity or display name is invalid")
	}
	if _, err := hex.DecodeString(nodeID); err != nil {
		return serverNodeRecord{}, serverDomainError("INVALID_NODE", "Node identity is invalid")
	}
	token, err := randomClientToken(rand.Reader)
	if err != nil {
		return serverNodeRecord{}, err
	}
	createdAt := formatServerTimestamp(time.Now())
	result, err := withImmediateTransaction(ctx, registry.database, func(connection *sql.Conn) (serverNodeRecord, error) {
		_, err := connection.ExecContext(ctx, `INSERT INTO nodes(node_id,kind,name,lifecycle,created_at,updated_at) VALUES(?,'remote',?,'active',?,?)`, nodeID, name, createdAt, createdAt)
		if err != nil {
			return serverNodeRecord{}, serverDomainError("NODE_ALREADY_REGISTERED", "Node is already registered")
		}
		_, err = connection.ExecContext(ctx, `INSERT INTO remote_nodes(node_id,node_public_key,management_address,frp_bind_port,http_vhost_port,port_start,port_end,desired_revision,active_token) VALUES(?,?,?,?,?,?,?,?,?)`, nodeID, hex.EncodeToString(publicKey), address, 7000, 8080, 20000, 29999, highestRevision, token)
		if err != nil {
			return serverNodeRecord{}, serverDomainError("NODE_ALREADY_REGISTERED", "Node identity or management address is already registered")
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO node_port_pools(node_id,port_start,port_end) VALUES(?,?,?)`, nodeID, 20000, 29999); err != nil {
			return serverNodeRecord{}, err
		}
		return serverNodeRecord{ID: nodeID, Kind: "remote", Name: name, Lifecycle: "active", ManagementAddress: address, PublicKey: append([]byte(nil), publicKey...), DesiredRevision: highestRevision, FRPBindPort: 7000, HTTPVhostPort: 8080, PortStart: 20000, PortEnd: 29999, CreatedAt: createdAt, UpdatedAt: createdAt}, nil
	})
	return result, err
}

func (registry *serverNodeRegistry) get(ctx context.Context, nodeID string) (serverNodeRecord, error) {
	row := registry.database.QueryRowContext(ctx, `SELECT n.node_id,n.kind,n.name,n.lifecycle,n.advertised_frp_host,n.advertised_frp_port,n.http_ingress_host,n.http_ingress_port,n.created_at,n.updated_at,r.node_public_key,r.management_address,r.desired_revision,r.desired_hash,r.desired_snapshot,r.staged_token_revision,r.frp_bind_port,r.http_vhost_port,r.port_start,r.port_end FROM nodes n JOIN remote_nodes r ON r.node_id=n.node_id WHERE n.node_id=?`, nodeID)
	var record serverNodeRecord
	var publicHex string
	if err := row.Scan(&record.ID, &record.Kind, &record.Name, &record.Lifecycle, &record.AdvertisedFRPHost, &record.AdvertisedFRPPort, &record.HTTPIngressHost, &record.HTTPIngressPort, &record.CreatedAt, &record.UpdatedAt, &publicHex, &record.ManagementAddress, &record.DesiredRevision, &record.DesiredHash, &record.DesiredSnapshot, &record.StagedTokenRevision, &record.FRPBindPort, &record.HTTPVhostPort, &record.PortStart, &record.PortEnd); err != nil {
		if err == sql.ErrNoRows {
			return serverNodeRecord{}, serverDomainError("NOT_FOUND", "Node not found")
		}
		return serverNodeRecord{}, err
	}
	var err error
	record.PublicKey, err = hex.DecodeString(publicHex)
	if err != nil || len(record.PublicKey) != 32 {
		return serverNodeRecord{}, fmt.Errorf("stored Node identity is invalid")
	}
	err = registry.database.QueryRowContext(ctx, `SELECT candidate_address,failure_code FROM node_management_candidates WHERE node_id=?`, nodeID).Scan(&record.PendingManagementAddress, &record.CandidateError)
	if err != nil && err != sql.ErrNoRows {
		return serverNodeRecord{}, err
	}
	return record, nil
}

func (registry *serverNodeRegistry) listRemote(ctx context.Context) ([]serverNodeRecord, error) {
	rows, err := registry.database.QueryContext(ctx, `SELECT node_id FROM nodes WHERE kind='remote' ORDER BY created_at,node_id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	result := make([]serverNodeRecord, 0, len(ids))
	for _, id := range ids {
		record, err := registry.get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}
