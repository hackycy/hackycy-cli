package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/node"
	"github.com/hackycy/hackycy-cli/ent/server/nodeportpool"
	"github.com/hackycy/hackycy-cli/ent/server/serverclient"
	"github.com/hackycy/hackycy-cli/ent/server/tunnel"
	"github.com/hackycy/hackycy-cli/ent/server/tunnelhttproute"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

// TrustedTunnelClient is the durable server-side record addressed by a
// recoverable Client Token and owned by one account.
type TrustedTunnelClient struct {
	ID                         string
	OwnerAccountID             string
	NodeID                     string
	PendingNodeID              *string
	PendingSince               *string
	LastAppliedNodeID          *string
	Remark                     string
	Token                      string
	DesiredRevision            int64
	LastAppliedRevision        int64
	DesiredRestartGeneration   int64
	CompletedRestartGeneration int64
	RestartError               *tunnelruntime.StructuredRuntimeError
	RevocationPending          bool
	CreatedAt                  string
	RotatedAt                  *string
}

type ServerControlPlaneEvent struct {
	Type           string
	ClientID       string
	OwnerAccountID string
}

const (
	serverClientCreated = "client_created"
	serverClientUpdated = "client_updated"
	serverClientRotated = "client_rotated"
	serverClientDeleted = "client_deleted"
	serverClientRestart = "client_restart"
)

type ServerControlPlaneOptions struct {
	Database  *sql.DB
	Now       func() time.Time
	Random    io.Reader
	PortRange ServerPortRange
}

// ServerControlPlane owns the durable desired-state transactions. It has no
// HTTP, session, FRP, or command registration responsibilities.
type ServerControlPlane struct {
	database *sql.DB
	now      func() time.Time
	random   io.Reader

	observers      map[uint64]func(ServerControlPlaneEvent)
	observersMu    sync.Mutex
	nextObserverID uint64
}

