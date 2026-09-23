package server

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
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

	wantDirectory := filepath.Join(baseDirectory, "go-v1")
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

func TestOpenStateRejectsOldAndUnknownSchemasWithoutChangingFiles(t *testing.T) {
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
			if state, err := OpenState(StateOptions{DataDirectory: root}); err == nil {
				_ = state.Close()
				t.Fatal("OpenState() accepted incompatible database")
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

func TestOpenStateRestartsOnlyFreshGoSessionAndSQLiteState(t *testing.T) {
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

func TestOpenStateReusesControllerIdentityWhenOnlyDatabaseIsLost(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	keyAfter, err := os.ReadFile(keyPath)
	if err != nil || string(keyBefore) != string(keyAfter) {
		t.Fatalf("Controller identity changed after database recreation: %v", err)
	}
}

func TestOpenStateRejectsIncompleteV3WithoutChangingFiles(t *testing.T) {
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
	if state, err := OpenState(StateOptions{DataDirectory: root}); err == nil {
		_ = state.Close()
		t.Fatal("OpenState() accepted incomplete v3")
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
	if err := state.database.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("schema version = (%q, %v), want %q", version, err, schemaVersion)
	}
	for _, table := range []string{"meta", "accounts", "clients", "tunnels", "tunnel_http_routes"} {
		var found bool
		if err := state.database.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, table).Scan(&found); err != nil || !found {
			t.Fatalf("table %q exists = (%t, %v)", table, found, err)
		}
	}
}
