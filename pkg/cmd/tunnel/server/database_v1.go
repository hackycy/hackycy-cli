package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	serverent "github.com/hackycy/hackycy-cli/ent/tunnel/server"
	"github.com/hackycy/hackycy-cli/internal/windowsacl"
	_ "github.com/ncruces/go-sqlite3/driver"
)

//go:embed migrations/20260926_v1_initial_schema.sql
var serverV1Schema string

//go:embed migrations/20260929_node_policies.sql
var serverNodePoliciesMigration string

const serverV1SchemaVersion = "2"
const serverV1BaseSchemaVersion = "1"

// openEmptyServerV1Database is called under the data directory's process lock.
// The returned Ent client borrows db; the caller closes db exactly once.
func openEmptyServerV1Database(ctx context.Context, dataDirectory string) (*sql.DB, *serverent.Client, error) {
	if strings.TrimSpace(dataDirectory) == "" {
		return nil, nil, fmt.Errorf("Server data directory is required")
	}
	dataDirectory, err := filepath.Abs(dataDirectory)
	if err != nil {
		return nil, nil, err
	}
	stateDirectory := filepath.Join(dataDirectory, "server-state-v1")
	if err := prepareEmptyServerV1Directory(stateDirectory); err != nil {
		return nil, nil, err
	}
	return createServerV1Database(ctx, stateDirectory)
}

func createServerV1Database(ctx context.Context, stateDirectory string) (*sql.DB, *serverent.Client, error) {
	path := filepath.Join(stateDirectory, databaseFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create Server v1 database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, nil, err
	}
	if err := windowsacl.RestrictPrivatePath(path); err != nil {
		return nil, nil, err
	}
	db, err := openServerV1SQLDatabase(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, serverV1Schema); err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		return nil, nil, fmt.Errorf("create Server v1 schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if err := applyServerNodePoliciesMigration(ctx, db); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate fresh Server v1 schema: %w", err)
	}
	return db, serverent.NewClient(serverent.Driver(entsql.OpenDB(dialect.SQLite, db))), nil
}

func openServerV1SQLDatabase(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", databaseFileURI(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure Server v1 database: %w", err)
		}
	}
	return db, nil
}

func migrateServerV1Database(ctx context.Context, db *sql.DB) error {
	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Server database migration: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var version string
	if err := transaction.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		return fmt.Errorf("read Server database schema version: %w", err)
	}
	switch version {
	case serverV1BaseSchemaVersion:
		if _, err := transaction.ExecContext(ctx, serverNodePoliciesMigration); err != nil {
			return fmt.Errorf("apply Server database migration 2: %w", err)
		}
		if err := recordServerMigrations(ctx, transaction); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE meta SET value = ? WHERE key = 'schema_version'`, serverV1SchemaVersion); err != nil {
			return fmt.Errorf("record Server database schema version: %w", err)
		}
	case serverV1SchemaVersion:
		if err := verifyServerMigrations(ctx, transaction); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported Server database schema version %q", version)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit Server database migration: %w", err)
	}
	return nil
}

func applyServerNodePoliciesMigration(ctx context.Context, db *sql.DB) error {
	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, serverNodePoliciesMigration); err != nil {
		return err
	}
	if err := recordServerMigrations(ctx, transaction); err != nil {
		return err
	}
	return transaction.Commit()
}

func serverMigrationChecksum(contents string) string {
	digest := sha256.Sum256([]byte(contents))
	return hex.EncodeToString(digest[:])
}

func recordServerMigrations(ctx context.Context, transaction *sql.Tx) error {
	for index, contents := range []string{serverV1Schema, serverNodePoliciesMigration} {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO schema_migrations(version, checksum, applied_at) VALUES (?, ?, datetime('now'))`, index+1, serverMigrationChecksum(contents)); err != nil {
			return fmt.Errorf("record Server migration %d: %w", index+1, err)
		}
	}
	return nil
}

func verifyServerMigrations(ctx context.Context, transaction *sql.Tx) error {
	rows, err := transaction.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read Server migration history: %w", err)
	}
	defer rows.Close()
	versions := []string{serverV1Schema, serverNodePoliciesMigration}
	index := 0
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return err
		}
		if index >= len(versions) || version != index+1 || checksum != serverMigrationChecksum(versions[index]) {
			return fmt.Errorf("Server database migration history differs from the current SQL assets")
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if index != len(versions) {
		return fmt.Errorf("Server database migration history is incomplete")
	}
	return nil
}

func serverEntOnConnection(connection *sql.Conn) *serverent.Client {
	driver := entsql.NewDriver(dialect.SQLite, entsql.Conn{ExecQuerier: connection})
	return serverent.NewClient(serverent.Driver(serverImmediateEntDriver{Driver: driver}))
}

func serverEntForQueryer(queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) *serverent.Client {
	if connection, ok := queryer.(*sql.Conn); ok {
		return serverEntOnConnection(connection)
	}
	return serverent.NewClient(serverent.Driver(entsql.OpenDB(dialect.SQLite, queryer.(*sql.DB))))
}

type serverImmediateEntDriver struct{ *entsql.Driver }

func (driver serverImmediateEntDriver) Tx(context.Context) (dialect.Tx, error) {
	return dialect.NopTx(driver.Driver), nil
}

