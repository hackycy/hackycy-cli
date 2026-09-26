CREATE TABLE identity (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  node_id TEXT NOT NULL CHECK (length(node_id) = 32),
  private_key BLOB NOT NULL CHECK (length(private_key) = 32),
  public_key BLOB NOT NULL CHECK (length(public_key) = 32)
);

CREATE TABLE controller_binding (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  controller_public BLOB NOT NULL CHECK (length(controller_public) = 32)
);

CREATE TABLE runtime_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  highest_revision INTEGER NOT NULL DEFAULT 0 CHECK (highest_revision >= 0),
  highest_digest TEXT NOT NULL DEFAULT '',
  candidate BLOB,
  phase TEXT NOT NULL DEFAULT 'idle' CHECK (phase IN ('idle', 'accepted', 'switching', 'applied', 'disabling', 'disabled', 'failed', 'interrupted')),
  applied_revision INTEGER NOT NULL DEFAULT 0 CHECK (applied_revision BETWEEN 0 AND highest_revision),
  last_good BLOB,
  boot_disabled INTEGER NOT NULL DEFAULT 0 CHECK (boot_disabled IN (0, 1)),
  disabled_complete INTEGER NOT NULL DEFAULT 0 CHECK (disabled_complete IN (0, 1)),
  failure_code TEXT NOT NULL DEFAULT '',
  owner_pid INTEGER NOT NULL DEFAULT 0 CHECK (owner_pid >= 0),
  owner_create_time INTEGER NOT NULL DEFAULT 0 CHECK (owner_create_time >= 0),
  owner_binary TEXT NOT NULL DEFAULT '',
  owner_config TEXT NOT NULL DEFAULT '',
  CHECK (disabled_complete = 0 OR (boot_disabled = 1 AND last_good IS NULL)),
  CHECK ((owner_pid = 0 AND owner_create_time = 0 AND owner_binary = '' AND owner_config = '')
    OR (owner_pid > 0 AND owner_binary <> '' AND owner_config <> ''))
);
