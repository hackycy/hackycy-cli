# Ent + ncruces SQLite prototype

This directory is throwaway validation for
[验证 Ent ORM 的模型与事务](../../issues/03-orm-prototype.md). It is not production code.
Its Ent-created database is a test fixture; production schema creation and
upgrades must use versioned SQL migrations.

The schemas in `ent/schema/` model a minimal Node state, an owner, and an
allocation. The model generates:

- a cross-column `CHECK` for the port range;
- a Node revision `CHECK`;
- a partial unique index for active `(owner_id, port)` pairs;
- a required foreign key from allocation to owner.

`main.go` opens the existing `ncruces/go-sqlite3` driver, creates the empty
database through Ent, exercises valid and invalid writes, runs an Ent
transaction with `_txlock=immediate`, and wraps a reserved `*sql.Conn` with
`entgo.io/ent/dialect/sql.NewDriver` after an explicit `BEGIN IMMEDIATE`.

Run it with:

```sh
go run .
```

Regenerate the client with the pinned generator:

```sh
go run -mod=mod entgo.io/ent/cmd/ent generate ./ent/schema
```

Regeneration produced the same generated-source checksum on the validation
run. `CGO_ENABLED=0` builds passed for darwin/linux/windows on amd64 and arm64.

## Verdict

Ent handles the representative constraints and works with the current pure-Go
SQLite driver. This prototype does not validate SQL migrations or schema
parity with Ent models. A dedicated connection transaction needs a short-lived Ent
client backed by that connection; the client must not close the connection it
does not own. This is a local adapter boundary for Server's existing
`BEGIN IMMEDIATE` operations.
