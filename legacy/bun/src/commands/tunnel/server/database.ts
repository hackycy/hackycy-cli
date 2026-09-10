import { mkdirSync } from 'node:fs'
import path from 'node:path'
import { Database } from 'bun:sqlite'

const SCHEMA_VERSION = 1

const TUNNEL_SCHEMA = `
CREATE TABLE IF NOT EXISTS tunnels (
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

CREATE TABLE IF NOT EXISTS tunnel_http_routes (
  tunnel_id TEXT NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
  hostname TEXT NOT NULL COLLATE NOCASE,
  location TEXT NOT NULL,
  PRIMARY KEY(hostname, location)
);

CREATE UNIQUE INDEX IF NOT EXISTS tunnels_unique_transport_port
ON tunnels(protocol, server_port) WHERE protocol IN ('tcp', 'udp');

CREATE INDEX IF NOT EXISTS tunnels_by_client
ON tunnels(client_internal_id, created_at, id);
`

const SCHEMA = `
CREATE TABLE IF NOT EXISTS meta (
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

CREATE TABLE IF NOT EXISTS clients (
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

${TUNNEL_SCHEMA}

CREATE INDEX clients_by_owner
ON clients(owner_account_id, created_at, internal_id);
`

export class TunnelDatabase {
  readonly sqlite: Database

  constructor(readonly filePath: string) {
    if (filePath !== ':memory:')
      mkdirSync(path.dirname(filePath), { recursive: true })
    this.sqlite = new Database(filePath, { create: true, strict: true })
    this.sqlite.run('PRAGMA foreign_keys = ON')
    this.sqlite.run('PRAGMA journal_mode = WAL')
    this.sqlite.run('PRAGMA busy_timeout = 5000')
    try {
      this.initialize()
    }
    catch (cause) {
      this.sqlite.close(false)
      throw cause
    }
  }

  private initialize(): void {
    const initialize = this.sqlite.transaction(() => {
      const hasMeta = this.sqlite.query<{ name: string }, []>('SELECT name FROM sqlite_master WHERE type = \'table\' AND name = \'meta\'').get()
      if (!hasMeta)
        this.sqlite.run(SCHEMA)
      this.sqlite.query('INSERT INTO meta(key, value) VALUES(\'schema_version\', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value').run(String(SCHEMA_VERSION))
    })
    initialize.immediate()
  }

  close(): void {
    this.sqlite.close(false)
  }
}

export function openTunnelDatabase(dataDirectory: string): TunnelDatabase {
  return new TunnelDatabase(path.join(dataDirectory, 'tunnel.sqlite'))
}