func NewServerControlPlane(options ServerControlPlaneOptions) (*ServerControlPlane, error) {
	if options.Database == nil {
		return nil, errors.New("Tunnel server database is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	portRange, err := normalizeServerPortRange(options.PortRange)
	if err != nil {
		return nil, err
	}
	tx, err := serverEntForQueryer(options.Database).Tx(context.Background())
	if err != nil {
		return nil, fmt.Errorf("initialize Local Node port pool: %w", err)
	}
	defer tx.Rollback()
	exists, err := tx.NodePortPool.Query().Where(nodeportpool.NodeIDEQ("local")).Exist(context.Background())
	if err != nil {
		return nil, fmt.Errorf("initialize Local Node port pool: %w", err)
	}
	if !exists {
		if _, err := tx.NodePortPool.Create().SetNodeID("local").SetPortStart(int(portRange.Start)).SetPortEnd(int(portRange.End)).Save(context.Background()); err != nil {
			return nil, fmt.Errorf("initialize Local Node port pool: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("initialize Local Node port pool: %w", err)
	}
	return &ServerControlPlane{
		database:  options.Database,
		now:       options.Now,
		random:    options.Random,
		observers: make(map[uint64]func(ServerControlPlaneEvent)),
	}, nil
}

// Subscribe receives domain events only after their database transaction has
// committed. It returns an idempotent unsubscriber.
func (plane *ServerControlPlane) Subscribe(observer func(ServerControlPlaneEvent)) func() {
	if plane == nil || observer == nil {
		return func() {}
	}
	plane.observersMu.Lock()
	id := plane.nextObserverID
	plane.nextObserverID++
	plane.observers[id] = observer
	plane.observersMu.Unlock()
	return func() {
		plane.observersMu.Lock()
		delete(plane.observers, id)
		plane.observersMu.Unlock()
	}
}

func (plane *ServerControlPlane) emit(event ServerControlPlaneEvent) {
	plane.observersMu.Lock()
	observers := make([]func(ServerControlPlaneEvent), 0, len(plane.observers))
	for _, observer := range plane.observers {
		observers = append(observers, observer)
	}
	plane.observersMu.Unlock()
	for _, observer := range observers {
		observer(event)
	}
}

func (plane *ServerControlPlane) CreateClient(ctx context.Context, ownerAccountID, remark string) (TrustedTunnelClient, error) {
	if strings.TrimSpace(ownerAccountID) == "" {
		return TrustedTunnelClient{}, serverDomainError("NOT_FOUND", "Trusted Tunnel Client owner was not found")
	}
	normalizedRemark, err := normalizeClientRemark(remark)
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	id, err := randomUUID(plane.random)
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("generate Trusted Tunnel Client ID: %w", err)
	}
	token, err := randomClientToken(plane.random)
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("generate Trusted Tunnel Client token: %w", err)
	}
	createdAt := formatServerTimestamp(plane.now())
	item, err := serverEntForQueryer(plane.database).ServerClient.Create().SetID(id).
		SetOwnerAccountID(ownerAccountID).SetNodeID("local").SetRemark(normalizedRemark).
		SetToken(token).SetCreatedAt(createdAt).Save(ctx)
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("create Trusted Tunnel Client: %w", err)
	}
	created := trustedClientFromEnt(item)
	plane.emit(ServerControlPlaneEvent{Type: serverClientCreated, ClientID: created.ID, OwnerAccountID: created.OwnerAccountID})
	return created, nil
}

func (plane *ServerControlPlane) ListClients(ctx context.Context) ([]TrustedTunnelClient, error) {
	items, err := serverEntForQueryer(plane.database).ServerClient.Query().Order(serverclient.ByCreatedAt(), serverclient.ByID()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Trusted Tunnel Clients: %w", err)
	}
	return mapTrustedClients(items), nil
}

func (plane *ServerControlPlane) ListClientsForOwner(ctx context.Context, ownerAccountID string) ([]TrustedTunnelClient, error) {
	items, err := serverEntForQueryer(plane.database).ServerClient.Query().Where(serverclient.OwnerAccountIDEQ(ownerAccountID)).Order(serverclient.ByCreatedAt(), serverclient.ByID()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list owner Trusted Tunnel Clients: %w", err)
	}
	return mapTrustedClients(items), nil
}

func (plane *ServerControlPlane) GetClient(ctx context.Context, clientID string) (TrustedTunnelClient, error) {
	return selectClient(ctx, plane.database, clientID)
}

func (plane *ServerControlPlane) GetClientForOwner(ctx context.Context, clientID, ownerAccountID string) (TrustedTunnelClient, error) {
	client, err := selectClientForOwner(ctx, plane.database, clientID, ownerAccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return TrustedTunnelClient{}, serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	return client, err
}

// AssignClientNode commits an online assignment after an external, fresh
// target health check, or records an offline pending target. The targetReady
// flag is intentionally supplied by the caller because management and FRPS
// checks must never run inside the database transaction.
func (plane *ServerControlPlane) AssignClientNode(ctx context.Context, clientID, nodeID string, online, targetReady bool) (TrustedTunnelClient, error) {
	var previousRevision int64
	result, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (TrustedTunnelClient, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return TrustedTunnelClient{}, err
		}
		previousRevision = client.DesiredRevision
		if strings.TrimSpace(nodeID) == "" {
			return TrustedTunnelClient{}, serverDomainError("INVALID_NODE", "Client Node is required")
		}
		target, err := serverEntOnConnection(connection).Node.Query().Where(node.IDEQ(nodeID)).Only(ctx)
		if serverent.IsNotFound(err) {
			return TrustedTunnelClient{}, serverDomainError("NOT_FOUND", "Target Node was not found")
		}
		if err != nil {
			return TrustedTunnelClient{}, fmt.Errorf("read target Node: %w", err)
		}
		if target.Lifecycle != node.LifecycleActive {
			return TrustedTunnelClient{}, serverDomainError("NODE_REMOVE_PENDING", "Target Node is pending removal")
		}
		if client.NodeID == nodeID {
			if client.PendingNodeID == nil {
				return client, nil
			}
			if _, err := serverEntOnConnection(connection).ServerClient.UpdateOneID(clientID).ClearPendingNodeID().ClearPendingSince().Save(ctx); err != nil {
				return TrustedTunnelClient{}, fmt.Errorf("clear completed Client Node assignment: %w", err)
			}
			return selectClient(ctx, connection, clientID)
		}
		if !online {
			timestamp := formatServerTimestamp(plane.now())
			if _, err := serverEntOnConnection(connection).ServerClient.UpdateOneID(clientID).SetPendingNodeID(nodeID).SetPendingSince(timestamp).Save(ctx); err != nil {
				return TrustedTunnelClient{}, fmt.Errorf("save pending Client Node assignment: %w", err)
			}
			return selectClient(ctx, connection, clientID)
		}
		if !targetReady {
			return TrustedTunnelClient{}, serverDomainError("NODE_TARGET_UNAVAILABLE", "Target Node management and FRPS observation are not fresh and running")
		}
		if err := migrateClientNode(ctx, connection, client, nodeID); err != nil {
			return TrustedTunnelClient{}, err
		}
		return selectClient(ctx, connection, clientID)
	})
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: result.ID, OwnerAccountID: result.OwnerAccountID})
	if result.DesiredRevision != previousRevision {
		plane.emit(ServerControlPlaneEvent{Type: serverDesiredState, ClientID: result.ID, OwnerAccountID: result.OwnerAccountID})
	}
	return result, nil
}

