package ent

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
	"entgo.io/ent/schema/field"
	nodemigrate "github.com/hackycy/hackycy-cli/ent/node/migrate"
	servermigrate "github.com/hackycy/hackycy-cli/ent/server/migrate"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestServerV1SQLMatchesEnt(t *testing.T) {
	db := createSchemaDatabase(t, "../pkg/cmd/tunnel/server/migrations/001_v1.sql")
	verifySchema(t, db, servermigrate.Tables)

	if _, err := db.Exec(`INSERT INTO nodes(node_id,kind,name,created_at,updated_at) VALUES('local','remote','bad','now','now')`); err == nil {
		t.Fatal("local Node kind CHECK was not enforced")
	}
	if _, err := db.Exec(`INSERT INTO remote_nodes(node_id,node_public_key,management_address,frp_bind_port,http_vhost_port,port_start,port_end,active_token) VALUES('missing','key','address',7000,8080,20000,29999,'token')`); err == nil {
		t.Fatal("remote Node foreign key was not enforced")
	}
	for _, statement := range []string{
		`INSERT INTO nodes(node_id,kind,name,created_at,updated_at) VALUES('local','local','Local','now','now')`,
		`INSERT INTO accounts(internal_id,kind,username,username_key,role,password_hash,created_at,updated_at) VALUES('owner','local','owner','owner','user','hash','now','now')`,
		`INSERT INTO clients(internal_id,owner_account_id,token,created_at) VALUES('client','owner','token','now')`,
		`INSERT INTO tunnels(id,client_internal_id,protocol,custom_domains,local_host,local_port,created_at,updated_at) VALUES('tunnel','client','http','example.test','localhost',8080,'now','now')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO tunnel_http_routes(id,tunnel_id,hostname,location) VALUES('route','tunnel','Example.test','/')`); err == nil {
		t.Fatal("uppercase hostname CHECK was not enforced")
	}
	var collation string
	if err := db.QueryRow(`SELECT coll FROM pragma_index_xinfo('tunnel_http_routes_host_location') WHERE key=1 AND seqno=0`).Scan(&collation); err != nil || collation != "NOCASE" {
		t.Fatalf("route hostname index collation = %q, %v", collation, err)
	}
	if servermigrate.TunnelHTTPRoutesColumns[1].Collation != "NOCASE" {
		t.Fatal("Ent route hostname collation is missing")
	}
}

func TestNodeV1SQLMatchesEnt(t *testing.T) {
	db := createSchemaDatabase(t, "../pkg/cmd/tunnel/node/migrations/001_v1.sql")
	verifySchema(t, db, nodemigrate.Tables)

	if _, err := db.Exec(`INSERT INTO identity(id,node_id,private_key,public_key) VALUES(1,'node',x'00',x'00')`); err == nil {
		t.Fatal("identity key length CHECK was not enforced")
	}
	if _, err := db.Exec(`INSERT INTO controller_binding(id,controller_public) VALUES(2,zeroblob(32))`); err == nil {
		t.Fatal("binding single-row CHECK was not enforced")
	}
	if _, err := db.Exec(`INSERT INTO runtime_state(id,highest_revision,applied_revision) VALUES(1,0,1)`); err == nil {
		t.Fatal("revision ordering CHECK was not enforced")
	}
	if _, err := db.Exec(`INSERT INTO runtime_state(id,owner_pid) VALUES(1,10)`); err == nil {
		t.Fatal("FRPS ownership group CHECK was not enforced")
	}
}

func createSchemaDatabase(t *testing.T, sqlPath string) *sql.DB {
	t.Helper()
	contents, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "state.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(contents)); err != nil {
		t.Fatalf("execute %s: %v", sqlPath, err)
	}
	return db
}

