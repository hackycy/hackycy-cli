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

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/account"
	sqlite3 "github.com/ncruces/go-sqlite3"
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
	tx, err := serverEntForQueryer(accounts.database).Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := tx.Account.Get(ctx, environmentAdministratorID)
	if serverent.IsNotFound(err) {
		_, err = tx.Account.Create().SetID(environmentAdministratorID).SetKind(account.KindEnvironment).
			SetUsername(username).SetUsernameKey(usernameKey).SetRole(account.RoleAdmin).
			SetCreatedAt(timestamp).SetUpdatedAt(timestamp).Save(ctx)
	} else if err == nil {
		_, err = tx.Account.UpdateOne(current).SetUsername(username).SetUsernameKey(usernameKey).
			SetRole(account.RoleAdmin).ClearPasswordHash().SetUpdatedAt(timestamp).Save(ctx)
	}
	if err != nil {
		return mapEnvironmentAccountError(ctx, tx.Account, usernameKey, err)
	}
	return tx.Commit()
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
	client := serverEntForQueryer(accounts.database)
	items, err := client.Account.Query().Order(account.ByKind(), account.ByUsernameKey(), account.ByID()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Tunnel server accounts: %w", err)
	}
	views := make([]ServerAccountView, 0, len(items))
	for _, item := range items {
		count, err := item.QueryClients().Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("count Tunnel server account clients: %w", err)
		}
		view := ServerAccountView{ServerAccount: serverAccountFromRow(serverAccountRowFromEnt(item)), ClientCount: int64(count)}
		view.ManagedByEnvironment = view.Kind == AccountKindEnvironment
		views = append(views, view)
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
	client := serverEntForQueryer(accounts.database)
	created, err := client.Account.Create().SetID(accountID).
		SetKind(account.KindLocal).SetUsername(username).SetUsernameKey(usernameKey).
		SetRole(account.Role(role)).SetPasswordHash(passwordHash).
		SetCreatedAt(timestamp).SetUpdatedAt(timestamp).Save(ctx)
	if err != nil {
		return ServerAccount{}, mapLocalAccountError(ctx, client.Account, usernameKey, err)
	}
	return serverAccountFromRow(serverAccountRowFromEnt(created)), nil
}

