package node

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenEmptyNodeV1Database(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, nodeDatabaseFile)
	legacyContents := []byte("untouched legacy Node database")
	if err := os.WriteFile(legacyPath, legacyContents, 0o600); err != nil {
		t.Fatal(err)
	}
	db, client, err := openEmptyNodeV1Database(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	count, err := client.Identity.Query().Count(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("query empty v1 through Ent: count=%d err=%v", count, err)
	}
	if _, err := client.Identity.Create().SetID(1).SetNodeID(strings.Repeat("a", 32)).SetPrivateKey(make([]byte, 32)).SetPublicKey(make([]byte, 32)).Save(t.Context()); err != nil {
		t.Fatalf("write v1 through Ent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "node-state-v1", nodeInitializedFile)); err != nil {
		t.Fatalf("Node v1 initialization marker is missing: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openEmptyNodeV1Database(t.Context(), root); err == nil {
		t.Fatal("nonempty v1 directory was accepted")
	}
	got, err := os.ReadFile(legacyPath)
	if err != nil || string(got) != string(legacyContents) {
		t.Fatalf("legacy database changed: %q, %v", got, err)
	}
}

func TestInitializeNodeV1IdentityIsAtomic(t *testing.T) {
	root := t.TempDir()
	db, client, err := openEmptyNodeV1Database(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initializeNodeV1Identity(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	identity, err := client.Identity.Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !validIdentity(identity.NodeID, identity.PrivateKey, identity.PublicKey) {
		t.Fatalf("invalid initialized identity: %v", err)
	}
	runtime, err := client.RuntimeState.Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.HighestRevision != 0 || string(runtime.Phase) != "idle" {
		t.Fatalf("invalid initial runtime: %+v, %v", runtime, err)
	}
	if err := initializeNodeV1Identity(context.Background(), client); err == nil {
		t.Fatal("duplicate initialization succeeded")
	}
	identities, err := client.Identity.Query().Count(t.Context())
	if err != nil || identities != 1 {
		t.Fatalf("duplicate initialization changed identity count: %d, %v", identities, err)
	}
}

func TestInspectNodeV1DatabaseRejectsIncompleteStateWithoutRewriting(t *testing.T) {
	mutate := func(statement string) func(string) error {
		return func(root string) error {
			db, err := sql.Open("sqlite3", nodeDatabaseURI(filepath.Join(root, "node-state-v1", nodeDatabaseFile)))
			if err != nil {
				return err
			}
			_, err = db.Exec(statement)
			return errors.Join(err, db.Close())
		}
	}
	for name, damage := range map[string]func(string) error{
		"missing marker": func(root string) error {
			return os.Remove(filepath.Join(root, "node-state-v1", nodeInitializedFile))
		},
		"missing database": func(root string) error {
			return os.Remove(filepath.Join(root, "node-state-v1", nodeDatabaseFile))
		},
		"missing identity": mutate("DELETE FROM identity"),
		"missing runtime":  mutate("DELETE FROM runtime_state"),
		"schema mismatch":  mutate("DROP TABLE controller_binding"),
		"damaged snapshot": mutate("UPDATE runtime_state SET highest_revision=1, highest_digest='wrong', candidate='{}' WHERE id=1"),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			db, client, err := openEmptyNodeV1Database(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			if err := initializeNodeV1Identity(t.Context(), client); err != nil {
				t.Fatal(err)
			}
			stateDirectory := filepath.Join(root, "node-state-v1")
			if err := secureDatabaseFiles(filepath.Join(stateDirectory, nodeDatabaseFile)); err != nil {
				t.Fatal(err)
			}
			if _, err := inspectNodeV1Database(t.Context(), stateDirectory); err != nil {
				t.Fatalf("valid state rejected: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := damage(root); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(stateDirectory, nodeDatabaseFile)
			before, readErr := os.ReadFile(path)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if _, err := inspectNodeV1Database(t.Context(), stateDirectory); err == nil {
				t.Fatal("incomplete state was accepted")
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejection rewrote original database")
			}
		})
	}
}
