package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const environmentAdministratorID = "environment-admin"

const (
	argon2Memory      = 65_536
	argon2Time        = 3
	argon2Parallelism = 1
	argon2KeyLength   = 32
	argon2SaltLength  = 16
)

type AccountKind string
type AccountRole string

const (
	AccountKindEnvironment AccountKind = "environment"
	AccountKindLocal       AccountKind = "local"
	AccountRoleAdmin       AccountRole = "admin"
	AccountRoleUser        AccountRole = "user"
)

type ServerAccount struct {
	ID        string
	Kind      AccountKind
	Username  string
	Role      AccountRole
	CreatedAt string
	UpdatedAt string
}

type ServerAccountView struct {
	ServerAccount
	ManagedByEnvironment bool
	ClientCount          int64
}

type ServerAccountsOptions struct {
	Database      *sql.DB
	AdminUsername string
	AdminPassword string
	Now           func() time.Time
	Random        io.Reader
}

// ServerAccounts owns durable account identity and password records. Session
// issuance and HTTP authorization are deliberately separate callers.
type ServerAccounts struct {
	database            *sql.DB
	now                 func() time.Time
	random              io.Reader
	environmentPassword string
	environmentHash     string
}

var accountUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func NewServerAccounts(ctx context.Context, options ServerAccountsOptions) (*ServerAccounts, error) {
	if options.Database == nil {
		return nil, fmt.Errorf("Tunnel server account database is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	username, usernameKey, err := normalizeAccountUsername(options.AdminUsername)
	if err != nil {
		return nil, err
	}
	if err := validateAccountPassword(options.AdminPassword); err != nil {
		return nil, err
	}
	environmentHash, err := hashAccountPassword(options.AdminPassword, options.Random)
	if err != nil {
		return nil, fmt.Errorf("hash environment administrator password: %w", err)
	}
	accounts := &ServerAccounts{
		database:            options.Database,
		now:                 options.Now,
		random:              options.Random,
		environmentPassword: options.AdminPassword,
		environmentHash:     environmentHash,
	}
	if err := accounts.initializeEnvironmentAdministrator(ctx, username, usernameKey); err != nil {
		return nil, err
	}
	return accounts, nil
}

func (accounts *ServerAccounts) initializeEnvironmentAdministrator(ctx context.Context, username, usernameKey string) error {
	timestamp := formatServerTimestamp(accounts.now())
	_, err := withImmediateTransaction(ctx, accounts.database, func(connection *sql.Conn) (struct{}, error) {
		if _, err := connection.ExecContext(ctx, `
			INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at)
			VALUES(?, 'environment', ?, ?, 'admin', NULL, ?, ?)
			ON CONFLICT(internal_id) DO UPDATE SET
				username = excluded.username,
				username_key = excluded.username_key,
				role = 'admin',
				password_hash = NULL,
				updated_at = excluded.updated_at
		`, environmentAdministratorID, username, usernameKey, timestamp, timestamp); err != nil {
			return struct{}{}, mapEnvironmentAccountError(err)
		}
		return struct{}{}, nil
	})
	return err
}

func (accounts *ServerAccounts) GetAccount(ctx context.Context, accountID string) (ServerAccount, error) {
	return selectAccount(ctx, accounts.database, accountID)
}

func (accounts *ServerAccounts) GetAccountByUsername(ctx context.Context, username string) (ServerAccount, error) {
	_, usernameKey, err := normalizeAccountUsername(username)
	if err != nil {
		return ServerAccount{}, err
	}
	account, err := selectAccountByUsername(ctx, accounts.database, usernameKey)
	if err == sql.ErrNoRows {
		return ServerAccount{}, serverDomainError("AUTHENTICATION_REQUIRED", "Authenticated session is required")
	}
	return account, err
}

func (accounts *ServerAccounts) ListAccounts(ctx context.Context) ([]ServerAccountView, error) {
	rows, err := accounts.database.QueryContext(ctx, `
		SELECT accounts.internal_id, accounts.kind, accounts.username, accounts.role, accounts.created_at, accounts.updated_at, count(clients.internal_id)
		FROM accounts LEFT JOIN clients ON clients.owner_account_id = accounts.internal_id
		GROUP BY accounts.internal_id, accounts.kind, accounts.username, accounts.role, accounts.created_at, accounts.updated_at, accounts.username_key
		ORDER BY accounts.kind, accounts.username_key, accounts.internal_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list Tunnel server accounts: %w", err)
	}
	defer rows.Close()
	views := make([]ServerAccountView, 0)
	for rows.Next() {
		var view ServerAccountView
		if err := rows.Scan(&view.ID, &view.Kind, &view.Username, &view.Role, &view.CreatedAt, &view.UpdatedAt, &view.ClientCount); err != nil {
			return nil, fmt.Errorf("read Tunnel server account: %w", err)
		}
		view.ManagedByEnvironment = view.Kind == AccountKindEnvironment
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Tunnel server accounts: %w", err)
	}
	return views, nil
}

func (accounts *ServerAccounts) CreateLocalAccount(ctx context.Context, username, password string, role AccountRole) (ServerAccount, error) {
	username, usernameKey, err := normalizeAccountUsername(username)
	if err != nil {
		return ServerAccount{}, err
	}
	if err := validateAccountPassword(password); err != nil {
		return ServerAccount{}, err
	}
	if role == "" {
		role = AccountRoleUser
	}
	if role != AccountRoleAdmin && role != AccountRoleUser {
		return ServerAccount{}, serverDomainError("INVALID_ACCOUNT", "Account role must be admin or user")
	}
	passwordHash, err := hashAccountPassword(password, accounts.random)
	if err != nil {
		return ServerAccount{}, fmt.Errorf("hash local account password: %w", err)
	}
	accountID, err := randomUUID(accounts.random)
	if err != nil {
		return ServerAccount{}, fmt.Errorf("generate local account ID: %w", err)
	}
	timestamp := formatServerTimestamp(accounts.now())
	return withImmediateTransaction(ctx, accounts.database, func(connection *sql.Conn) (ServerAccount, error) {
		if _, err := connection.ExecContext(ctx, `
			INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at)
			VALUES(?, 'local', ?, ?, ?, ?, ?, ?)
		`, accountID, username, usernameKey, role, passwordHash, timestamp, timestamp); err != nil {
			return ServerAccount{}, mapLocalAccountError(err)
		}
		return selectAccount(ctx, connection, accountID)
	})
}

func (accounts *ServerAccounts) ChangeLocalAccountRole(ctx context.Context, accountID string, role AccountRole) (ServerAccount, bool, error) {
	if role != AccountRoleAdmin && role != AccountRoleUser {
		return ServerAccount{}, false, serverDomainError("INVALID_ACCOUNT", "Account role must be admin or user")
	}
	timestamp := formatServerTimestamp(accounts.now())
	result, err := withImmediateTransaction(ctx, accounts.database, func(connection *sql.Conn) (struct {
		account ServerAccount
		changed bool
	}, error) {
		account, err := selectLocalAccount(ctx, connection, accountID)
		if err != nil {
			return struct {
				account ServerAccount
				changed bool
			}{}, err
		}
		if account.Role == role {
			return struct {
				account ServerAccount
				changed bool
			}{account: account}, nil
		}
		if _, err := connection.ExecContext(ctx, `UPDATE accounts SET role = ?, updated_at = ? WHERE internal_id = ?`, role, timestamp, accountID); err != nil {
			return struct {
				account ServerAccount
				changed bool
			}{}, fmt.Errorf("change local account role: %w", err)
		}
		updated, err := selectAccount(ctx, connection, accountID)
		if err != nil {
			return struct {
				account ServerAccount
				changed bool
			}{}, err
		}
		return struct {
			account ServerAccount
			changed bool
		}{account: updated, changed: true}, nil
	})
	if err != nil {
		return ServerAccount{}, false, err
	}
	return result.account, result.changed, nil
}

func (accounts *ServerAccounts) ResetLocalAccountPassword(ctx context.Context, accountID, password string) (ServerAccount, error) {
	if err := validateAccountPassword(password); err != nil {
		return ServerAccount{}, err
	}
	passwordHash, err := hashAccountPassword(password, accounts.random)
	if err != nil {
		return ServerAccount{}, fmt.Errorf("hash replacement local account password: %w", err)
	}
	timestamp := formatServerTimestamp(accounts.now())
	return withImmediateTransaction(ctx, accounts.database, func(connection *sql.Conn) (ServerAccount, error) {
		_, err := selectLocalAccount(ctx, connection, accountID)
		if err != nil {
			return ServerAccount{}, err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE accounts SET password_hash = ?, updated_at = ? WHERE internal_id = ?`, passwordHash, timestamp, accountID); err != nil {
			return ServerAccount{}, fmt.Errorf("reset local account password: %w", err)
		}
		return selectAccount(ctx, connection, accountID)
	})
}

