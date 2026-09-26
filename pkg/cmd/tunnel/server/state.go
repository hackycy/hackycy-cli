package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/internal/filesession"
)

const databaseFileName = "tunnel.sqlite"

// State owns fresh-Go Tunnel persistence beneath one operator-selected base directory.
// It deliberately delegates session lifecycle to the shared filesession owner.
type State struct {
	sessions     *filesession.Manager
	database     *sql.DB
	client       *serverent.Client
	databasePath string
}

// StateOptions identifies a fresh-Go Tunnel state root.
type StateOptions struct {
	DataDirectory       string
	SessionIdleLifetime time.Duration
}

// OpenState creates or reopens only the Go-owned state below DataDirectory.
func OpenState(options StateOptions) (*State, error) {
	if strings.TrimSpace(options.DataDirectory) == "" {
		return nil, errors.New("tunnel state directory is required")
	}
	stateDirectory := filepath.Join(options.DataDirectory, "server-state-v1")
	entries, err := os.ReadDir(stateDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect Server v1 state directory: %w", err)
	}
	fresh := errors.Is(err, os.ErrNotExist) || len(entries) == 0
	if fresh {
		err = nil
	}
	databasePath := filepath.Join(stateDirectory, databaseFileName)
	var storedPublicKey string
	if !fresh {
		for _, name := range []string{databaseFileName, controllerKeyFileName, ".session-key"} {
			if _, err := os.Lstat(filepath.Join(stateDirectory, name)); err != nil {
				return nil, fmt.Errorf("Server v1 state is incomplete: %s: %w", name, err)
			}
		}
		storedPublicKey, err = inspectServerV1Database(context.Background(), databasePath)
	}
	if err != nil {
		return nil, err
	}
	sessions, err := filesession.Open(filesession.Options{
		BaseDirectory:      options.DataDirectory,
		StateDirectoryName: "server-state-v1",
		LockBaseDirectory:  true,
		IdleLifetime:       options.SessionIdleLifetime,
	})
	if err != nil {
		return nil, fmt.Errorf("open Tunnel sessions: %w", err)
	}
	publicKey, err := loadControllerPublicKey(sessions.Directory(), !fresh)
	if err != nil {
		_ = sessions.Close()
		return nil, err
	}
	if storedPublicKey != "" && storedPublicKey != publicKey {
		_ = sessions.Close()
		return nil, fmt.Errorf("Tunnel Controller identity does not match database; restore the original identity file")
	}
	databasePath = filepath.Join(sessions.Directory(), databaseFileName)
	var database *sql.DB
	var client *serverent.Client
	if fresh {
		database, client, err = createServerV1Database(context.Background(), sessions.Directory())
		if err == nil {
			err = initializeServerV1Identity(context.Background(), client, publicKey)
		}
	} else {
		database, err = openServerV1SQLDatabase(context.Background(), databasePath)
		if err == nil {
			client = serverent.NewClient(serverent.Driver(entsql.OpenDB(dialect.SQLite, database)))
		}
	}
	if err != nil {
		if database != nil {
			_ = database.Close()
		}
		_ = sessions.Close()
		return nil, err
	}
	return &State{
		sessions:     sessions,
		database:     database,
		client:       client,
		databasePath: databasePath,
	}, nil
}

// Close releases the database before the session lock.
func (state *State) Close() error {
	if state == nil {
		return nil
	}
	var closeErr error
	if state.database != nil {
		closeErr = errors.Join(closeErr, state.database.Close())
	}
	if state.sessions != nil {
		closeErr = errors.Join(closeErr, state.sessions.Close())
	}
	return closeErr
}