func (accounts *ServerAccounts) ChangeLocalAccountRole(ctx context.Context, accountID string, role AccountRole) (ServerAccount, bool, error) {
	if role != AccountRoleAdmin && role != AccountRoleUser {
		return ServerAccount{}, false, serverDomainError("INVALID_ACCOUNT", "Account role must be admin or user")
	}
	timestamp := formatServerTimestamp(accounts.now())
	tx, err := serverEntForQueryer(accounts.database).Tx(ctx)
	if err != nil {
		return ServerAccount{}, false, err
	}
	defer tx.Rollback()
	current, err := localAccountInEntTx(ctx, tx, accountID)
	if err != nil {
		return ServerAccount{}, false, err
	}
	if AccountRole(current.Role) == role {
		return serverAccountFromRow(serverAccountRowFromEnt(current)), false, nil
	}
	updated, err := tx.Account.UpdateOne(current).SetRole(account.Role(role)).SetUpdatedAt(timestamp).Save(ctx)
	if err != nil {
		return ServerAccount{}, false, fmt.Errorf("change local account role: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ServerAccount{}, false, err
	}
	return serverAccountFromRow(serverAccountRowFromEnt(updated)), true, nil
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
	tx, err := serverEntForQueryer(accounts.database).Tx(ctx)
	if err != nil {
		return ServerAccount{}, err
	}
	defer tx.Rollback()
	current, err := localAccountInEntTx(ctx, tx, accountID)
	if err != nil {
		return ServerAccount{}, err
	}
	updated, err := tx.Account.UpdateOne(current).SetPasswordHash(passwordHash).SetUpdatedAt(timestamp).Save(ctx)
	if err != nil {
		return ServerAccount{}, fmt.Errorf("reset local account password: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ServerAccount{}, err
	}
	return serverAccountFromRow(serverAccountRowFromEnt(updated)), nil
}

func localAccountInEntTx(ctx context.Context, tx *serverent.Tx, accountID string) (*serverent.Account, error) {
	item, err := tx.Account.Get(ctx, accountID)
	if serverent.IsNotFound(err) {
		return nil, serverDomainError("NOT_FOUND", "Control Plane Account was not found")
	}
	if err != nil {
		return nil, fmt.Errorf("read Tunnel server account: %w", err)
	}
	if item.Kind == account.KindEnvironment {
		return nil, serverDomainError("MANAGED_ACCOUNT", "Deployment Administrator is managed by environment variables")
	}
	return item, nil
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
	tx, err := serverEntForQueryer(accounts.database).Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := localAccountInEntTx(ctx, tx, accountID)
	if err != nil {
		return err
	}
	clientCount, err := current.QueryClients().Count(ctx)
	if err != nil {
		return fmt.Errorf("count local account clients: %w", err)
	}
	if clientCount > 0 {
		return serverDomainError("ACCOUNT_NOT_EMPTY", "Control Plane Account still owns Trusted Tunnel Clients")
	}
	if err := tx.Account.DeleteOne(current).Exec(ctx); err != nil {
		return fmt.Errorf("delete local account: %w", err)
	}
	return tx.Commit()
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
	item, err := serverEntForQueryer(queryer).Account.Get(ctx, accountID)
	if serverent.IsNotFound(err) {
		return serverAccountRow{}, sql.ErrNoRows
	}
	if err != nil {
		return serverAccountRow{}, err
	}
	return serverAccountRowFromEnt(item), nil
}

func selectAccountRowByUsername(ctx context.Context, queryer accountQueryer, usernameKey string) (serverAccountRow, error) {
	item, err := serverEntForQueryer(queryer).Account.Query().Where(account.UsernameKeyEQ(usernameKey)).Only(ctx)
	if serverent.IsNotFound(err) {
		return serverAccountRow{}, sql.ErrNoRows
	}
	if err != nil {
		return serverAccountRow{}, err
	}
	return serverAccountRowFromEnt(item), nil
}

func serverAccountRowFromEnt(item *serverent.Account) serverAccountRow {
	row := serverAccountRow{ID: item.ID, Kind: AccountKind(item.Kind), Username: item.Username, UsernameKey: item.UsernameKey, Role: AccountRole(item.Role), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if item.PasswordHash != nil {
		row.PasswordHash = sql.NullString{String: *item.PasswordHash, Valid: true}
	}
	return row
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

func mapEnvironmentAccountError(ctx context.Context, accounts *serverent.AccountClient, usernameKey string, err error) error {
	if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) {
		owner, lookupErr := accounts.Query().Where(account.UsernameKeyEQ(usernameKey)).Only(ctx)
		if lookupErr != nil && !serverent.IsNotFound(lookupErr) {
			return fmt.Errorf("check environment administrator username conflict: %w", lookupErr)
		}
		if owner != nil && owner.ID != environmentAdministratorID {
			return serverDomainError("INVALID_CONFIG", "Environment administrator username conflicts with a local account")
		}
	}
	return fmt.Errorf("initialize environment administrator: %w", err)
}

func mapLocalAccountError(ctx context.Context, accounts *serverent.AccountClient, usernameKey string, err error) error {
	if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) {
		reserved, lookupErr := accounts.Query().Where(account.UsernameKeyEQ(usernameKey)).Exist(ctx)
		if lookupErr != nil {
			return fmt.Errorf("check local account username conflict: %w", lookupErr)
		}
		if reserved {
			return serverDomainError("USERNAME_TAKEN", "Username is already in use")
		}
	}
	return fmt.Errorf("create local account: %w", err)
}
