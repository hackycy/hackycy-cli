package node

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"github.com/hackycy/hackycy-cli/internal/windowsacl"
	_ "github.com/ncruces/go-sqlite3/driver"
)

const nodeDatabaseFile = "node.sqlite"
const nodeInitializedFile = "node.initialized"
const nodeSchemaVersion = "4"
const previousNodeSchemaVersion = "3"

type State struct {
	db        *sql.DB
	lock      *tunnelruntime.StateDirectoryLock
	directory string
	private   []byte
	public    []byte
	nodeID    string
}

func OpenState(directory string) (_ *State, err error) {
	if strings.TrimSpace(directory) == "" {
		return nil, fmt.Errorf("Node state directory is required")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(directory); err != nil {
		return nil, err
	}
	lock, err := tunnelruntime.AcquireStateDirectoryLock(directory)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = lock.Release()
		}
	}()
	path := filepath.Join(directory, nodeDatabaseFile)
	exists, err := inspectNodeDatabase(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		entries, readErr := os.ReadDir(directory)
		if readErr != nil {
			return nil, readErr
		}
		for _, entry := range entries {
			if entry.Name() != ".lock" {
				return nil, fmt.Errorf("Node state directory contains %s without a database; inspect or use an empty directory", entry.Name())
			}
		}
		marker, createErr := os.OpenFile(filepath.Join(directory, nodeInitializedFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			return nil, fmt.Errorf("create Node initialization marker: %w", createErr)
		}
		if _, writeErr := marker.Write([]byte("1\n")); writeErr != nil {
			_ = marker.Close()
			return nil, writeErr
		}
		if syncErr := marker.Sync(); syncErr != nil {
			_ = marker.Close()
			return nil, syncErr
		}
		if closeErr := marker.Close(); closeErr != nil {
			return nil, closeErr
		}
		if err := windowsacl.RestrictPrivatePath(filepath.Join(directory, nodeInitializedFile)); err != nil {
			return nil, err
		}
		if runtime.GOOS != "windows" {
			directoryHandle, openErr := os.Open(directory)
			if openErr != nil {
				return nil, openErr
			}
			syncErr := directoryHandle.Sync()
			_ = directoryHandle.Close()
			if syncErr != nil {
				return nil, syncErr
			}
		}
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			return nil, fmt.Errorf("create private Node database: %w", createErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, closeErr
		}
	} else {
		marker, markerErr := os.Lstat(filepath.Join(directory, nodeInitializedFile))
		if markerErr != nil || !marker.Mode().IsRegular() || (runtime.GOOS != "windows" && marker.Mode().Perm()&0o077 != 0) {
			return nil, fmt.Errorf("Node initialization marker is missing or invalid; inspect the existing directory")
		}
	}
	uri := nodeDatabaseURI(path)
	db, err := sql.Open("sqlite3", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	state := &State{db: db, lock: lock, directory: directory}
	if err = state.initialize(exists); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = secureDatabaseFiles(path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return state, nil
}

func secureDatabaseFiles(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		file := path + suffix
		info, err := os.Lstat(file)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("Node database file must be regular: %s", filepath.Base(file))
		}
		if err := os.Chmod(file, 0o600); err != nil {
			return fmt.Errorf("protect Node database file %s: %w", filepath.Base(file), err)
		}
		if err := windowsacl.RestrictPrivatePath(file); err != nil {
			return fmt.Errorf("protect Node database ACL %s: %w", filepath.Base(file), err)
		}
	}
	return nil
}

func nodeDatabaseURI(path string) string {
	if runtime.GOOS == "windows" {
		normalized := filepath.ToSlash(path)
		return "file:" + (&url.URL{Path: normalized}).EscapedPath()
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func ensurePrivateDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create Node state directory: %w", err)
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return fmt.Errorf("inspect Node state directory: %w", err)
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return fmt.Errorf("Node state directory must be private and must not be a symlink")
	}
	return windowsacl.RestrictPrivatePath(directory)
}

