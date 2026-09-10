package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/filesession"
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