// CancelPendingClientNode removes only the pending target and leaves the
// current assignment, resources, and applied runtime untouched.
func (plane *ServerControlPlane) CancelPendingClientNode(ctx context.Context, clientID string) (TrustedTunnelClient, error) {
	result, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (TrustedTunnelClient, error) {
		if _, err := selectClient(ctx, connection, clientID); err != nil {
			return TrustedTunnelClient{}, err
		}
		if _, err := serverEntOnConnection(connection).ServerClient.UpdateOneID(clientID).ClearPendingNodeID().ClearPendingSince().Save(ctx); err != nil {
			return TrustedTunnelClient{}, fmt.Errorf("cancel pending Client Node assignment: %w", err)
		}
		return selectClient(ctx, connection, clientID)
	})
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: result.ID, OwnerAccountID: result.OwnerAccountID})
	return result, nil
}

func migrateClientNode(ctx context.Context, connection *sql.Conn, client TrustedTunnelClient, targetNodeID string) error {
	entClient := serverEntOnConnection(connection)
	pool, err := nodePortPool(ctx, connection, targetNodeID)
	if err != nil {
		return serverDomainError("NODE_TARGET_UNAVAILABLE", "Target Node has no saved port pool")
	}
	clientTunnels, err := entClient.Tunnel.Query().Where(tunnel.ClientInternalIDEQ(client.ID)).All(ctx)
	if err != nil {
		return fmt.Errorf("read Client Tunnel resources for Node assignment: %w", err)
	}
	for _, resource := range clientTunnels {
		if resource.Protocol != tunnel.ProtocolTCP && resource.Protocol != tunnel.ProtocolUDP {
			continue
		}
		if resource.ServerPort == nil || int64(*resource.ServerPort) < pool.Start || int64(*resource.ServerPort) > pool.End {
			return serverDomainError("NODE_RESOURCE_CONFLICT", fmt.Sprintf("Tunnel %s uses a port outside the target Node pool %d-%d", resource.ID, pool.Start, pool.End))
		}
		occupied, err := entClient.Tunnel.Query().Where(
			tunnel.NodeIDEQ(targetNodeID), tunnel.ProtocolEQ(resource.Protocol),
			tunnel.ServerPortEQ(*resource.ServerPort), tunnel.ClientInternalIDNEQ(client.ID),
		).Exist(ctx)
		if err != nil {
			return err
		}
		if occupied {
			return serverDomainError("NODE_RESOURCE_CONFLICT", "Target Node already owns a conflicting Tunnel resource")
		}
	}
	hostnames, err := clientHostnamesOnConnection(ctx, connection, client.ID)
	if err != nil {
		return err
	}
	for _, hostname := range hostnames {
		routes, err := serverEntOnConnection(connection).TunnelHTTPRoute.Query().Where(tunnelhttproute.HostnameEQ(hostname)).WithTunnel().All(ctx)
		if err != nil {
			return fmt.Errorf("read hostname ownership for Node assignment: %w", err)
		}
		for _, route := range routes {
			owner := route.Edges.Tunnel
			if owner == nil {
				return fmt.Errorf("HTTP hostname %s has no Tunnel", hostname)
			}
			if owner.ClientInternalID != client.ID && owner.NodeID != targetNodeID {
				return serverDomainError("NODE_RESOURCE_CONFLICT", fmt.Sprintf("HTTP hostname %s has routes remaining on the current Node", hostname))
			}
		}
	}
	if _, err := entClient.ServerClient.UpdateOneID(client.ID).SetNodeID(targetNodeID).ClearPendingNodeID().ClearPendingSince().AddDesiredRevision(1).Save(ctx); err != nil {
		return fmt.Errorf("update Client Node assignment: %w", err)
	}
	if _, err := entClient.Tunnel.Update().Where(tunnel.ClientInternalIDEQ(client.ID)).SetNodeID(targetNodeID).Save(ctx); err != nil {
		return err
	}
	inconsistent, err := entClient.Tunnel.Query().Where(tunnel.ClientInternalIDEQ(client.ID), tunnel.NodeIDNEQ(targetNodeID)).Exist(ctx)
	if err != nil {
		return err
	}
	if inconsistent {
		return fmt.Errorf("Client Tunnel Node assignment is inconsistent")
	}
	return nil
}