func inspectNodeDatabase(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return false, fmt.Errorf("Node database must be a private regular file")
	}
	copyDir, err := os.MkdirTemp("", "node-db-inspect-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(copyDir)
	if err := windowsacl.RestrictPrivatePath(copyDir); err != nil {
		return false, err
	}
	copyPath := filepath.Join(copyDir, nodeDatabaseFile)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if suffix != "" {
			sidecar, statErr := os.Lstat(path + suffix)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			if statErr != nil || !sidecar.Mode().IsRegular() {
				return false, fmt.Errorf("Node database sidecar must be a regular file")
			}
		}
		source, openErr := os.Open(path + suffix)
		if openErr != nil {
			return false, fmt.Errorf("inspect Node database: %w", openErr)
		}
		target, createErr := os.OpenFile(copyPath+suffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr == nil {
			_, createErr = io.Copy(target, source)
			createErr = errors.Join(createErr, target.Close())
		}
		_ = source.Close()
		if createErr != nil {
			return false, fmt.Errorf("inspect Node database: %w", createErr)
		}
	}
	copyURI := nodeDatabaseURI(copyPath)
	copyDB, err := sql.Open("sqlite3", copyURI)
	if err != nil {
		return false, err
	}
	defer copyDB.Close()
	var version, integrity string
	if err := copyDB.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil || (version != nodeSchemaVersion && version != previousNodeSchemaVersion) {
		return false, fmt.Errorf("Node database schema is unknown or damaged; inspect the existing directory")
	}
	if err := copyDB.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return false, fmt.Errorf("Node database is damaged; inspect the existing directory")
	}
	var private, public []byte
	var nodeID string
	if err := copyDB.QueryRow(`SELECT node_id, private_key, public_key FROM identity WHERE id=1`).Scan(&nodeID, &private, &public); err != nil || !validIdentity(nodeID, private, public) {
		return false, fmt.Errorf("Node database identity is missing or damaged; inspect the existing directory")
	}
	if _, err := copyDB.Exec(`SELECT controller_public FROM binding WHERE id=1`); err != nil {
		return false, fmt.Errorf("Node database binding schema is damaged; inspect the existing directory")
	}
	var running runtimeRecord
	runtimeQuery := `SELECT highest_revision, highest_digest, candidate, phase, applied_revision, last_good, boot_disabled, disabled_complete, failure_code, owner_pid, 0, owner_started, owner_binary, owner_config FROM node_runtime WHERE id=1`
	if version == nodeSchemaVersion {
		runtimeQuery = `SELECT highest_revision, highest_digest, candidate, phase, applied_revision, last_good, boot_disabled, disabled_complete, failure_code, owner_pid, owner_create_time, owner_started, owner_binary, owner_config FROM node_runtime WHERE id=1`
	}
	if err := copyDB.QueryRow(runtimeQuery).Scan(
		&running.HighestRevision, &running.HighestDigest, &running.Candidate, &running.Phase, &running.AppliedRevision, &running.LastGood, &running.BootDisabled, &running.DisabledComplete, &running.FailureCode, &running.OwnerPID, &running.OwnerCreateTime, &running.OwnerStarted, &running.OwnerBinary, &running.OwnerConfig); err != nil {
		return false, fmt.Errorf("Node database runtime schema is damaged; inspect the existing directory")
	}
	if running.HighestRevision < running.AppliedRevision || running.HighestRevision < 0 || running.OwnerPID < 0 || running.OwnerCreateTime < 0 || (running.OwnerPID == 0 && (running.OwnerCreateTime != 0 || running.OwnerStarted != "" || running.OwnerBinary != "" || running.OwnerConfig != "")) || (running.OwnerPID > 0 && (running.OwnerBinary == "" || running.OwnerConfig == "" || (version == previousNodeSchemaVersion && running.OwnerStarted == ""))) || (running.DisabledComplete && (!running.BootDisabled || len(running.LastGood) != 0)) {
		return false, fmt.Errorf("Node database runtime state is inconsistent; inspect the existing directory")
	}
	if running.HighestRevision == 0 {
		if running.HighestDigest != "" || len(running.Candidate) != 0 || running.AppliedRevision != 0 || len(running.LastGood) != 0 {
			return false, fmt.Errorf("Node database runtime state is inconsistent; inspect the existing directory")
		}
	} else {
		snapshot, err := decodeDesiredSnapshot(running.Candidate, nodeID)
		if err != nil || snapshot.Revision != running.HighestRevision || snapshotDigest(running.Candidate) != running.HighestDigest {
			return false, fmt.Errorf("Node database runtime snapshot is damaged; inspect the existing directory")
		}
	}
	if len(running.LastGood) != 0 {
		snapshot, err := decodeDesiredSnapshot(running.LastGood, nodeID)
		if err != nil || snapshot.State != "running" || snapshot.Revision != running.AppliedRevision {
			return false, fmt.Errorf("Node database last good snapshot is damaged; inspect the existing directory")
		}
	}
	return true, nil
}

