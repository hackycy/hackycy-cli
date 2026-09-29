package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestOpenEmptyServerV1Database(t *testing.T) {
	root := t.TempDir()
	legacyDirectory := filepath.Join(root, "go-v1")
	if err := os.Mkdir(legacyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDirectory, databaseFileName)
	legacyContents := []byte("untouched legacy database")
	if err := os.WriteFile(legacyPath, legacyContents, 0o600); err != nil {
		t.Fatal(err)
	}
	db, client, err := openEmptyServerV1Database(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	count, err := client.Node.Query().Count(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("query empty v1 through Ent: count=%d err=%v", count, err)
	}
	if _, err := client.Node.Create().SetID("local").SetKind("local").SetName("Local").SetCreatedAt("now").SetUpdatedAt("now").Save(t.Context()); err != nil {
		t.Fatalf("write v1 through Ent: %v", err)
	}
	var initialized bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_observations')`).Scan(&initialized); err != nil || !initialized {
		t.Fatalf("v1 schema is incomplete: initialized=%t err=%v", initialized, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openEmptyServerV1Database(t.Context(), root); err == nil {
		t.Fatal("nonempty v1 directory was accepted")
	}
	got, err := os.ReadFile(legacyPath)
	if err != nil || string(got) != string(legacyContents) {
		t.Fatalf("legacy database changed: %q, %v", got, err)
	}
}

func TestMigrateServerV1DatabaseConvertsLegacyNode404Policy(t *testing.T) {
	db, err := sql.Open("sqlite3", databaseFileURI(filepath.Join(t.TempDir(), databaseFileName)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(serverV1Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', '1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO nodes(node_id, kind, name, created_at, updated_at) VALUES('local', 'local', 'Local', 'now', 'now'), ('remote', 'remote', 'Remote', 'now', 'now'), ('empty', 'remote', 'Empty', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO remote_nodes(node_id, node_public_key, management_address, frp_bind_port, http_vhost_port, port_start, port_end, desired_snapshot, active_token) VALUES('remote', 'key', 'address', 7000, 8080, 20000, 20100, ?, 'token')`, `{"custom404Page":"<main>custom</main>"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO remote_nodes(node_id, node_public_key, management_address, frp_bind_port, http_vhost_port, port_start, port_end, desired_snapshot, active_token) VALUES('empty', 'other-key', 'other-address', 7000, 8080, 20000, 20100, ?, 'token')`, `{"custom404Page":""}`); err != nil {
		t.Fatal(err)
	}
	if err := migrateServerV1Database(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var version, policy string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil || version != serverV1SchemaVersion {
		t.Fatalf("schema version = %q, %v", version, err)
	}
	if err := db.QueryRow(`SELECT desired_policy FROM remote_nodes WHERE node_id='remote'`).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	decoded, err := parseNodeConfigurationPolicy(policy)
	if err != nil || decoded.Fields["custom404Page"].Mode != "custom" || decoded.Fields["custom404Page"].Value != "<main>custom</main>" {
		t.Fatalf("migrated policy = (%+v, %v)", decoded, err)
	}
	if err := db.QueryRow(`SELECT desired_policy FROM remote_nodes WHERE node_id='empty'`).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	decoded, err = parseNodeConfigurationPolicy(policy)
	if err != nil || decoded.Fields["custom404Page"].Mode != "inherit" {
		t.Fatalf("migrated empty policy = (%+v, %v)", decoded, err)
	}
	if err := migrateServerV1Database(t.Context(), db); err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_migrations SET checksum='drift' WHERE version=2`); err != nil {
		t.Fatal(err)
	}
	if err := migrateServerV1Database(t.Context(), db); err == nil {
		t.Fatal("changed migration checksum was accepted")
	}
}

func TestMigrateServerV1DatabaseRollsBackFailedMigration(t *testing.T) {
	db, err := sql.Open("sqlite3", databaseFileURI(filepath.Join(t.TempDir(), databaseFileName)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO meta(key, value) VALUES('schema_version', '1')`); err != nil {
		t.Fatal(err)
	}
	if err := migrateServerV1Database(t.Context(), db); err == nil {
		t.Fatal("migration unexpectedly succeeded against an incomplete schema")
	}
	var version string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil || version != "1" {
		t.Fatalf("schema version after failed migration = %q, %v", version, err)
	}
	var columns int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('remote_nodes')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatalf("failed migration created remote_nodes columns: %d", columns)
	}
}
