package node

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	nodeent "github.com/hackycy/hackycy-cli/ent/node"
	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	_ "github.com/ncruces/go-sqlite3/driver"
)

const nodeDatabaseFile = "node.sqlite"
const nodeInitializedFile = "node.initialized"

type State struct {
	db        *sql.DB
	client    *nodeent.Client
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
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create Node data directory %s: %w", directory, err)
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
	stateDirectory := filepath.Join(directory, "node-state-v1")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create Node state directory %s: %w", stateDirectory, err)
	}
	entries, err := os.ReadDir(stateDirectory)
	if err != nil {
		return nil, err
	}
	var db *sql.DB
	var client *nodeent.Client
	var identity nodeV1Identity
	if len(entries) == 0 {
		db, client, err = openEmptyNodeV1Database(context.Background(), directory)
		if err != nil {
			return nil, err
		}
		if err = initializeNodeV1Identity(context.Background(), client); err != nil {
			_ = db.Close()
			return nil, err
		}
		row, readErr := client.Identity.Get(context.Background(), 1)
		if readErr != nil {
			_ = db.Close()
			return nil, readErr
		}
		identity = nodeV1Identity{nodeID: row.NodeID, private: row.PrivateKey, public: row.PublicKey}
	} else {
		db, client, identity, err = openExistingNodeV1Database(context.Background(), stateDirectory)
		if err != nil {
			return nil, err
		}
	}
	state := &State{db: db, client: client, lock: lock, directory: stateDirectory, nodeID: identity.nodeID, private: identity.private, public: identity.public}
	return state, nil
}

func nodeDatabaseURI(path string) string {
	if runtime.GOOS == "windows" {
		normalized := filepath.ToSlash(path)
		return "file:" + (&url.URL{Path: normalized}).EscapedPath()
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
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
	return claimNodeV1(ctx, state.client, controllerPublic)
}

func (state *State) ControllerMatches(ctx context.Context, controllerPublic []byte) (bool, error) {
	return controllerMatchesNodeV1(ctx, state.client, controllerPublic)
}

func (state *State) Close() error {
	if state == nil {
		return nil
	}
	return errors.Join(state.db.Close(), state.lock.Release())
}