func clientHostnamesOnConnection(ctx context.Context, connection *sql.Conn, clientID string) ([]string, error) {
	routes, err := serverEntOnConnection(connection).TunnelHTTPRoute.Query().Where(tunnelhttproute.HasTunnelWith(tunnel.ClientInternalIDEQ(clientID))).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Client hostname resources for Node assignment: %w", err)
	}
	unique := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		unique[route.Hostname] = struct{}{}
	}
	hostnames := make([]string, 0, len(unique))
	for hostname := range unique {
		hostnames = append(hostnames, hostname)
	}
	sort.Strings(hostnames)
	return hostnames, nil
}

func (plane *ServerControlPlane) FindClientByToken(ctx context.Context, token string) (*TrustedTunnelClient, error) {
	client, err := selectClientByToken(ctx, plane.database, token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &client, nil
}

func (plane *ServerControlPlane) UpdateClientRemark(ctx context.Context, clientID, remark string) (TrustedTunnelClient, error) {
	normalizedRemark, err := normalizeClientRemark(remark)
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	if _, err := selectClient(ctx, plane.database, clientID); err != nil {
		return TrustedTunnelClient{}, err
	}
	item, err := serverEntForQueryer(plane.database).ServerClient.UpdateOneID(clientID).SetRemark(normalizedRemark).Save(ctx)
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("update Trusted Tunnel Client remark: %w", err)
	}
	updated := trustedClientFromEnt(item)
	plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: updated.ID, OwnerAccountID: updated.OwnerAccountID})
	return updated, nil
}

func (plane *ServerControlPlane) RotateClientToken(ctx context.Context, clientID string) (TrustedTunnelClient, error) {
	token, err := randomClientToken(plane.random)
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("generate replacement Trusted Tunnel Client token: %w", err)
	}
	rotatedAt := formatServerTimestamp(plane.now())
	tx, err := serverEntForQueryer(plane.database).Tx(ctx)
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	defer tx.Rollback()
	current, err := tx.ServerClient.Get(ctx, clientID)
	if serverent.IsNotFound(err) {
		return TrustedTunnelClient{}, serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	item, err := tx.ServerClient.UpdateOne(current).SetToken(token).SetRevocationPending(true).SetRotatedAt(rotatedAt).Save(ctx)
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("rotate Trusted Tunnel Client token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TrustedTunnelClient{}, err
	}
	rotated := trustedClientFromEnt(item)
	plane.emit(ServerControlPlaneEvent{Type: serverClientRotated, ClientID: rotated.ID, OwnerAccountID: rotated.OwnerAccountID})
	return rotated, nil
}

