package server

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/filesession"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestOpenStateCreatesFreshGoSessionAndSQLitePrimitives(t *testing.T) {
	baseDirectory := t.TempDir()
	unrelatedPath := filepath.Join(baseDirectory, "operator-note.txt")
	if err := os.WriteFile(unrelatedPath, []byte("operator managed"), 0o600); err != nil {
		t.Fatalf("write unrelated state: %v", err)
	}

	state, err := OpenState(StateOptions{DataDirectory: baseDirectory})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })

	wantDirectory := filepath.Join(baseDirectory, "server-state-v1")
	if state.sessions.Directory() != wantDirectory {
		t.Fatalf("session directory = %q, want %q", state.sessions.Directory(), wantDirectory)
	}
	if state.databasePath != filepath.Join(wantDirectory, databaseFileName) {
		t.Fatalf("database path = %q", state.databasePath)
	}
	if got, err := os.ReadFile(unrelatedPath); err != nil || string(got) != "operator managed" {
		t.Fatalf("unrelated state = (%q, %v), want unchanged", got, err)
	}

	assertDatabasePragmasAndSchema(t, state)
}

func TestOpenStateIgnoresOldAndUnknownSchemasWithoutChangingFiles(t *testing.T) {
	for _, version := range []string{"1", "2", "99"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "go-v1")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, databaseFileName)
			database, err := sql.Open("sqlite3", databaseFileURI(path))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`PRAGMA journal_mode = WAL`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(tunnelSchemaV1); err != nil {
				t.Fatal(err)
			}
			if version == "2" {
				if _, err := database.Exec(`ALTER TABLE clients ADD COLUMN desired_restart_generation INTEGER NOT NULL DEFAULT 0`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := database.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', ?)`, version); err != nil {
				t.Fatal(err)
			}
			before := databaseFileBytes(t, path)
			state, err := OpenState(StateOptions{DataDirectory: root})
			if err != nil {
				t.Fatalf("OpenState() read legacy database: %v", err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			assertDatabaseFilesUnchanged(t, path, before)
			_ = database.Close()
		})
	}
}

func TestOpenDatabaseRejectsUnknownWALWithoutChangingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), databaseFileName)
	database, err := sql.Open("sqlite3", databaseFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', '99')`); err != nil {
		t.Fatal(err)
	}
	before := databaseFileBytes(t, path)
	if opened, err := openDatabase(path, "test-controller-public-key"); err == nil {
		_ = opened.Close()
		t.Fatal("openDatabase() accepted unknown schema")
	}
	assertDatabaseFilesUnchanged(t, path, before)
}

func assertDatabaseFilesUnchanged(t *testing.T, path string, before map[string][]byte) {
	t.Helper()
	after := databaseFileBytes(t, path)
	if len(after) != len(before) {
		t.Fatalf("database file set changed: before %d, after %d", len(before), len(after))
	}
	for name, content := range before {
		if string(after[name]) != string(content) {
			t.Fatalf("%s changed during rejection", name)
		}
	}
}

func databaseFileBytes(t *testing.T, path string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		content, err := os.ReadFile(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		result[suffix] = content
	}
	return result
}

