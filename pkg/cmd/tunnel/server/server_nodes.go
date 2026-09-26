package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/node"
	"github.com/hackycy/hackycy-cli/ent/server/remotenode"
	sqlite3 "github.com/ncruces/go-sqlite3"
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
		client := serverEntOnConnection(connection)
		_, err := client.Node.Create().SetID(nodeID).SetKind(node.KindRemote).SetName(name).
			SetLifecycle(node.LifecycleActive).SetCreatedAt(createdAt).SetUpdatedAt(createdAt).Save(ctx)
		if err != nil {
			return serverNodeRecord{}, mapNodeRegistrationConstraintError(ctx, client, nodeID, err)
		}
		publicKeyHex := hex.EncodeToString(publicKey)
		_, err = client.RemoteNode.Create().SetNodeID(nodeID).SetNodePublicKey(publicKeyHex).
			SetManagementAddress(address).SetFrpBindPort(7000).SetHTTPVhostPort(8080).
			SetPortStart(20000).SetPortEnd(29999).SetDesiredRevision(highestRevision).
			SetActiveToken(token).Save(ctx)
		if err != nil {
			return serverNodeRecord{}, mapRemoteNodeRegistrationConstraintError(ctx, client, nodeID, publicKeyHex, address, err)
		}
		if _, err := client.NodePortPool.Create().SetNodeID(nodeID).SetPortStart(20000).SetPortEnd(29999).Save(ctx); err != nil {
			return serverNodeRecord{}, err
		}
		return serverNodeRecord{ID: nodeID, Kind: "remote", Name: name, Lifecycle: "active", ManagementAddress: address, PublicKey: append([]byte(nil), publicKey...), DesiredRevision: highestRevision, FRPBindPort: 7000, HTTPVhostPort: 8080, PortStart: 20000, PortEnd: 29999, CreatedAt: createdAt, UpdatedAt: createdAt}, nil
	})
	return result, err
}

func mapNodeRegistrationConstraintError(ctx context.Context, client *serverent.Client, nodeID string, err error) error {
	if errors.Is(err, sqlite3.CONSTRAINT_PRIMARYKEY) || errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) {
		registered, checkErr := client.Node.Query().Where(node.IDEQ(nodeID)).Exist(ctx)
		if checkErr != nil {
			return fmt.Errorf("check Node registration conflict: %w", checkErr)
		}
		if registered {
			return serverDomainError("NODE_ALREADY_REGISTERED", "Node is already registered")
		}
	}
	return fmt.Errorf("register Node: %w", err)
}

func mapRemoteNodeRegistrationConstraintError(ctx context.Context, client *serverent.Client, nodeID, publicKey, address string, err error) error {
	if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) {
		registered, checkErr := client.RemoteNode.Query().Where(remotenode.Or(
			remotenode.NodeIDEQ(nodeID), remotenode.NodePublicKeyEQ(publicKey), remotenode.ManagementAddressEQ(address),
		)).Exist(ctx)
		if checkErr != nil {
			return fmt.Errorf("check Remote Node registration conflict: %w", checkErr)
		}
		if registered {
			return serverDomainError("NODE_ALREADY_REGISTERED", "Node identity or management address is already registered")
		}
	}
	return fmt.Errorf("register Remote Node: %w", err)
}

func (registry *serverNodeRegistry) get(ctx context.Context, nodeID string) (serverNodeRecord, error) {
	item, err := serverEntForQueryer(registry.database).Node.Query().Where(node.IDEQ(nodeID), node.KindEQ(node.KindRemote)).WithRemoteNode().WithManagementCandidate().Only(ctx)
	if serverent.IsNotFound(err) {
		return serverNodeRecord{}, serverDomainError("NOT_FOUND", "Node not found")
	}
	if err != nil {
		return serverNodeRecord{}, err
	}
	remote := item.Edges.RemoteNode
	if remote == nil {
		return serverNodeRecord{}, fmt.Errorf("stored Remote Node record is missing")
	}
	record := serverNodeRecord{
		ID: item.ID, Kind: string(item.Kind), Name: item.Name, Lifecycle: string(item.Lifecycle),
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		ManagementAddress: remote.ManagementAddress, DesiredRevision: remote.DesiredRevision,
		FRPBindPort: int64(remote.FrpBindPort), HTTPVhostPort: int64(remote.HTTPVhostPort),
		PortStart: int64(remote.PortStart), PortEnd: int64(remote.PortEnd),
	}
	if item.AdvertisedFrpHost != nil {
		record.AdvertisedFRPHost = sql.NullString{String: *item.AdvertisedFrpHost, Valid: true}
	}
	if item.AdvertisedFrpPort != nil {
		record.AdvertisedFRPPort = sql.NullInt64{Int64: int64(*item.AdvertisedFrpPort), Valid: true}
	}
	if item.HTTPIngressHost != nil {
		record.HTTPIngressHost = sql.NullString{String: *item.HTTPIngressHost, Valid: true}
	}
	if item.HTTPIngressPort != nil {
		record.HTTPIngressPort = sql.NullInt64{Int64: int64(*item.HTTPIngressPort), Valid: true}
	}
	if remote.DesiredHash != nil {
		record.DesiredHash = sql.NullString{String: *remote.DesiredHash, Valid: true}
	}
	if remote.DesiredSnapshot != nil {
		record.DesiredSnapshot = sql.NullString{String: *remote.DesiredSnapshot, Valid: true}
	}
	if remote.StagedTokenRevision != nil {
		record.StagedTokenRevision = sql.NullInt64{Int64: *remote.StagedTokenRevision, Valid: true}
	}
	if item.Edges.ManagementCandidate != nil {
		record.PendingManagementAddress = item.Edges.ManagementCandidate.CandidateAddress
		record.CandidateError = item.Edges.ManagementCandidate.FailureCode
	}
	record.PublicKey, err = hex.DecodeString(remote.NodePublicKey)
	if err != nil || len(record.PublicKey) != 32 {
		return serverNodeRecord{}, fmt.Errorf("stored Node identity is invalid")
	}
	return record, nil
}

func (registry *serverNodeRegistry) listRemote(ctx context.Context) ([]serverNodeRecord, error) {
	items, err := serverEntForQueryer(registry.database).Node.Query().Where(node.KindEQ(node.KindRemote)).Order(node.ByCreatedAt(), node.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]serverNodeRecord, 0, len(items))
	for _, item := range items {
		record, err := registry.get(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}
