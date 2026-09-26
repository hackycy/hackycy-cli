package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"entgo.io/ent/dialect"
	entdialectsql "entgo.io/ent/dialect/sql"
	entschema "entgo.io/ent/dialect/sql/schema"
	"example.com/tunnel-ent-prototype/ent"
	"example.com/tunnel-ent-prototype/ent/account"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func main() {
	ctx := context.Background()
	path := filepath.Join(os.TempDir(), "tunnel-ent-prototype.sqlite")
	_ = os.Remove(path)
	database, err := sqlOpen(path)
	if err != nil {
		panic(err)
	}
	defer database.Close()

	driver := entdialectsql.OpenDB(dialect.SQLite, database)
	client := ent.NewClient(ent.Driver(driver))
	defer client.Close()
	if err := client.Schema.Create(ctx, entschema.WithForeignKeys(true)); err != nil {
		panic(fmt.Errorf("create schema: %w", err))
	}

	if err := verifyDDL(database); err != nil {
		panic(err)
	}
	if err := verifyEntWrites(ctx, client); err != nil {
		panic(err)
	}
	if err := verifyImmediateTransaction(ctx, database, client); err != nil {
		panic(err)
	}
	fmt.Printf("prototype passed: %s\n", path)
}

func sqlOpen(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite3", "file:"+path+"?_txlock=immediate")
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	return database, nil
}

func verifyDDL(database *sql.DB) error {
	var sqlText string
	if err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='allocations'`).Scan(&sqlText); err != nil {
		return fmt.Errorf("read allocation DDL: %w", err)
	}
	if !containsAll(sqlText, "CHECK", "port BETWEEN 1 AND 65535", "FOREIGN KEY") {
		return fmt.Errorf("allocation DDL does not contain model constraints: %s", sqlText)
	}
	var indexSQL string
	if err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='allocation_owner_id_port'`).Scan(&indexSQL); err != nil {
		return fmt.Errorf("read partial index DDL: %w", err)
	}
	if !containsAll(indexSQL, "UNIQUE", "WHERE enabled = 1") {
		return fmt.Errorf("partial unique index was not generated: %s", indexSQL)
	}
	return nil
}

func verifyEntWrites(ctx context.Context, client *ent.Client) error {
	if _, err := client.Node.Create().SetNodeID("node-1").SetControllerPublic("controller-key").SetHighestRevision(1).SetPhase("accepted").Save(ctx); err != nil {
		return fmt.Errorf("create Node state: %w", err)
	}
	if _, err := client.Node.Create().SetNodeID("node-invalid").SetHighestRevision(-1).Save(ctx); err == nil {
		return errors.New("Node state CHECK accepted a negative revision")
	}
	owner, err := client.Account.Create().SetName("owner").Save(ctx)
	if err != nil {
		return fmt.Errorf("create owner: %w", err)
	}
	if _, err := client.Allocation.Create().SetOwnerID(owner.ID).SetPort(0).Save(ctx); err == nil {
		return errors.New("model CHECK accepted an invalid port")
	}
	if _, err := client.Allocation.Create().SetOwnerID(owner.ID).SetPort(8080).Save(ctx); err != nil {
		return fmt.Errorf("create active allocation: %w", err)
	}
	if _, err := client.Allocation.Create().SetOwnerID(owner.ID + 1000).SetPort(8081).Save(ctx); err == nil {
		return errors.New("foreign key constraint accepted an unknown owner")
	}
	if _, err := client.Allocation.Create().SetOwnerID(owner.ID).SetPort(8080).Save(ctx); err == nil {
		return errors.New("partial unique index accepted a duplicate active port")
	}
	if _, err := client.Allocation.Create().SetOwnerID(owner.ID).SetPort(8080).SetEnabled(false).Save(ctx); err != nil {
		return fmt.Errorf("create disabled duplicate allocation: %w", err)
	}
	return nil
}

func verifyImmediateTransaction(ctx context.Context, database *sql.DB, client *ent.Client) error {
	owner, err := client.Account.Query().Where(account.NameEQ("owner")).Only(ctx)
	if err != nil {
		return fmt.Errorf("query owner before Ent transaction write: %w", err)
	}
	transaction, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("Ent transaction using _txlock=immediate: %w", err)
	}
	if _, err := transaction.Allocation.Create().SetOwnerID(owner.ID).SetPort(9090).Save(ctx); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("Ent transaction write: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("Ent transaction commit: %w", err)
	}

	connection, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}
	reservedDriver := entdialectsql.NewDriver(dialect.SQLite, entdialectsql.Conn{ExecQuerier: connection})
	reservedClient := ent.NewClient(ent.Driver(reservedDriver))
	if _, err := reservedClient.Allocation.Create().SetOwnerID(owner.ID).SetPort(10000).Save(ctx); err != nil {
		_, _ = connection.ExecContext(ctx, "ROLLBACK")
		return fmt.Errorf("Ent write on reserved connection: %w", err)
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit reserved connection transaction: %w", err)
	}

	// Ent's generated client owns a database/sql.DB-backed driver. This
	// short-lived client wraps the reserved *sql.Conn for the immediate path;
	// the client is deliberately not closed because it does not own the Conn.
	return nil
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