func (state *State) initialize(exists bool) error {
	for _, statement := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000"} {
		if _, err := state.db.Exec(statement); err != nil {
			return fmt.Errorf("configure Node database: %w", err)
		}
	}
	if !exists {
		key, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		nodeIDBytes := make([]byte, 16)
		if _, err := rand.Read(nodeIDBytes); err != nil {
			return err
		}
		transaction, err := state.db.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer transaction.Rollback()
		for _, statement := range []string{
			`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
			`CREATE TABLE identity (id INTEGER PRIMARY KEY CHECK(id=1), node_id TEXT NOT NULL, private_key BLOB NOT NULL, public_key BLOB NOT NULL)`,
			`CREATE TABLE binding (id INTEGER PRIMARY KEY CHECK(id=1), controller_public BLOB NOT NULL CHECK(length(controller_public)=32))`,
			`CREATE TABLE node_runtime (id INTEGER PRIMARY KEY CHECK(id=1), highest_revision INTEGER NOT NULL DEFAULT 0, highest_digest TEXT NOT NULL DEFAULT '', candidate BLOB, phase TEXT NOT NULL DEFAULT 'idle', applied_revision INTEGER NOT NULL DEFAULT 0, last_good BLOB, boot_disabled INTEGER NOT NULL DEFAULT 0, disabled_complete INTEGER NOT NULL DEFAULT 0, failure_code TEXT NOT NULL DEFAULT '', owner_pid INTEGER NOT NULL DEFAULT 0, owner_create_time INTEGER NOT NULL DEFAULT 0, owner_started TEXT NOT NULL DEFAULT '', owner_binary TEXT NOT NULL DEFAULT '', owner_config TEXT NOT NULL DEFAULT '')`,
			`INSERT INTO node_runtime(id) VALUES(1)`,
		} {
			if _, err := transaction.Exec(statement); err != nil {
				return fmt.Errorf("create Node schema: %w", err)
			}
		}
		if _, err := transaction.Exec(`INSERT INTO meta(key,value) VALUES('schema_version',?)`, nodeSchemaVersion); err != nil {
			return err
		}
		if _, err := transaction.Exec(`INSERT INTO identity(id,node_id,private_key,public_key) VALUES(1,?,?,?)`, hex.EncodeToString(nodeIDBytes), key.Bytes(), key.PublicKey().Bytes()); err != nil {
			return err
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit Node identity: %w", err)
		}
	} else if err := state.migrateExistingSchema(); err != nil {
		return err
	}
	if err := state.db.QueryRow(`SELECT node_id,private_key,public_key FROM identity WHERE id=1`).Scan(&state.nodeID, &state.private, &state.public); err != nil || !validIdentity(state.nodeID, state.private, state.public) {
		return fmt.Errorf("read persisted Node identity: %w", err)
	}
	return nil
}

func (state *State) migrateExistingSchema() error {
	var version string
	if err := state.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
		return fmt.Errorf("read Node schema version: %w", err)
	}
	if version == nodeSchemaVersion {
		return nil
	}
	if version != previousNodeSchemaVersion {
		return fmt.Errorf("Node database schema version %q is unsupported", version)
	}
	transaction, err := state.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin Node schema migration: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`ALTER TABLE node_runtime ADD COLUMN owner_create_time INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("migrate Node runtime schema: %w", err)
	}
	if _, err := transaction.Exec(`UPDATE meta SET value=? WHERE key='schema_version'`, nodeSchemaVersion); err != nil {
		return fmt.Errorf("update Node schema version: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit Node schema migration: %w", err)
	}
	return nil
}

func validIdentity(nodeID string, private, public []byte) bool {
	if len(nodeID) != 32 || len(private) != 32 || len(public) != 32 {
		return false
	}
	if _, err := hex.DecodeString(nodeID); err != nil {
		return false
	}
	key, err := ecdh.X25519().NewPrivateKey(private)
	return err == nil && string(key.PublicKey().Bytes()) == string(public)
}

func (state *State) Fingerprint() string {
	hash := sha256.Sum256(state.public)
	return "SHA256:" + base64.RawURLEncoding.EncodeToString(hash[:])
}

func (state *State) Claim(ctx context.Context, controllerPublic []byte) (bool, error) {
	if len(controllerPublic) != 32 {
		return false, fmt.Errorf("invalid Controller identity")
	}
	transaction, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `INSERT OR IGNORE INTO binding(id,controller_public) VALUES(1,?)`, controllerPublic)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	return inserted == 1, nil
}

func (state *State) ControllerMatches(ctx context.Context, controllerPublic []byte) (bool, error) {
	var stored []byte
	if err := state.db.QueryRowContext(ctx, `SELECT controller_public FROM binding WHERE id=1`).Scan(&stored); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return bytes.Equal(stored, controllerPublic), nil
}

func (state *State) Close() error {
	if state == nil {
		return nil
	}
	return errors.Join(state.db.Close(), state.lock.Release())
}
