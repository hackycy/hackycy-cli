package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

import _ "github.com/ncruces/go-sqlite3/driver"

const schemaVersion = "3"

const tunnelSchemaV1 = `
CREATE TABLE meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE accounts (
  internal_id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('environment', 'local')),
  username TEXT NOT NULL,
  username_key TEXT NOT NULL UNIQUE,
  role TEXT NOT NULL CHECK (role IN ('admin', 'user')),
  password_hash TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK (
    (kind = 'environment' AND role = 'admin' AND password_hash IS NULL)
    OR
    (kind = 'local' AND password_hash IS NOT NULL)
  )
);

CREATE UNIQUE INDEX accounts_single_environment
ON accounts(kind) WHERE kind = 'environment';

CREATE TABLE clients (
  internal_id TEXT PRIMARY KEY,
  owner_account_id TEXT NOT NULL REFERENCES accounts(internal_id) ON DELETE RESTRICT,
  remark TEXT NOT NULL DEFAULT '' CHECK (length(remark) <= 100),
  token TEXT NOT NULL UNIQUE,
  desired_revision INTEGER NOT NULL DEFAULT 0 CHECK (desired_revision >= 0),
  last_applied_revision INTEGER NOT NULL DEFAULT 0 CHECK (last_applied_revision >= 0),
  revocation_pending INTEGER NOT NULL DEFAULT 0 CHECK (revocation_pending IN (0, 1)),
  created_at TEXT NOT NULL,
  rotated_at TEXT
);

CREATE TABLE tunnels (
  id TEXT PRIMARY KEY,
  client_internal_id TEXT NOT NULL REFERENCES clients(internal_id) ON DELETE CASCADE,
  label TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 100),
  protocol TEXT NOT NULL CHECK (protocol IN ('http', 'tcp', 'udp')),
  custom_domains TEXT,
  location TEXT,
  server_port INTEGER,
  local_host TEXT NOT NULL,
  local_port INTEGER NOT NULL CHECK (local_port BETWEEN 1 AND 65535),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  options_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(options_json)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK (
    (protocol = 'http' AND custom_domains IS NOT NULL AND server_port IS NULL)
    OR (protocol IN ('tcp', 'udp') AND custom_domains IS NULL AND location IS NULL AND server_port IS NOT NULL)
  )
);

CREATE TABLE tunnel_http_routes (
  tunnel_id TEXT NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
  hostname TEXT NOT NULL COLLATE NOCASE,
  location TEXT NOT NULL,
  PRIMARY KEY(hostname, location)
);

CREATE UNIQUE INDEX tunnels_unique_transport_port
ON tunnels(protocol, server_port) WHERE protocol IN ('tcp', 'udp');

CREATE INDEX tunnels_by_client
ON tunnels(client_internal_id, created_at, id);

CREATE INDEX clients_by_owner
ON clients(owner_account_id, created_at, internal_id);
`

const tunnelSchema = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE nodes (
  node_id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('local', 'remote')),
  name TEXT NOT NULL,
  lifecycle TEXT NOT NULL DEFAULT 'active' CHECK (lifecycle IN ('active', 'removing')),
  advertised_frp_host TEXT,
  advertised_frp_port INTEGER CHECK (advertised_frp_port BETWEEN 1 AND 65535),
  http_ingress_host TEXT,
  http_ingress_port INTEGER CHECK (http_ingress_port BETWEEN 1 AND 65535),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK ((advertised_frp_host IS NULL) = (advertised_frp_port IS NULL)),
  CHECK ((http_ingress_host IS NULL) = (http_ingress_port IS NULL)),
  CHECK ((node_id = 'local' AND kind = 'local') OR (node_id <> 'local' AND kind = 'remote'))
);
CREATE UNIQUE INDEX nodes_one_local ON nodes(kind) WHERE kind = 'local';
CREATE UNIQUE INDEX nodes_frp_endpoint ON nodes(advertised_frp_host, advertised_frp_port)
  WHERE lifecycle = 'active' AND advertised_frp_host IS NOT NULL;