func verifySchema(t *testing.T, db *sql.DB, tables []*schema.Table) {
	t.Helper()
	var actualTables []string
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actualTables = append(actualTables, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var expectedTables []string
	for _, table := range tables {
		expectedTables = append(expectedTables, table.Name)
		verifyTable(t, db, table)
	}
	slices.Sort(expectedTables)
	if !slices.Equal(actualTables, expectedTables) {
		t.Fatalf("tables: SQL=%v Ent=%v", actualTables, expectedTables)
	}
}

func verifyTable(t *testing.T, db *sql.DB, table *schema.Table) {
	t.Helper()
	var createSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table.Name).Scan(&createSQL); err != nil {
		t.Fatal(err)
	}
	for _, check := range table.Annotation.Checks {
		if !strings.Contains(normalizeSQL(createSQL), normalizeSQL(check)) {
			t.Errorf("%s: missing CHECK %q", table.Name, check)
		}
	}
	type sqlColumn struct {
		columnType string
		notNull    bool
		primaryKey bool
	}
	columns := make(map[string]sqlColumn)
	rows, err := db.Query("PRAGMA table_info(" + table.Name + ")")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = sqlColumn{strings.ToUpper(columnType), notNull != 0, primaryKey != 0}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(columns) != len(table.Columns) {
		t.Errorf("%s: SQL has %d columns, Ent has %d", table.Name, len(columns), len(table.Columns))
	}
	for _, column := range table.Columns {
		actual, ok := columns[column.Name]
		if !ok {
			t.Errorf("%s: missing column %s", table.Name, column.Name)
			continue
		}
		var expectedType string
		switch column.Type {
		case field.TypeString, field.TypeEnum:
			expectedType = "TEXT"
		case field.TypeInt, field.TypeInt64, field.TypeBool:
			expectedType = "INTEGER"
		case field.TypeBytes:
			expectedType = "BLOB"
		default:
			t.Fatalf("%s.%s: unhandled Ent type %s", table.Name, column.Name, column.Type)
		}
		if actual.columnType != expectedType {
			t.Errorf("%s.%s: SQL type %s, Ent type %s", table.Name, column.Name, actual.columnType, expectedType)
		}
		if !actual.primaryKey && actual.notNull == column.Nullable {
			t.Errorf("%s.%s: SQL and Ent nullability differ", table.Name, column.Name)
		}
	}
	for _, index := range table.Indexes {
		var definition string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name=?`, index.Name).Scan(&definition); err != nil {
			t.Errorf("%s: missing index %s: %v", table.Name, index.Name, err)
			continue
		}
		if index.Unique && !strings.Contains(strings.ToUpper(definition), "CREATE UNIQUE INDEX") {
			t.Errorf("%s: index %s is not unique", table.Name, index.Name)
		}
		if index.Annotation != nil && !strings.Contains(normalizeSQL(definition), normalizeSQL(index.Annotation.Where)) {
			t.Errorf("%s: index %s has different predicate", table.Name, index.Name)
		}
		var actualColumns []string
		indexRows, err := db.Query("PRAGMA index_info(" + index.Name + ")")
		if err != nil {
			t.Fatal(err)
		}
		for indexRows.Next() {
			var position, columnID int
			var name string
			if err := indexRows.Scan(&position, &columnID, &name); err != nil {
				t.Fatal(err)
			}
			actualColumns = append(actualColumns, name)
		}
		if err := indexRows.Close(); err != nil {
			t.Fatal(err)
		}
		var expectedColumns []string
		for _, column := range index.Columns {
			expectedColumns = append(expectedColumns, column.Name)
		}
		if !slices.Equal(actualColumns, expectedColumns) {
			t.Errorf("%s: index %s columns SQL=%v Ent=%v", table.Name, index.Name, actualColumns, expectedColumns)
		}
	}
	actualForeignKeys := make(map[string]bool)
	fkRows, err := db.Query("PRAGMA foreign_key_list(" + table.Name + ")")
	if err != nil {
		t.Fatal(err)
	}
	for fkRows.Next() {
		var id, sequence int
		var target, from, to, onUpdate, onDelete, match string
		if err := fkRows.Scan(&id, &sequence, &target, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		actualForeignKeys[fmt.Sprintf("%s:%s:%s:%s", from, target, to, onDelete)] = true
	}
	if err := fkRows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(actualForeignKeys) != len(table.ForeignKeys) {
		t.Errorf("%s: SQL has %d foreign keys, Ent has %d", table.Name, len(actualForeignKeys), len(table.ForeignKeys))
	}
	for _, fk := range table.ForeignKeys {
		key := fmt.Sprintf("%s:%s:%s:%s", fk.Columns[0].Name, fk.RefTable.Name, fk.RefColumns[0].Name, fk.OnDelete)
		if !actualForeignKeys[key] {
			t.Errorf("%s: missing foreign key %s", table.Name, key)
		}
	}
}

func normalizeSQL(s string) string { return strings.Join(strings.Fields(s), " ") }
