package node

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

func inspectNodeV1Database(ctx context.Context, stateDirectory string) (nodeV1Identity, error) {
	marker, err := os.Lstat(filepath.Join(stateDirectory, nodeInitializedFile))
	if err != nil || !marker.Mode().IsRegular() || (runtime.GOOS != "windows" && marker.Mode().Perm()&0o077 != 0) {
		return nodeV1Identity{}, fmt.Errorf("Node v1 initialization marker is missing or invalid")
	}
	path := filepath.Join(stateDirectory, nodeDatabaseFile)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return nodeV1Identity{}, fmt.Errorf("Node v1 database is missing or invalid")
	}
	copyDirectory, err := os.MkdirTemp("", "node-v1-inspect-")
	if err != nil {
		return nodeV1Identity{}, err
	}
	defer os.RemoveAll(copyDirectory)
	if err := windowsacl.RestrictPrivatePath(copyDirectory); err != nil {
		return nodeV1Identity{}, err
	}
	copyPath := filepath.Join(copyDirectory, nodeDatabaseFile)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		sourcePath := path + suffix
		part, err := os.Lstat(sourcePath)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil || !part.Mode().IsRegular() || (runtime.GOOS != "windows" && part.Mode().Perm()&0o077 != 0) {
			return nodeV1Identity{}, fmt.Errorf("Node v1 database file is missing or invalid: %s", filepath.Base(sourcePath))
		}
		source, err := os.Open(sourcePath)
		if err != nil {
			return nodeV1Identity{}, err
		}
		target, err := os.OpenFile(copyPath+suffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, err = io.Copy(target, source)
			err = errors.Join(err, target.Close())
		}
		_ = source.Close()
		if err != nil {
			return nodeV1Identity{}, fmt.Errorf("copy Node v1 database: %w", err)
		}
	}
	copyDB, err := sql.Open("sqlite3", nodeDatabaseURI(copyPath))
	if err != nil {
		return nodeV1Identity{}, err
	}
	defer copyDB.Close()
	var integrity string
	if err := copyDB.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nodeV1Identity{}, fmt.Errorf("Node v1 database is damaged: %v", err)
	}
	if err := verifyNodeV1Schema(ctx, copyDB); err != nil {
		return nodeV1Identity{}, err
	}
	client := nodeent.NewClient(nodeent.Driver(entsql.OpenDB(dialect.SQLite, copyDB)))
	identities, err := client.Identity.Query().All(ctx)
	if err != nil || len(identities) != 1 || identities[0].ID != 1 || !validIdentity(identities[0].NodeID, identities[0].PrivateKey, identities[0].PublicKey) {
		return nodeV1Identity{}, fmt.Errorf("Node v1 identity is missing or damaged: %v", err)
	}
	bindings, err := client.ControllerBinding.Query().All(ctx)
	if err != nil || len(bindings) > 1 || (len(bindings) == 1 && (bindings[0].ID != 1 || len(bindings[0].ControllerPublic) != 32)) {
		return nodeV1Identity{}, fmt.Errorf("Node v1 Controller binding is damaged: %v", err)
	}
	runtimes, err := client.RuntimeState.Query().All(ctx)
	if err != nil || len(runtimes) != 1 || runtimes[0].ID != 1 {
		return nodeV1Identity{}, fmt.Errorf("Node v1 runtime state is missing or damaged: %v", err)
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

type nodeSchemaEntry struct{ kind, name, table, statement string }

func verifyNodeV1Schema(ctx context.Context, db *sql.DB) error {
	expected, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return err
	}
	defer expected.Close()
	if _, err := expected.ExecContext(ctx, nodeV1Schema); err != nil {
		return err
	}
	actualSchema, err := readNodeSchema(ctx, db)
	if err != nil {
		return err
	}
	expectedSchema, err := readNodeSchema(ctx, expected)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actualSchema, expectedSchema) {
		return fmt.Errorf("Node v1 database schema does not match the current SQL asset")
	}
	return nil
}

func readNodeSchema(ctx context.Context, db *sql.DB) ([]nodeSchemaEntry, error) {
	rows, err := db.QueryContext(ctx, "SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []nodeSchemaEntry
	for rows.Next() {
		var entry nodeSchemaEntry
		if err := rows.Scan(&entry.kind, &entry.name, &entry.table, &entry.statement); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
