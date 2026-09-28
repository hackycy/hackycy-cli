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
	nodeent "github.com/hackycy/hackycy-cli/ent/tunnel/node"
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
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create Node state directory %s: %w", stateDirectory, err)
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
	path := filepath.Join(stateDirectory, nodeDatabaseFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create Node v1 database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, nil, err
	}
	db, client, err := openNodeV1Database(ctx, path)
	if err != nil {
		return nil, nil, err
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
	return db, client, nil
}

func openNodeV1Database(ctx context.Context, path string) (*sql.DB, *nodeent.Client, error) {
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

type nodeV1Identity struct {
	nodeID  string
	private []byte
	public  []byte
}

func openExistingNodeV1Database(ctx context.Context, stateDirectory string) (*sql.DB, *nodeent.Client, nodeV1Identity, error) {
	for _, name := range []string{nodeInitializedFile, nodeDatabaseFile} {
		path := filepath.Join(stateDirectory, name)
		if _, err := os.Stat(path); err != nil {
			return nil, nil, nodeV1Identity{}, fmt.Errorf("find existing Node state file %s: %w", path, err)
		}
	}
	path := filepath.Join(stateDirectory, nodeDatabaseFile)
	db, client, err := openNodeV1Database(ctx, path)
	if err != nil {
		return nil, nil, nodeV1Identity{}, err
	}
	identity, err := loadNodeV1Identity(ctx, client)
	if err != nil {
		_ = db.Close()
		return nil, nil, nodeV1Identity{}, err
	}
	return db, client, identity, nil
}

func loadNodeV1Identity(ctx context.Context, client *nodeent.Client) (nodeV1Identity, error) {
	identities, err := client.Identity.Query().All(ctx)
	if err != nil {
		return nodeV1Identity{}, fmt.Errorf("read Node v1 identity: %w", err)
	}
	if len(identities) != 1 || identities[0].ID != 1 || !validIdentity(identities[0].NodeID, identities[0].PrivateKey, identities[0].PublicKey) {
		return nodeV1Identity{}, fmt.Errorf("Node v1 identity is missing or invalid")
	}
	bindings, err := client.ControllerBinding.Query().All(ctx)
	if err != nil {
		return nodeV1Identity{}, fmt.Errorf("read Node v1 Controller binding: %w", err)
	}
	if len(bindings) > 1 || (len(bindings) == 1 && (bindings[0].ID != 1 || len(bindings[0].ControllerPublic) != 32)) {
		return nodeV1Identity{}, fmt.Errorf("Node v1 Controller binding is invalid")
	}
	runtimes, err := client.RuntimeState.Query().All(ctx)
	if err != nil {
		return nodeV1Identity{}, fmt.Errorf("read Node v1 runtime state: %w", err)
	}
	if len(runtimes) != 1 || runtimes[0].ID != 1 {
		return nodeV1Identity{}, fmt.Errorf("Node v1 runtime state is missing or invalid")
	}
	running := runtimes[0]
	var candidate, lastGood []byte
	if running.Candidate != nil {
		candidate = *running.Candidate
	}
	if running.LastGood != nil {
		lastGood = *running.LastGood
	}
	if running.HighestRevision < running.AppliedRevision || running.HighestRevision < 0 || running.OwnerPid < 0 || running.OwnerCreateTime < 0 || (running.OwnerPid == 0 && (running.OwnerCreateTime != 0 || running.OwnerBinary != "" || running.OwnerConfig != "")) || (running.OwnerPid > 0 && (running.OwnerBinary == "" || running.OwnerConfig == "")) || (running.DisabledComplete && (!running.BootDisabled || len(lastGood) != 0)) {
		return nodeV1Identity{}, fmt.Errorf("Node v1 runtime state is inconsistent")
	}
	if running.HighestRevision == 0 {
		if running.HighestDigest != "" || len(candidate) != 0 || running.AppliedRevision != 0 || len(lastGood) != 0 {
			return nodeV1Identity{}, fmt.Errorf("Node v1 runtime state is inconsistent")
		}
	} else {
		snapshot, err := decodeDesiredSnapshot(candidate, identities[0].NodeID)
		if err != nil || snapshot.Revision != running.HighestRevision || snapshotDigest(candidate) != running.HighestDigest {
			return nodeV1Identity{}, fmt.Errorf("Node v1 runtime snapshot is damaged: %v", err)
		}
	}
	if len(lastGood) != 0 {
		snapshot, err := decodeDesiredSnapshot(lastGood, identities[0].NodeID)
		if err != nil || snapshot.State != "running" || snapshot.Revision != running.AppliedRevision {
			return nodeV1Identity{}, fmt.Errorf("Node v1 last good snapshot is damaged: %v", err)
		}
	}
	return nodeV1Identity{nodeID: identities[0].NodeID, private: append([]byte(nil), identities[0].PrivateKey...), public: append([]byte(nil), identities[0].PublicKey...)}, nil
}
