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
	"strings"
	"sync"
	"time"
)

// TrustedTunnelClient is the durable server-side record addressed by a
// recoverable Client Token and owned by one account.
type TrustedTunnelClient struct {
	ID                  string
	OwnerAccountID      string
	Remark              string
	Token               string
	DesiredRevision     int64
	LastAppliedRevision int64
	RevocationPending   bool
	CreatedAt           string
	RotatedAt           *string
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
	database  *sql.DB
	now       func() time.Time
	random    io.Reader
	portRange ServerPortRange

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
	return &ServerControlPlane{
		database:  options.Database,
		now:       options.Now,
		random:    options.Random,
		portRange: portRange,
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
	created, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (TrustedTunnelClient, error) {
		if _, err := connection.ExecContext(ctx, `
			INSERT INTO clients(internal_id, owner_account_id, remark, token, created_at)
			VALUES(?, ?, ?, ?, ?)
		`, id, ownerAccountID, normalizedRemark, token, createdAt); err != nil {
			return TrustedTunnelClient{}, fmt.Errorf("create Trusted Tunnel Client: %w", err)
		}
		return selectClient(ctx, connection, id)
	})
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientCreated, ClientID: created.ID, OwnerAccountID: created.OwnerAccountID})
	return created, nil
}

func (plane *ServerControlPlane) ListClients(ctx context.Context) ([]TrustedTunnelClient, error) {
	rows, err := plane.database.QueryContext(ctx, `SELECT internal_id, owner_account_id, remark, token, desired_revision, last_applied_revision, revocation_pending, created_at, rotated_at FROM clients ORDER BY created_at, internal_id`)
	if err != nil {
		return nil, fmt.Errorf("list Trusted Tunnel Clients: %w", err)
	}
	defer rows.Close()
	return collectClients(rows)
}

func (plane *ServerControlPlane) ListClientsForOwner(ctx context.Context, ownerAccountID string) ([]TrustedTunnelClient, error) {
	rows, err := plane.database.QueryContext(ctx, `SELECT internal_id, owner_account_id, remark, token, desired_revision, last_applied_revision, revocation_pending, created_at, rotated_at FROM clients WHERE owner_account_id = ? ORDER BY created_at, internal_id`, ownerAccountID)
	if err != nil {
		return nil, fmt.Errorf("list owner Trusted Tunnel Clients: %w", err)
	}
	defer rows.Close()
	return collectClients(rows)
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
	updated, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (TrustedTunnelClient, error) {
		if _, err := selectClient(ctx, connection, clientID); err != nil {
			return TrustedTunnelClient{}, err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE clients SET remark = ? WHERE internal_id = ?`, normalizedRemark, clientID); err != nil {
			return TrustedTunnelClient{}, fmt.Errorf("update Trusted Tunnel Client remark: %w", err)
		}
		return selectClient(ctx, connection, clientID)
	})
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: updated.ID, OwnerAccountID: updated.OwnerAccountID})
	return updated, nil
}

func (plane *ServerControlPlane) RotateClientToken(ctx context.Context, clientID string) (TrustedTunnelClient, error) {
	token, err := randomClientToken(plane.random)
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("generate replacement Trusted Tunnel Client token: %w", err)
	}
	rotatedAt := formatServerTimestamp(plane.now())
	rotated, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (TrustedTunnelClient, error) {
		if _, err := selectClient(ctx, connection, clientID); err != nil {
			return TrustedTunnelClient{}, err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE clients SET token = ?, revocation_pending = 1, rotated_at = ? WHERE internal_id = ?`, token, rotatedAt, clientID); err != nil {
			return TrustedTunnelClient{}, fmt.Errorf("rotate Trusted Tunnel Client token: %w", err)
		}
		return selectClient(ctx, connection, clientID)
	})
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientRotated, ClientID: rotated.ID, OwnerAccountID: rotated.OwnerAccountID})
	return rotated, nil
}

func (plane *ServerControlPlane) AcknowledgeReplacementToken(ctx context.Context, clientID string) error {
	type acknowledgement struct {
		client  TrustedTunnelClient
		changed bool
	}
	result, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (acknowledgement, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return acknowledgement{}, err
		}
		if !client.RevocationPending {
			return acknowledgement{client: client}, nil
		}
		if _, err := connection.ExecContext(ctx, `UPDATE clients SET revocation_pending = 0 WHERE internal_id = ?`, clientID); err != nil {
			return acknowledgement{}, fmt.Errorf("acknowledge Trusted Tunnel Client token: %w", err)
		}
		updated, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return acknowledgement{}, err
		}
		return acknowledgement{client: updated, changed: true}, nil
	})
	if err != nil {
		return err
	}
	if !result.changed {
		return nil
	}
	plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: result.client.ID, OwnerAccountID: result.client.OwnerAccountID})
	return nil
}

func (plane *ServerControlPlane) DeleteClient(ctx context.Context, clientID string) error {
	deleted, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (TrustedTunnelClient, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return TrustedTunnelClient{}, err
		}
		if _, err := connection.ExecContext(ctx, `DELETE FROM clients WHERE internal_id = ?`, clientID); err != nil {
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

type clientQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func selectClient(ctx context.Context, queryer clientQueryer, clientID string) (TrustedTunnelClient, error) {
	client, err := scanClient(queryer.QueryRowContext(ctx, `SELECT internal_id, owner_account_id, remark, token, desired_revision, last_applied_revision, revocation_pending, created_at, rotated_at FROM clients WHERE internal_id = ?`, clientID))
	if errors.Is(err, sql.ErrNoRows) {
		return TrustedTunnelClient{}, serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("read Trusted Tunnel Client: %w", err)
	}
	return client, nil
}

func selectClientForOwner(ctx context.Context, queryer clientQueryer, clientID, ownerAccountID string) (TrustedTunnelClient, error) {
	client, err := scanClient(queryer.QueryRowContext(ctx, `SELECT internal_id, owner_account_id, remark, token, desired_revision, last_applied_revision, revocation_pending, created_at, rotated_at FROM clients WHERE internal_id = ? AND owner_account_id = ?`, clientID, ownerAccountID))
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	return client, nil
}

func selectClientByToken(ctx context.Context, queryer clientQueryer, token string) (TrustedTunnelClient, error) {
	client, err := scanClient(queryer.QueryRowContext(ctx, `SELECT internal_id, owner_account_id, remark, token, desired_revision, last_applied_revision, revocation_pending, created_at, rotated_at FROM clients WHERE token = ?`, token))
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	return client, nil
}

func collectClients(rows *sql.Rows) ([]TrustedTunnelClient, error) {
	clients := make([]TrustedTunnelClient, 0)
	for rows.Next() {
		client, err := scanClient(rows)
		if err != nil {
			return nil, fmt.Errorf("read Trusted Tunnel Client: %w", err)
		}
		clients = append(clients, client)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Trusted Tunnel Clients: %w", err)
	}
	return clients, nil
}

type clientScanner interface {
	Scan(...any) error
}

func scanClient(scanner clientScanner) (TrustedTunnelClient, error) {
	var client TrustedTunnelClient
	var revocationPending int
	var rotatedAt sql.NullString
	if err := scanner.Scan(&client.ID, &client.OwnerAccountID, &client.Remark, &client.Token, &client.DesiredRevision, &client.LastAppliedRevision, &revocationPending, &client.CreatedAt, &rotatedAt); err != nil {
		return TrustedTunnelClient{}, err
	}
	client.RevocationPending = revocationPending == 1
	if rotatedAt.Valid {
		client.RotatedAt = &rotatedAt.String
	}
	return client, nil
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