func (plane *ServerControlPlane) AcknowledgeReplacementToken(ctx context.Context, clientID string) error {
	tx, err := serverEntForQueryer(plane.database).Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := tx.ServerClient.Get(ctx, clientID)
	if serverent.IsNotFound(err) {
		return serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	if err != nil {
		return err
	}
	if !current.RevocationPending {
		return nil
	}
	if _, err := tx.ServerClient.UpdateOne(current).SetRevocationPending(false).Save(ctx); err != nil {
		return fmt.Errorf("acknowledge Trusted Tunnel Client token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: current.ID, OwnerAccountID: current.OwnerAccountID})
	return nil
}

func (plane *ServerControlPlane) DeleteClient(ctx context.Context, clientID string) error {
	deleted, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (TrustedTunnelClient, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return TrustedTunnelClient{}, err
		}
		if err := serverEntOnConnection(connection).ServerClient.DeleteOneID(clientID).Exec(ctx); err != nil {
			return TrustedTunnelClient{}, fmt.Errorf("delete Trusted Tunnel Client: %w", err)
		}
		return client, nil
	})
	if err != nil {
		return err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientDeleted, ClientID: deleted.ID, OwnerAccountID: deleted.OwnerAccountID})
	return nil
}

// RequestClientRestart durably coalesces restart requests into a monotonically
// increasing generation. Delivery to an online agent happens after commit.
func (plane *ServerControlPlane) RequestClientRestart(ctx context.Context, clientID string) (TrustedTunnelClient, error) {
	tx, err := serverEntForQueryer(plane.database).Tx(ctx)
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	defer tx.Rollback()
	current, err := tx.ServerClient.Get(ctx, clientID)
	if serverent.IsNotFound(err) {
		return TrustedTunnelClient{}, serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	if current.DesiredRestartGeneration >= serverMaximumSafeInteger {
		return TrustedTunnelClient{}, serverDomainError("RESTART_GENERATION_EXHAUSTED", "Restart generation cannot be advanced")
	}
	item, err := tx.ServerClient.UpdateOne(current).AddDesiredRestartGeneration(1).Save(ctx)
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("request Trusted Tunnel Client restart: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TrustedTunnelClient{}, err
	}
	updated := trustedClientFromEnt(item)
	plane.emit(ServerControlPlaneEvent{Type: serverClientRestart, ClientID: updated.ID, OwnerAccountID: updated.OwnerAccountID})
	return updated, nil
}

// RecordRestartResult advances completion at most to a desired generation.
// Already completed generations are accepted so reconnect hello replay is
// idempotent and can never cause the client to execute a restart twice.
func (plane *ServerControlPlane) RecordRestartResult(ctx context.Context, clientID string, result tunnelruntime.RestartResult) error {
	if result.Generation < 1 || result.Generation > serverMaximumSafeInteger {
		return serverDomainError("INVALID_RESTART_GENERATION", "Restart generation is invalid")
	}
	tx, err := serverEntForQueryer(plane.database).Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := tx.ServerClient.Get(ctx, clientID)
	if serverent.IsNotFound(err) {
		return serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	if err != nil {
		return err
	}
	if result.Generation > current.DesiredRestartGeneration {
		return serverDomainError("INVALID_RESTART_GENERATION", "Restart generation cannot exceed Desired Restart Generation")
	}
	if result.Generation <= current.CompletedRestartGeneration {
		return nil
	}
	update := tx.ServerClient.UpdateOne(current).SetCompletedRestartGeneration(result.Generation)
	if result.Success {
		update.ClearRestartErrorGeneration().ClearRestartErrorCode().ClearRestartErrorMessage()
	} else {
		runtimeError := result.Error
		if runtimeError == nil {
			runtimeError = &tunnelruntime.StructuredRuntimeError{Code: "RESTART_FAILED", Message: "Client could not restart frpc"}
		}
		code, message := strings.TrimSpace(runtimeError.Code), strings.TrimSpace(runtimeError.Message)
		if code == "" || message == "" {
			return serverDomainError("INVALID_RESTART_RESULT", "Failed restart result must include an error")
		}
		update.SetRestartErrorGeneration(result.Generation).SetRestartErrorCode(code).SetRestartErrorMessage(message)
	}
	if _, err := update.Save(ctx); err != nil {
		return fmt.Errorf("record Trusted Tunnel Client restart result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: current.ID, OwnerAccountID: current.OwnerAccountID})
	return nil
}

type clientQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func selectClient(ctx context.Context, queryer clientQueryer, clientID string) (TrustedTunnelClient, error) {
	item, err := serverEntForQueryer(queryer).ServerClient.Get(ctx, clientID)
	if serverent.IsNotFound(err) {
		return TrustedTunnelClient{}, serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("read Trusted Tunnel Client: %w", err)
	}
	return trustedClientFromEnt(item), nil
}

func selectClientForOwner(ctx context.Context, queryer clientQueryer, clientID, ownerAccountID string) (TrustedTunnelClient, error) {
	item, err := serverEntForQueryer(queryer).ServerClient.Query().Where(serverclient.IDEQ(clientID), serverclient.OwnerAccountIDEQ(ownerAccountID)).Only(ctx)
	if serverent.IsNotFound(err) {
		return TrustedTunnelClient{}, sql.ErrNoRows
	}
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	return trustedClientFromEnt(item), nil
}

func selectClientByToken(ctx context.Context, queryer clientQueryer, token string) (TrustedTunnelClient, error) {
	item, err := serverEntForQueryer(queryer).ServerClient.Query().Where(serverclient.TokenEQ(token)).Only(ctx)
	if serverent.IsNotFound(err) {
		return TrustedTunnelClient{}, sql.ErrNoRows
	}
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	return trustedClientFromEnt(item), nil
}

func mapTrustedClients(items []*serverent.ServerClient) []TrustedTunnelClient {
	clients := make([]TrustedTunnelClient, 0, len(items))
	for _, item := range items {
		clients = append(clients, trustedClientFromEnt(item))
	}
	return clients
}

func trustedClientFromEnt(item *serverent.ServerClient) TrustedTunnelClient {
	client := TrustedTunnelClient{
		ID: item.ID, OwnerAccountID: item.OwnerAccountID, NodeID: item.NodeID,
		PendingNodeID: item.PendingNodeID, PendingSince: item.PendingSince,
		LastAppliedNodeID: item.LastAppliedNodeID, Remark: item.Remark, Token: item.Token,
		DesiredRevision: item.DesiredRevision, LastAppliedRevision: item.LastAppliedRevision,
		DesiredRestartGeneration:   item.DesiredRestartGeneration,
		CompletedRestartGeneration: item.CompletedRestartGeneration,
		RevocationPending:          item.RevocationPending, CreatedAt: item.CreatedAt, RotatedAt: item.RotatedAt,
	}
	if item.RestartErrorGeneration != nil && *item.RestartErrorGeneration == item.CompletedRestartGeneration && item.RestartErrorCode != nil && item.RestartErrorMessage != nil {
		client.RestartError = &tunnelruntime.StructuredRuntimeError{Code: *item.RestartErrorCode, Message: *item.RestartErrorMessage}
	}
	return client
}

func withImmediateTransaction[T any](ctx context.Context, database *sql.DB, action func(*sql.Conn) (T, error)) (T, error) {
	var zero T
	connection, err := database.Conn(ctx)
	if err != nil {
		return zero, fmt.Errorf("acquire Tunnel database connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return zero, fmt.Errorf("begin Tunnel database transaction: %w", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	result, err := action(connection)
	if err != nil {
		return zero, err
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return zero, fmt.Errorf("commit Tunnel database transaction: %w", err)
	}
	rollback = false
	return result, nil
}

func randomClientToken(random io.Reader) (string, error) {
	bytes := make([]byte, 32)
	if _, err := io.ReadFull(random, bytes); err != nil {
		return "", err
	}
	return "ycy_" + base64.RawURLEncoding.EncodeToString(bytes), nil
}

func randomUUID(random io.Reader) (string, error) {
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(random, bytes); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return strings.Join([]string{
		hex.EncodeToString(bytes[0:4]),
		hex.EncodeToString(bytes[4:6]),
		hex.EncodeToString(bytes[6:8]),
		hex.EncodeToString(bytes[8:10]),
		hex.EncodeToString(bytes[10:16]),
	}, "-"), nil
}

func formatServerTimestamp(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}
