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

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

// TrustedTunnelClient is the durable server-side record addressed by a
// recoverable Client Token and owned by one account.
type TrustedTunnelClient struct {
	ID                         string
	OwnerAccountID             string
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

const serverClientColumns = `internal_id, owner_account_id, remark, token, desired_revision, last_applied_revision,
	desired_restart_generation, completed_restart_generation, restart_error_generation, restart_error_code, restart_error_message,
	revocation_pending, created_at, rotated_at`

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
	if _, err := options.Database.Exec(`
		INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('local', ?, ?)
		ON CONFLICT(node_id) DO NOTHING
	`, portRange.Start, portRange.End); err != nil {
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
	rows, err := plane.database.QueryContext(ctx, `SELECT `+serverClientColumns+` FROM clients ORDER BY created_at, internal_id`)
	if err != nil {
		return nil, fmt.Errorf("list Trusted Tunnel Clients: %w", err)
	}
	defer rows.Close()
	return collectClients(rows)
}

func (plane *ServerControlPlane) ListClientsForOwner(ctx context.Context, ownerAccountID string) ([]TrustedTunnelClient, error) {
	rows, err := plane.database.QueryContext(ctx, `SELECT `+serverClientColumns+` FROM clients WHERE owner_account_id = ? ORDER BY created_at, internal_id`, ownerAccountID)
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

// RequestClientRestart durably coalesces restart requests into a monotonically
// increasing generation. Delivery to an online agent happens after commit.
func (plane *ServerControlPlane) RequestClientRestart(ctx context.Context, clientID string) (TrustedTunnelClient, error) {
	updated, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (TrustedTunnelClient, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return TrustedTunnelClient{}, err
		}
		if client.DesiredRestartGeneration >= serverMaximumSafeInteger {
			return TrustedTunnelClient{}, serverDomainError("RESTART_GENERATION_EXHAUSTED", "Restart generation cannot be advanced")
		}
		if _, err := connection.ExecContext(ctx, `UPDATE clients SET desired_restart_generation = desired_restart_generation + 1 WHERE internal_id = ?`, clientID); err != nil {
			return TrustedTunnelClient{}, fmt.Errorf("request Trusted Tunnel Client restart: %w", err)
		}
		return selectClient(ctx, connection, clientID)
	})
	if err != nil {
		return TrustedTunnelClient{}, err
	}
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
	type recordedResult struct {
		client  TrustedTunnelClient
		changed bool
	}
	recorded, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (recordedResult, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return recordedResult{}, err
		}
		if result.Generation > client.DesiredRestartGeneration {
			return recordedResult{}, serverDomainError("INVALID_RESTART_GENERATION", "Restart generation cannot exceed Desired Restart Generation")
		}
		if result.Generation <= client.CompletedRestartGeneration {
			return recordedResult{client: client}, nil
		}
		var errorGeneration any
		var errorCode any
		var errorMessage any
		if !result.Success {
			runtimeError := result.Error
			if runtimeError == nil {
				runtimeError = &tunnelruntime.StructuredRuntimeError{Code: "RESTART_FAILED", Message: "Client could not restart frpc"}
			}
			errorGeneration = result.Generation
			errorCode = strings.TrimSpace(runtimeError.Code)
			errorMessage = strings.TrimSpace(runtimeError.Message)
			if errorCode == "" || errorMessage == "" {
				return recordedResult{}, serverDomainError("INVALID_RESTART_RESULT", "Failed restart result must include an error")
			}
		}
		if _, err := connection.ExecContext(ctx, `UPDATE clients SET completed_restart_generation = ?, restart_error_generation = ?, restart_error_code = ?, restart_error_message = ? WHERE internal_id = ?`, result.Generation, errorGeneration, errorCode, errorMessage, clientID); err != nil {
			return recordedResult{}, fmt.Errorf("record Trusted Tunnel Client restart result: %w", err)
		}
		updated, err := selectClient(ctx, connection, clientID)
		return recordedResult{client: updated, changed: err == nil}, err
	})
	if err != nil {
		return err
	}
	if recorded.changed {
		plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: recorded.client.ID, OwnerAccountID: recorded.client.OwnerAccountID})
	}
	return nil
}

type clientQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func selectClient(ctx context.Context, queryer clientQueryer, clientID string) (TrustedTunnelClient, error) {
	client, err := scanClient(queryer.QueryRowContext(ctx, `SELECT `+serverClientColumns+` FROM clients WHERE internal_id = ?`, clientID))
	if errors.Is(err, sql.ErrNoRows) {
		return TrustedTunnelClient{}, serverDomainError("NOT_FOUND", "Trusted Tunnel Client was not found")
	}
	if err != nil {
		return TrustedTunnelClient{}, fmt.Errorf("read Trusted Tunnel Client: %w", err)
	}
	return client, nil
}

func selectClientForOwner(ctx context.Context, queryer clientQueryer, clientID, ownerAccountID string) (TrustedTunnelClient, error) {
	client, err := scanClient(queryer.QueryRowContext(ctx, `SELECT `+serverClientColumns+` FROM clients WHERE internal_id = ? AND owner_account_id = ?`, clientID, ownerAccountID))
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	return client, nil
}

func selectClientByToken(ctx context.Context, queryer clientQueryer, token string) (TrustedTunnelClient, error) {
	client, err := scanClient(queryer.QueryRowContext(ctx, `SELECT `+serverClientColumns+` FROM clients WHERE token = ?`, token))
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
	var restartErrorGeneration sql.NullInt64
	var restartErrorCode, restartErrorMessage sql.NullString
	if err := scanner.Scan(&client.ID, &client.OwnerAccountID, &client.Remark, &client.Token, &client.DesiredRevision, &client.LastAppliedRevision,
		&client.DesiredRestartGeneration, &client.CompletedRestartGeneration, &restartErrorGeneration, &restartErrorCode, &restartErrorMessage,
		&revocationPending, &client.CreatedAt, &rotatedAt); err != nil {
		return TrustedTunnelClient{}, err
	}
	if restartErrorGeneration.Valid && restartErrorGeneration.Int64 == client.CompletedRestartGeneration && restartErrorCode.Valid && restartErrorMessage.Valid {
		client.RestartError = &tunnelruntime.StructuredRuntimeError{Code: restartErrorCode.String, Message: restartErrorMessage.String}
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