func TestOpenStateRestartsOnlyServerV1SessionAndSQLiteState(t *testing.T) {
	baseDirectory := t.TempDir()
	first, err := OpenState(StateOptions{DataDirectory: baseDirectory})
	if err != nil {
		t.Fatalf("first OpenState() error = %v", err)
	}
	revision, err := first.sessions.CredentialRevision("environment-admin\x00secret")
	if err != nil {
		t.Fatalf("CredentialRevision() error = %v", err)
	}
	session, err := first.sessions.Issue("environment-admin", revision)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := first.database.Exec(`INSERT INTO meta(key, value) VALUES('fresh_go_marker', 'present')`); err != nil {
		t.Fatalf("insert fresh Go marker: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := OpenState(StateOptions{DataDirectory: baseDirectory})
	if err != nil {
		t.Fatalf("second OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	resumed, err := second.sessions.Resume(session.Token, func(subject string) string {
		if subject != "environment-admin" {
			t.Fatalf("credential revision subject = %q", subject)
		}
		return revision
	})
	if err != nil || resumed == nil {
		t.Fatalf("Resume() = (%#v, %v), want fresh-Go session", resumed, err)
	}
	var marker string
	if err := second.database.QueryRow(`SELECT value FROM meta WHERE key = 'fresh_go_marker'`).Scan(&marker); err != nil || marker != "present" {
		t.Fatalf("fresh Go database marker = (%q, %v)", marker, err)
	}
}

func TestOpenStateControllerIdentityMismatchAndMissingDoNotChangeDatabase(t *testing.T) {
	for _, change := range []string{"missing", "mismatch"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			state, err := OpenState(StateOptions{DataDirectory: root})
			if err != nil {
				t.Fatal(err)
			}
			path := state.databasePath
			keyPath := filepath.Join(state.sessions.Directory(), controllerKeyFileName)
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			key, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			if change == "missing" {
				if err := os.Remove(keyPath); err != nil {
					t.Fatal(err)
				}
			} else {
				key[0] ^= 0xff
				if err := os.WriteFile(keyPath, key, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := databaseFileBytes(t, path)
			if reopened, err := OpenState(StateOptions{DataDirectory: root}); err == nil {
				_ = reopened.Close()
				t.Fatal("OpenState() accepted missing or mismatched Controller identity")
			}
			assertDatabaseFilesUnchanged(t, path, before)
		})
	}
}

func TestOpenStateRejectsMissingV1DatabaseWithoutReplacingIdentity(t *testing.T) {
	root := t.TempDir()
	first, err := OpenState(StateOptions{DataDirectory: root})
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(first.sessions.Directory(), controllerKeyFileName)
	keyBefore, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	path := first.databasePath
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	second, err := OpenState(StateOptions{DataDirectory: root})
	if err == nil {
		_ = second.Close()
		t.Fatal("OpenState() recreated missing v1 database")
	}
	keyAfter, err := os.ReadFile(keyPath)
	if err != nil || string(keyBefore) != string(keyAfter) {
		t.Fatalf("Controller identity changed after database recreation: %v", err)
	}
}

func TestOpenStateRejectsIncompleteOrDamagedV1WithoutRewritingDatabase(t *testing.T) {
	for _, change := range []string{"missing session key", "damaged database", "schema mismatch"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			state, err := OpenState(StateOptions{DataDirectory: root})
			if err != nil {
				t.Fatal(err)
			}
			path := state.databasePath
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing session key":
				err = os.Remove(filepath.Join(filepath.Dir(path), ".session-key"))
			case "damaged database":
				err = os.WriteFile(path, []byte("not a SQLite database"), 0o600)
			case "schema mismatch":
				var database *sql.DB
				database, err = sql.Open("sqlite3", databaseFileURI(path))
				if err == nil {
					_, err = database.Exec("DROP TABLE node_observations")
					_ = database.Close()
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before := databaseFileBytes(t, path)
			if reopened, err := OpenState(StateOptions{DataDirectory: root}); err == nil {
				_ = reopened.Close()
				t.Fatal("OpenState() accepted incomplete or damaged v1 state")
			}
			assertDatabaseFilesUnchanged(t, path, before)
		})
	}
}

func TestOpenStateRejectsCrossRecordV1InvariantsWithoutRewritingDatabase(t *testing.T) {
	for _, test := range []struct {
		name, mutation, want string
	}{
		{"Tunnel Client Node mismatch", `UPDATE tunnels SET node_id='remote' WHERE id='transport'`, "different Node"},
		{"occupied port outside pool", `UPDATE node_port_pools SET port_start=20001,port_end=20001 WHERE node_id='local'`, "outside its Node pool"},
		{"hostname split across Nodes", `
			INSERT INTO tunnels(id,client_internal_id,node_id,protocol,custom_domains,local_host,local_port,created_at,updated_at)
			VALUES('remote-http','remote-client','remote','http','["shared.example.test"]','localhost',8080,'now','now');
			INSERT INTO tunnel_http_routes(id,tunnel_id,hostname,location)
			VALUES('remote-route','remote-http','shared.example.test','/other')`, "split across Nodes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			state, err := OpenState(StateOptions{DataDirectory: root})
			if err != nil {
				t.Fatal(err)
			}
			path := state.databasePath
			_, err = state.database.Exec(`
				INSERT INTO nodes(node_id,kind,name,created_at,updated_at) VALUES('remote','remote','Remote','now','now');
				INSERT INTO node_port_pools(node_id,port_start,port_end) VALUES('local',20000,20001),('remote',20000,20001);
				INSERT INTO accounts(internal_id,kind,username,username_key,role,created_at,updated_at)
				VALUES('owner','environment','admin','admin','admin','now','now');
				INSERT INTO clients(internal_id,owner_account_id,node_id,token,created_at)
				VALUES('local-client','owner','local','local-token','now'),('remote-client','owner','remote','remote-token','now');
				INSERT INTO tunnels(id,client_internal_id,node_id,protocol,server_port,local_host,local_port,created_at,updated_at)
				VALUES('transport','local-client','local','tcp',20000,'localhost',8080,'now','now');
				INSERT INTO tunnels(id,client_internal_id,node_id,protocol,custom_domains,local_host,local_port,created_at,updated_at)
				VALUES('local-http','local-client','local','http','["shared.example.test"]','localhost',8080,'now','now');
				INSERT INTO tunnel_http_routes(id,tunnel_id,hostname,location)
				VALUES('local-route','local-http','shared.example.test','');
			`)
			if err == nil {
				_, err = state.database.Exec(test.mutation)
			}
			if err != nil {
				_ = state.Close()
				t.Fatal(err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			before := databaseFileBytes(t, path)
			opened, err := OpenState(StateOptions{DataDirectory: root})
			if err == nil {
				_ = opened.Close()
				t.Fatal("OpenState() accepted broken Server v1 invariant")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("OpenState() error = %v, want %q", err, test.want)
			}
			assertDatabaseFilesUnchanged(t, path, before)
		})
	}
}

func TestOpenStateIgnoresIncompleteLegacyV3WithoutChangingFiles(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "go-v1")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, databaseFileName)
	database, err := sql.Open("sqlite3", databaseFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO meta(key, value) VALUES('schema_version', '3')`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	before := databaseFileBytes(t, path)
	state, err := OpenState(StateOptions{DataDirectory: root})
	if err != nil {
		t.Fatalf("OpenState() read incomplete legacy database: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	assertDatabaseFilesUnchanged(t, path, before)
}

func TestOpenStateRejectsAnEmptyDirectory(t *testing.T) {
	_, err := OpenState(StateOptions{})
	if err == nil || errors.Is(err, filesession.ErrStorageUnavailable) {
		t.Fatalf("OpenState(empty) error = %v", err)
	}
}

func assertDatabasePragmasAndSchema(t *testing.T, state *State) {
	t.Helper()
	var foreignKeys int
	if err := state.database.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys = (%d, %v), want enabled", foreignKeys, err)
	}
	var journalMode string
	if err := state.database.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil || journalMode != "wal" {
		t.Fatalf("journal_mode = (%q, %v), want wal", journalMode, err)
	}
	var busyTimeout int
	if err := state.database.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil || busyTimeout != 5000 {
		t.Fatalf("busy_timeout = (%d, %v), want 5000", busyTimeout, err)
	}
	var version string
	if err := state.database.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil || version != serverV1SchemaVersion {
		t.Fatalf("schema version = (%q, %v), want %q", version, err, serverV1SchemaVersion)
	}
	for _, table := range []string{"meta", "nodes", "remote_nodes", "node_port_pools", "accounts", "clients", "tunnels", "tunnel_http_routes", "node_management_candidates", "node_observations"} {
		var found bool
		if err := state.database.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, table).Scan(&found); err != nil || !found {
			t.Fatalf("table %q exists = (%t, %v)", table, found, err)
		}
	}
}