CREATE TRIGGER nodes_keep_local_delete BEFORE DELETE ON nodes WHEN OLD.node_id = 'local'
BEGIN SELECT RAISE(ABORT, 'local Node cannot be deleted'); END;
CREATE TRIGGER nodes_keep_local_update BEFORE UPDATE ON nodes WHEN OLD.node_id = 'local' AND
  (NEW.node_id <> 'local' OR NEW.kind <> 'local' OR NEW.lifecycle <> 'active')
BEGIN SELECT RAISE(ABORT, 'local Node cannot be changed'); END;
CREATE TABLE remote_nodes (
  node_id TEXT PRIMARY KEY REFERENCES nodes(node_id) ON DELETE CASCADE,
  node_public_key TEXT NOT NULL UNIQUE,
  management_address TEXT NOT NULL UNIQUE,
  frp_bind_port INTEGER NOT NULL CHECK (frp_bind_port BETWEEN 1 AND 65535),
  http_vhost_port INTEGER NOT NULL CHECK (http_vhost_port BETWEEN 1 AND 65535),
  port_start INTEGER NOT NULL CHECK (port_start BETWEEN 1 AND 65535),
  port_end INTEGER NOT NULL CHECK (port_end BETWEEN port_start AND 65535),
  desired_revision INTEGER NOT NULL DEFAULT 0 CHECK (desired_revision >= 0),
  desired_hash TEXT,
  desired_snapshot TEXT,
  active_token TEXT NOT NULL,
  staged_token TEXT,
  staged_token_revision INTEGER
);
CREATE TABLE node_port_pools (
  node_id TEXT PRIMARY KEY REFERENCES nodes(node_id) ON DELETE CASCADE,
  port_start INTEGER NOT NULL CHECK (port_start BETWEEN 1 AND 65535),
  port_end INTEGER NOT NULL CHECK (port_end BETWEEN port_start AND 65535)
);
CREATE TABLE accounts (
  internal_id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('environment', 'local')),
  username TEXT NOT NULL,
  username_key TEXT NOT NULL UNIQUE,
  role TEXT NOT NULL CHECK (role IN ('admin', 'user')),
  password_hash TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK ((kind = 'environment' AND role = 'admin' AND password_hash IS NULL)
    OR (kind = 'local' AND password_hash IS NOT NULL))
);
CREATE UNIQUE INDEX accounts_single_environment ON accounts(kind) WHERE kind = 'environment';
CREATE TABLE clients (
  internal_id TEXT PRIMARY KEY,
  owner_account_id TEXT NOT NULL REFERENCES accounts(internal_id) ON DELETE RESTRICT,
  node_id TEXT NOT NULL DEFAULT 'local' REFERENCES nodes(node_id) ON DELETE RESTRICT,
  pending_node_id TEXT REFERENCES nodes(node_id) ON DELETE RESTRICT,
  pending_since TEXT,
  last_applied_node_id TEXT REFERENCES nodes(node_id) ON DELETE SET NULL,
  remark TEXT NOT NULL DEFAULT '' CHECK (length(remark) <= 100),
  token TEXT NOT NULL UNIQUE,
  desired_revision INTEGER NOT NULL DEFAULT 0 CHECK (desired_revision >= 0),
  last_applied_revision INTEGER NOT NULL DEFAULT 0 CHECK (last_applied_revision >= 0),
  revocation_pending INTEGER NOT NULL DEFAULT 0 CHECK (revocation_pending IN (0, 1)),
  desired_restart_generation INTEGER NOT NULL DEFAULT 0 CHECK (desired_restart_generation >= 0),
  completed_restart_generation INTEGER NOT NULL DEFAULT 0 CHECK (completed_restart_generation >= 0),
  restart_error_generation INTEGER CHECK (restart_error_generation IS NULL OR restart_error_generation >= 0),
  restart_error_code TEXT,
  restart_error_message TEXT,
  created_at TEXT NOT NULL,
  rotated_at TEXT,
  UNIQUE(internal_id, node_id)
);
CREATE INDEX clients_by_owner ON clients(owner_account_id, created_at, internal_id);
CREATE TABLE tunnels (
  id TEXT PRIMARY KEY,
  client_internal_id TEXT NOT NULL,
  node_id TEXT NOT NULL DEFAULT 'local',
  label TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 100),
  protocol TEXT NOT NULL CHECK (protocol IN ('http', 'tcp', 'udp')),
  custom_domains TEXT,
  location TEXT,
  server_port INTEGER,
  local_host TEXT NOT NULL,
  local_port INTEGER NOT NULL CHECK (local_port BETWEEN 1 AND 65535),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  options_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(options_json)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(id, node_id),
  FOREIGN KEY(client_internal_id, node_id) REFERENCES clients(internal_id, node_id)
    ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
  CHECK ((protocol = 'http' AND custom_domains IS NOT NULL AND server_port IS NULL)
    OR (protocol IN ('tcp', 'udp') AND custom_domains IS NULL AND location IS NULL AND server_port IS NOT NULL))
);
CREATE UNIQUE INDEX tunnels_unique_transport_port ON tunnels(node_id, protocol, server_port)
  WHERE protocol IN ('tcp', 'udp');
