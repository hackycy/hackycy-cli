package node

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	nodeent "github.com/hackycy/hackycy-cli/ent/node"
	"github.com/hackycy/hackycy-cli/internal/windowsacl"
)

//go:embed migrations/001_v1.sql
var nodeV1Schema string

// openEmptyNodeV1Database is called under the data directory's process lock.
// The returned Ent client borrows db; the caller closes db exactly once.
func openEmptyNodeV1Database(ctx context.Context, dataDirectory string) (*sql.DB, *nodeent.Client, error) {
	if strings.TrimSpace(dataDirectory) == "" {
		return nil, nil, fmt.Errorf("Node data directory is required")
	}
	dataDirectory, err := filepath.Abs(dataDirectory)
	if err != nil {
		return nil, nil, err
	}
	stateDirectory := filepath.Join(dataDirectory, "node-state-v1")
	if err := ensurePrivateDirectory(stateDirectory); err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(stateDirectory)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) != 0 {
		return nil, nil, fmt.Errorf("Node v1 state directory is not empty")
	}
	markerPath := filepath.Join(stateDirectory, nodeInitializedFile)
	marker, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create Node v1 initialization marker: %w", err)
	}
	if _, err := marker.WriteString("1\n"); err != nil {
		_ = marker.Close()
		return nil, nil, err
	}
	if err := marker.Sync(); err != nil {
		_ = marker.Close()
		return nil, nil, err
	}
	if err := marker.Close(); err != nil {
		return nil, nil, err
	}
	if err := windowsacl.RestrictPrivatePath(markerPath); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(stateDirectory, nodeDatabaseFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create Node v1 database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, nil, err
	}
	if err := windowsacl.RestrictPrivatePath(path); err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite3", nodeDatabaseURI(path))
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, nil, fmt.Errorf("configure Node v1 database: %w", err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, nodeV1Schema); err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		return nil, nil, fmt.Errorf("create Node v1 schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return db, nodeent.NewClient(nodeent.Driver(entsql.OpenDB(dialect.SQLite, db))), nil
}

func initializeNodeV1Identity(ctx context.Context, client *nodeent.Client) error {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	nodeID := make([]byte, 16)
	if _, err := rand.Read(nodeID); err != nil {
		return err
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Identity.Create().SetID(1).SetNodeID(hex.EncodeToString(nodeID)).SetPrivateKey(key.Bytes()).SetPublicKey(key.PublicKey().Bytes()).Save(ctx); err != nil {
		return err
	}
	if _, err := tx.RuntimeState.Create().SetID(1).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
