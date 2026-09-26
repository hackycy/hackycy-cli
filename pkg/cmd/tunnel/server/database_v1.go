package server

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/internal/windowsacl"
)

//go:embed migrations/001_v1.sql
var serverV1Schema string

// openEmptyServerV1Database is called under the data directory's process lock.
// The returned Ent client borrows db; the caller closes db exactly once.
func openEmptyServerV1Database(ctx context.Context, dataDirectory string) (*sql.DB, *serverent.Client, error) {
	if strings.TrimSpace(dataDirectory) == "" {
		return nil, nil, fmt.Errorf("Server data directory is required")
	}
	dataDirectory, err := filepath.Abs(dataDirectory)
	if err != nil {
		return nil, nil, err
	}
	stateDirectory := filepath.Join(dataDirectory, "server-state-v1")
	if err := prepareEmptyServerV1Directory(stateDirectory); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(stateDirectory, databaseFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create Server v1 database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, nil, err
	}
	if err := windowsacl.RestrictPrivatePath(path); err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite3", databaseFileURI(path))
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, nil, fmt.Errorf("configure Server v1 database: %w", err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, serverV1Schema); err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		return nil, nil, fmt.Errorf("create Server v1 schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return db, serverent.NewClient(serverent.Driver(entsql.OpenDB(dialect.SQLite, db))), nil
}

func prepareEmptyServerV1Directory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create Server v1 state directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return fmt.Errorf("Server v1 state directory must be private and not a symlink")
	}
	if err := windowsacl.RestrictPrivatePath(path); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("Server v1 state directory is not empty")
	}
	return nil
}
