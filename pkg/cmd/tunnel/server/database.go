package server

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
)

import _ "github.com/ncruces/go-sqlite3/driver"

const schemaVersion = "1"

const tunnelSchema = `
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

func openDatabase(path string) (*sql.DB, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Tunnel database path: %w", err)
	}
	databaseURL := databaseFileURI(absPath)
	database, err := sql.Open("sqlite3", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open Tunnel database: %w", err)
	}
	database.SetMaxOpenConns(1)
	if err := initializeDatabase(context.Background(), database); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func initializeDatabase(ctx context.Context, database *sql.DB) error {
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
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES('schema_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, schemaVersion); err != nil {
		return fmt.Errorf("record Tunnel database schema version: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit Tunnel database initialization: %w", err)
	}
	return nil
}