func (accounts *ServerAccounts) ChangeOwnLocalAccountPassword(ctx context.Context, accountID, currentPassword, replacementPassword string) (ServerAccount, error) {
	if err := validateAccountPassword(replacementPassword); err != nil {
		return ServerAccount{}, err
	}
	row, err := selectAccountRow(ctx, accounts.database, accountID)
	if err != nil {
		return ServerAccount{}, serverDomainError("AUTHENTICATION_REQUIRED", "Authenticated session is required")
	}
	if row.Kind == AccountKindEnvironment {
		return ServerAccount{}, serverDomainError("MANAGED_ACCOUNT", "Deployment Administrator is managed by environment variables")
	}
	if !verifyAccountPassword(currentPassword, row.PasswordHash.String) {
		return ServerAccount{}, serverDomainError("INVALID_CURRENT_PASSWORD", "Current password is invalid")
	}
	return accounts.ResetLocalAccountPassword(ctx, accountID, replacementPassword)
}

func (accounts *ServerAccounts) DeleteLocalAccount(ctx context.Context, accountID string) error {
	_, err := withImmediateTransaction(ctx, accounts.database, func(connection *sql.Conn) (struct{}, error) {
		_, err := selectLocalAccount(ctx, connection, accountID)
		if err != nil {
			return struct{}{}, err
		}
		var clientCount int64
		if err := connection.QueryRowContext(ctx, `SELECT count(*) FROM clients WHERE owner_account_id = ?`, accountID).Scan(&clientCount); err != nil {
			return struct{}{}, fmt.Errorf("count local account clients: %w", err)
		}
		if clientCount > 0 {
			return struct{}{}, serverDomainError("ACCOUNT_NOT_EMPTY", "Control Plane Account still owns Trusted Tunnel Clients")
		}
		if _, err := connection.ExecContext(ctx, `DELETE FROM accounts WHERE internal_id = ?`, accountID); err != nil {
			return struct{}{}, fmt.Errorf("delete local account: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

func (accounts *ServerAccounts) accountCredential(ctx context.Context, accountID string) (ServerAccount, string, error) {
	row, err := selectAccountRow(ctx, accounts.database, accountID)
	if err != nil {
		return ServerAccount{}, "", err
	}
	account := serverAccountFromRow(row)
	if account.Kind == AccountKindEnvironment {
		return account, accounts.environmentPassword, nil
	}
	return account, row.PasswordHash.String, nil
}

func (accounts *ServerAccounts) verifyCredentials(ctx context.Context, username, password string) (ServerAccount, error) {
	_, usernameKey, usernameErr := normalizeAccountUsername(username)
	row, err := selectAccountRowByUsername(ctx, accounts.database, usernameKey)
	hash := accounts.environmentHash
	if usernameErr == nil && err == nil && row.Kind == AccountKindLocal {
		hash = row.PasswordHash.String
	}
	valid := verifyAccountPassword(password, hash)
	if usernameErr != nil || err == sql.ErrNoRows || err != nil || !valid {
		return ServerAccount{}, serverDomainError("AUTHENTICATION_FAILED", "Account credentials are invalid")
	}
	return serverAccountFromRow(row), nil
}

type accountQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type serverAccountRow struct {
	ID           string
	Kind         AccountKind
	Username     string
	UsernameKey  string
	Role         AccountRole
	PasswordHash sql.NullString
	CreatedAt    string
	UpdatedAt    string
}

func selectAccount(ctx context.Context, queryer accountQueryer, accountID string) (ServerAccount, error) {
	row, err := selectAccountRow(ctx, queryer, accountID)
	if err == sql.ErrNoRows {
		return ServerAccount{}, serverDomainError("AUTHENTICATION_REQUIRED", "Authenticated session is required")
	}
	if err != nil {
		return ServerAccount{}, fmt.Errorf("read Tunnel server account: %w", err)
	}
	return serverAccountFromRow(row), nil
}

func selectLocalAccount(ctx context.Context, queryer accountQueryer, accountID string) (ServerAccount, error) {
	row, err := selectAccountRow(ctx, queryer, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return ServerAccount{}, serverDomainError("NOT_FOUND", "Control Plane Account was not found")
	}
	if err != nil {
		return ServerAccount{}, fmt.Errorf("read Tunnel server account: %w", err)
	}
	account := serverAccountFromRow(row)
	if account.Kind == AccountKindEnvironment {
		return ServerAccount{}, serverDomainError("MANAGED_ACCOUNT", "Deployment Administrator is managed by environment variables")
	}
	return account, nil
}

func selectAccountByUsername(ctx context.Context, queryer accountQueryer, usernameKey string) (ServerAccount, error) {
	row, err := selectAccountRowByUsername(ctx, queryer, usernameKey)
	if err != nil {
		return ServerAccount{}, err
	}
	return serverAccountFromRow(row), nil
}

func selectAccountRow(ctx context.Context, queryer accountQueryer, accountID string) (serverAccountRow, error) {
	return scanAccount(queryer.QueryRowContext(ctx, `SELECT internal_id, kind, username, username_key, role, password_hash, created_at, updated_at FROM accounts WHERE internal_id = ?`, accountID))
}

func selectAccountRowByUsername(ctx context.Context, queryer accountQueryer, usernameKey string) (serverAccountRow, error) {
	return scanAccount(queryer.QueryRowContext(ctx, `SELECT internal_id, kind, username, username_key, role, password_hash, created_at, updated_at FROM accounts WHERE username_key = ?`, usernameKey))
}

func scanAccount(scanner interface{ Scan(...any) error }) (serverAccountRow, error) {
	var row serverAccountRow
	if err := scanner.Scan(&row.ID, &row.Kind, &row.Username, &row.UsernameKey, &row.Role, &row.PasswordHash, &row.CreatedAt, &row.UpdatedAt); err != nil {
		return serverAccountRow{}, err
	}
	return row, nil
}

func serverAccountFromRow(row serverAccountRow) ServerAccount {
	return ServerAccount{ID: row.ID, Kind: row.Kind, Username: row.Username, Role: row.Role, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func normalizeAccountUsername(value string) (string, string, error) {
	if !accountUsernamePattern.MatchString(value) {
		return "", "", serverDomainError("INVALID_ACCOUNT", "Username must contain 1-64 ASCII letters, numbers, dots, underscores, or hyphens")
	}
	return value, strings.ToLower(value), nil
}

func validateAccountPassword(value string) error {
	if utf16CodeUnitCount(value) < 5 || utf16CodeUnitCount(value) > 256 {
		return serverDomainError("INVALID_ACCOUNT", "Password must contain 5-256 characters")
	}
	return nil
}

func hashAccountPassword(password string, random io.Reader) (string, error) {
	salt := make([]byte, argon2SaltLength)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", err
	}
	digest := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Parallelism, argon2KeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argon2Memory, argon2Time, argon2Parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func verifyAccountPassword(password, encoded string) bool {
	parameters, salt, digest, err := parseArgon2IDPHC(encoded)
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, parameters.time, parameters.memory, parameters.parallelism, uint32(len(digest)))
	return subtle.ConstantTimeCompare(actual, digest) == 1
}

type argon2IDParameters struct {
	memory      uint32
	time        uint32
	parallelism uint8
}

func parseArgon2IDPHC(encoded string) (argon2IDParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return argon2IDParameters{}, nil, nil, fmt.Errorf("invalid Argon2id PHC")
	}
	parameters := argon2IDParameters{}
	for _, value := range strings.Split(parts[3], ",") {
		key, raw, found := strings.Cut(value, "=")
		if !found {
			return argon2IDParameters{}, nil, nil, fmt.Errorf("invalid Argon2id PHC parameters")
		}
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || parsed == 0 {
			return argon2IDParameters{}, nil, nil, fmt.Errorf("invalid Argon2id PHC parameter")
		}
		switch key {
		case "m":
			parameters.memory = uint32(parsed)
		case "t":
			parameters.time = uint32(parsed)
		case "p":
			if parsed > 255 {
				return argon2IDParameters{}, nil, nil, fmt.Errorf("invalid Argon2id PHC parallelism")
			}
			parameters.parallelism = uint8(parsed)
		default:
			return argon2IDParameters{}, nil, nil, fmt.Errorf("unsupported Argon2id PHC parameter")
		}
	}
	if parameters.memory == 0 || parameters.time == 0 || parameters.parallelism == 0 {
		return argon2IDParameters{}, nil, nil, fmt.Errorf("incomplete Argon2id PHC parameters")
	}
	salt, err := decodePHCBase64(parts[4])
	if err != nil || len(salt) == 0 {
		return argon2IDParameters{}, nil, nil, fmt.Errorf("invalid Argon2id PHC salt")
	}
	digest, err := decodePHCBase64(parts[5])
	if err != nil || len(digest) == 0 {
		return argon2IDParameters{}, nil, nil, fmt.Errorf("invalid Argon2id PHC hash")
	}
	return parameters, salt, digest, nil
}

func decodePHCBase64(value string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(value)
}

func mapEnvironmentAccountError(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "accounts.username_key") && strings.Contains(message, "unique") {
		return serverDomainError("INVALID_CONFIG", "Environment administrator username conflicts with a local account")
	}
	return fmt.Errorf("initialize environment administrator: %w", err)
}

func mapLocalAccountError(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "accounts.username_key") && strings.Contains(message, "unique") {
		return serverDomainError("USERNAME_TAKEN", "Username is already in use")
	}
	return fmt.Errorf("create local account: %w", err)
}
