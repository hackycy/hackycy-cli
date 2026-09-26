package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
