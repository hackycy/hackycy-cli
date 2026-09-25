package server

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hackycy/hackycy-cli/internal/filesession"
)

const databaseFileName = "tunnel.sqlite"

// State owns fresh-Go Tunnel persistence beneath one operator-selected base directory.
// It deliberately delegates session lifecycle to the shared filesession owner.
type State struct {
	sessions     *filesession.Manager
	database     *sql.DB
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
	databasePath := filepath.Join(options.DataDirectory, "go-v1", databaseFileName)
	storedPublicKey, err := inspectExistingDatabase(databasePath)
	if err != nil {
		return nil, err
	}
	sessions, err := filesession.Open(filesession.Options{
		BaseDirectory: options.DataDirectory,
		IdleLifetime:  options.SessionIdleLifetime,
	})
	if err != nil {
		return nil, fmt.Errorf("open Tunnel sessions: %w", err)
	}
	publicKey, err := loadControllerPublicKey(sessions.Directory(), storedPublicKey != "")
	if err != nil {
		_ = sessions.Close()
		return nil, err
	}
	if storedPublicKey != "" && storedPublicKey != publicKey {
		_ = sessions.Close()
		return nil, fmt.Errorf("Tunnel Controller identity does not match database; restore the original identity file")
	}
	databasePath = filepath.Join(sessions.Directory(), databaseFileName)
	database, err := openDatabase(databasePath, publicKey)
	if err != nil {
		_ = sessions.Close()
		return nil, err
	}
	return &State{
		sessions:     sessions,
		database:     database,
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