func initializeServerV1Identity(ctx context.Context, client *serverent.Client, publicKey string) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range map[string]string{
		"schema_version":     serverV1SchemaVersion,
		"controller_key_ref": controllerKeyFileName,
		"controller_pubkey":  publicKey,
	} {
		if _, err := tx.Meta.Create().SetID(key).SetValue(value).Save(ctx); err != nil {
			return err
		}
	}
	now := formatServerTimestamp(time.Now())
	if _, err := tx.Node.Create().SetID("local").SetKind("local").SetName("Local").SetCreatedAt(now).SetUpdatedAt(now).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func copyDatabaseFile(sourcePath, targetPath string, required bool) error {
	info, err := os.Lstat(sourcePath)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Tunnel database file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Tunnel database file %s must be regular", filepath.Base(sourcePath))
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("read Tunnel database inspection source: %w", err)
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create Tunnel database inspection copy: %w", err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return fmt.Errorf("copy Tunnel database for inspection: %w", err)
	}
	return target.Close()
}

func inspectServerV1Database(ctx context.Context, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect Server v1 database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Server v1 database must be a regular file")
	}
	directory, err := os.MkdirTemp("", "server-v1-inspect-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	copyPath := filepath.Join(directory, databaseFileName)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyDatabaseFile(path+suffix, copyPath+suffix, suffix == ""); err != nil {
			return "", err
		}
	}
	db, err := sql.Open("sqlite3", databaseFileURI(copyPath))
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err := migrateServerV1Database(ctx, db); err != nil {
		return "", err
	}
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return "", fmt.Errorf("Server v1 database is damaged: %v (%s)", err, integrity)
	}
	if err := verifyServerV1Schema(ctx, db); err != nil {
		return "", err
	}
	var version, keyRef, publicKey string
	for key, target := range map[string]*string{"schema_version": &version, "controller_key_ref": &keyRef, "controller_pubkey": &publicKey} {
		if err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(target); err != nil {
			return "", fmt.Errorf("Server v1 identity metadata is incomplete: %w", err)
		}
	}
	if version != serverV1SchemaVersion || keyRef != controllerKeyFileName || len(publicKey) != 64 {
		return "", fmt.Errorf("Server v1 identity metadata is invalid")
	}
	var kind, lifecycle string
	if err := db.QueryRowContext(ctx, "SELECT kind, lifecycle FROM nodes WHERE node_id = 'local'").Scan(&kind, &lifecycle); err != nil || kind != "local" || lifecycle != "active" {
		return "", fmt.Errorf("Server v1 Local Node is missing or invalid: %v", err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if rows.Next() {
		return "", fmt.Errorf("Server v1 database has foreign key violations")
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if err := verifyServerV1BusinessInvariants(ctx, db); err != nil {
		return "", err
	}
	return publicKey, nil
}

func verifyServerV1BusinessInvariants(ctx context.Context, db *sql.DB) error {
	client := serverent.NewClient(serverent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	clients, err := client.ServerClient.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("verify Server v1 Client ownership: %w", err)
	}
	clientNodes := make(map[string]string, len(clients))
	for _, item := range clients {
		clientNodes[item.ID] = item.NodeID
	}
	pools, err := client.NodePortPool.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("verify Server v1 Node port pools: %w", err)
	}
	poolByNode := make(map[string]*serverent.NodePortPool, len(pools))
	for _, pool := range pools {
		poolByNode[pool.NodeID] = pool
	}
	tunnels, err := client.Tunnel.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("verify Server v1 Tunnels: %w", err)
	}
	tunnelNodes := make(map[string]string, len(tunnels))
	for _, item := range tunnels {
		if clientNodes[item.ClientInternalID] != item.NodeID {
			return fmt.Errorf("Server v1 Tunnel %s has a different Node from its Client", item.ID)
		}
		tunnelNodes[item.ID] = item.NodeID
		if item.Protocol == "tcp" || item.Protocol == "udp" {
			pool := poolByNode[item.NodeID]
			if pool == nil || item.ServerPort == nil || *item.ServerPort < pool.PortStart || *item.ServerPort > pool.PortEnd {
				return fmt.Errorf("Server v1 Tunnel %s uses a port outside its Node pool", item.ID)
			}
		}
	}
	routes, err := client.TunnelHTTPRoute.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("verify Server v1 HTTP routes: %w", err)
	}
	hostnameNodes := make(map[string]string, len(routes))
	for _, route := range routes {
		nodeID := tunnelNodes[route.TunnelID]
		if previous, exists := hostnameNodes[route.Hostname]; exists && previous != nodeID {
			return fmt.Errorf("Server v1 hostname %s is split across Nodes", route.Hostname)
		}
		hostnameNodes[route.Hostname] = nodeID
	}
	return nil
}

type serverSchemaEntry struct{ kind, name, table, statement string }

func verifyServerV1Schema(ctx context.Context, db *sql.DB) error {
	expected, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return err
	}
	defer expected.Close()
	if _, err := expected.ExecContext(ctx, serverV1Schema); err != nil {
		return err
	}
	if _, err := expected.ExecContext(ctx, serverNodePoliciesMigration); err != nil {
		return err
	}
	actualSchema, err := readServerSchema(ctx, db)
	if err != nil {
		return err
	}
	expectedSchema, err := readServerSchema(ctx, expected)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actualSchema, expectedSchema) {
		return errors.New("Server v1 database schema does not match the current SQL asset")
	}
	return nil
}

func readServerSchema(ctx context.Context, db *sql.DB) ([]serverSchemaEntry, error) {
	rows, err := db.QueryContext(ctx, "SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []serverSchemaEntry
	for rows.Next() {
		var entry serverSchemaEntry
		if err := rows.Scan(&entry.kind, &entry.name, &entry.table, &entry.statement); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func prepareEmptyServerV1Directory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create Server v1 state directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return fmt.Errorf("Server v1 state directory must be private and not a symlink")
	}
	if err := windowsacl.RestrictPrivatePath(path); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("Server v1 state directory is not empty")
	}
	return nil
}
