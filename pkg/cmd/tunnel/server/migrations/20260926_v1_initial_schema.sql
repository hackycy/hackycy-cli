CREATE TABLE meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

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
  CHECK ((node_id = 'local') = (kind = 'local')),
  CHECK (node_id <> 'local' OR lifecycle = 'active')
);
CREATE UNIQUE INDEX nodes_one_local ON nodes(kind) WHERE kind = 'local';
CREATE UNIQUE INDEX nodes_frp_endpoint ON nodes(advertised_frp_host, advertised_frp_port)
  WHERE lifecycle = 'active' AND advertised_frp_host IS NOT NULL;

CREATE TABLE remote_nodes (
  id INTEGER PRIMARY KEY,
  node_id TEXT NOT NULL UNIQUE REFERENCES nodes(node_id) ON DELETE CASCADE,
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
  staged_token_revision INTEGER CHECK (staged_token_revision IS NULL OR staged_token_revision >= 0)
);

CREATE TABLE node_port_pools (
  id INTEGER PRIMARY KEY,
  node_id TEXT NOT NULL UNIQUE REFERENCES nodes(node_id) ON DELETE CASCADE,
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
  rotated_at TEXT
);
CREATE INDEX clients_by_owner ON clients(owner_account_id, created_at, internal_id);

CREATE TABLE tunnels (
  id TEXT PRIMARY KEY,
  client_internal_id TEXT NOT NULL REFERENCES clients(internal_id) ON DELETE CASCADE,
  node_id TEXT NOT NULL DEFAULT 'local' REFERENCES nodes(node_id) ON DELETE RESTRICT,
  label TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 100),
  protocol TEXT NOT NULL CHECK (protocol IN ('http', 'tcp', 'udp')),
  custom_domains TEXT,
  location TEXT,
  server_port INTEGER CHECK (server_port IS NULL OR server_port BETWEEN 1 AND 65535),
  local_host TEXT NOT NULL,
  local_port INTEGER NOT NULL CHECK (local_port BETWEEN 1 AND 65535),
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  options_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(options_json)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK ((protocol = 'http' AND custom_domains IS NOT NULL AND server_port IS NULL)
    OR (protocol IN ('tcp', 'udp') AND custom_domains IS NULL AND location IS NULL AND server_port IS NOT NULL))
);
CREATE UNIQUE INDEX tunnels_unique_transport_port ON tunnels(node_id, protocol, server_port)
  WHERE protocol IN ('tcp', 'udp');
CREATE INDEX tunnels_by_client ON tunnels(client_internal_id, created_at, id);

CREATE TABLE tunnel_http_routes (
  id TEXT PRIMARY KEY,
  tunnel_id TEXT NOT NULL REFERENCES tunnels(id) ON DELETE CASCADE,
  hostname TEXT NOT NULL COLLATE NOCASE CHECK (hostname COLLATE BINARY = lower(hostname)),
  location TEXT NOT NULL
);
CREATE UNIQUE INDEX tunnel_http_routes_host_location ON tunnel_http_routes(hostname, location);

CREATE TABLE node_management_candidates (
  id INTEGER PRIMARY KEY,
  node_id TEXT NOT NULL UNIQUE REFERENCES nodes(node_id) ON DELETE CASCADE,
  candidate_address TEXT NOT NULL,
  failure_code TEXT NOT NULL DEFAULT ''
);

CREATE TABLE node_observations (
  id INTEGER PRIMARY KEY,
  node_id TEXT NOT NULL UNIQUE REFERENCES nodes(node_id) ON DELETE CASCADE,
  status_json TEXT,
  status_observed_at TEXT,
  failure_code TEXT NOT NULL DEFAULT '',
  attempted_at TEXT NOT NULL
);