CREATE INDEX tunnels_by_client ON tunnels(client_internal_id, created_at, id);
CREATE TABLE hostname_owners (
  hostname_key TEXT PRIMARY KEY COLLATE NOCASE,
  node_id TEXT NOT NULL REFERENCES nodes(node_id) ON DELETE RESTRICT,
  UNIQUE(hostname_key, node_id)
);
CREATE TABLE tunnel_http_routes (
  tunnel_id TEXT NOT NULL,
  node_id TEXT NOT NULL DEFAULT 'local',
  hostname TEXT NOT NULL COLLATE NOCASE,
  location TEXT NOT NULL,
  PRIMARY KEY(hostname, location),
  FOREIGN KEY(tunnel_id, node_id) REFERENCES tunnels(id, node_id)
    ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY(hostname, node_id) REFERENCES hostname_owners(hostname_key, node_id)
    DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER tunnel_http_routes_release_owner AFTER DELETE ON tunnel_http_routes
BEGIN
  DELETE FROM hostname_owners WHERE hostname_key = OLD.hostname AND
    NOT EXISTS (SELECT 1 FROM tunnel_http_routes WHERE hostname = OLD.hostname);
END;
`

func openDatabase(path, controllerPublicKey string) (*sql.DB, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Tunnel database path: %w", err)
	}
	storedPublicKey, err := inspectExistingDatabase(absPath)
	if err != nil {
		return nil, err
	}
	if storedPublicKey != "" && storedPublicKey != controllerPublicKey {
		return nil, fmt.Errorf("Tunnel Controller identity does not match database; restore the original identity file")
	}
	databaseURL := databaseFileURI(absPath)
	database, err := sql.Open("sqlite3", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open Tunnel database: %w", err)
	}
	database.SetMaxOpenConns(1)
	if err := initializeDatabase(context.Background(), database, controllerPublicKey); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

// Inspect a private copy so SQLite can recover WAL state without creating or
// updating auxiliary files in an incompatible operator-owned directory.
func inspectExistingDatabase(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, sidecarErr := os.Stat(path + suffix); sidecarErr == nil {
				return "", fmt.Errorf("Tunnel database is absent but %s remains; use an empty data directory", filepath.Base(path+suffix))
			} else if !errors.Is(sidecarErr, os.ErrNotExist) {
				return "", fmt.Errorf("inspect Tunnel database sidecar: %w", sidecarErr)
			}
		}
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect Tunnel database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Tunnel database must be a regular file; use an empty data directory")
	}
	directory, err := os.MkdirTemp("", "tunnel-db-inspect-")
	if err != nil {
		return "", fmt.Errorf("create Tunnel database inspection directory: %w", err)
	}
	defer os.RemoveAll(directory)
	copyPath := filepath.Join(directory, databaseFileName)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyDatabaseFile(path+suffix, copyPath+suffix, suffix == ""); err != nil {
			return "", err
		}
	}
	database, err := sql.Open("sqlite3", databaseFileURI(copyPath))
	if err != nil {
		return "", fmt.Errorf("inspect Tunnel database: %w", err)
	}
	defer database.Close()
	var version string
	if err := database.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		return "", fmt.Errorf("Tunnel database has no readable schema version; use an empty data directory: %w", err)
	}
	if version != schemaVersion {
		return "", fmt.Errorf("unsupported Tunnel database schema version %q; use an empty data directory", version)
	}
	var integrity string
	if err := database.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return "", fmt.Errorf("Tunnel database is damaged or incomplete; use an empty data directory: %v (%s)", err, integrity)
	}
	for _, query := range []string{
		`SELECT node_id, kind, lifecycle, advertised_frp_host, advertised_frp_port FROM nodes LIMIT 0`,
		`SELECT node_id, node_public_key, desired_revision, active_token FROM remote_nodes LIMIT 0`,
		`SELECT node_id, port_start, port_end FROM node_port_pools LIMIT 0`,
		`SELECT internal_id, kind FROM accounts LIMIT 0`,
		`SELECT internal_id, node_id, pending_node_id, last_applied_node_id FROM clients LIMIT 0`,
		`SELECT id, client_internal_id, node_id, protocol, server_port FROM tunnels LIMIT 0`,
		`SELECT hostname_key, node_id FROM hostname_owners LIMIT 0`,
		`SELECT tunnel_id, node_id, hostname, location FROM tunnel_http_routes LIMIT 0`,
	} {
		rows, err := database.Query(query)
		if err != nil {
			return "", fmt.Errorf("Tunnel database schema v3 is incomplete; use an empty data directory: %w", err)
		}
		_ = rows.Close()
	}
	var publicKey, reference string
	if err := database.QueryRow(`SELECT value FROM meta WHERE key = 'controller_pubkey'`).Scan(&publicKey); err != nil {
		return "", fmt.Errorf("Tunnel database Controller identity is missing: %w", err)
	}
	if len(publicKey) != 64 {
		return "", fmt.Errorf("Tunnel database Controller public key is invalid")
	}
	if err := database.QueryRow(`SELECT value FROM meta WHERE key = 'controller_key_ref'`).Scan(&reference); err != nil || reference != controllerKeyFileName {
		return "", fmt.Errorf("Tunnel database Controller identity reference is invalid: %v", err)
	}
	return publicKey, nil
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

func initializeDatabase(ctx context.Context, database *sql.DB, controllerPublicKey string) error {
	if controllerPublicKey == "" {
		return fmt.Errorf("Tunnel Controller identity is required")
	}
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure Tunnel database: %w", err)
		}
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Tunnel database initialization: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	var hasMeta bool
	if err := transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'meta')`).Scan(&hasMeta); err != nil {
		return fmt.Errorf("inspect Tunnel database schema: %w", err)
	}
	if !hasMeta {
		if _, err := transaction.ExecContext(ctx, tunnelSchema); err != nil {
			return fmt.Errorf("create Tunnel database schema: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES('schema_version', ?)`, schemaVersion); err != nil {
			return fmt.Errorf("record Tunnel database schema version: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES('controller_key_ref', ?), ('controller_pubkey', ?)`, controllerKeyFileName, controllerPublicKey); err != nil {
			return fmt.Errorf("record Tunnel Controller identity: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('local', 'local', 'Local', datetime('now'), datetime('now'))`); err != nil {
			return fmt.Errorf("create Local Node: %w", err)
		}
	} else {
		var version string
		if err := transaction.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
			return fmt.Errorf("read Tunnel database schema version: %w", err)
		}
		if version != schemaVersion {
			return fmt.Errorf("unsupported Tunnel database schema version %q", version)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit Tunnel database initialization: %w", err)
	}
	return nil
}
