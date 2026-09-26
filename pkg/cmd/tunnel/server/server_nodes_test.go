package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func TestServerNodeRegistryRegisterIsAtomicAndUnconfigured(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	record, err := registry.register(context.Background(), id, "East Node", "http://127.0.0.1:7600", publicKey, 7)
	if err != nil {
		t.Fatal(err)
	}
	if record.DesiredRevision != 7 || record.DesiredSnapshot.Valid || record.AdvertisedFRPHost.Valid {
		t.Fatalf("re-added Node became configured: %+v", record)
	}
	saved, err := registry.get(context.Background(), id)
	if err != nil || saved.Name != "East Node" || saved.DesiredRevision != 7 || saved.DesiredSnapshot.Valid {
		t.Fatalf("saved Node = (%+v, %v)", saved, err)
	}
	otherKey := append([]byte(nil), publicKey...)
	otherKey[0] ^= 1
	for _, conflict := range []struct {
		name    string
		id      string
		address string
		key     []byte
	}{
		{name: "Node ID", id: id, address: "http://127.0.0.1:7601", key: otherKey},
		{name: "public key", id: "fedcba9876543210fedcba9876543210", address: "http://127.0.0.1:7601", key: publicKey},
		{name: "management address", id: "abcdef0123456789abcdef0123456789", address: "http://127.0.0.1:7600", key: otherKey},
	} {
		t.Run(conflict.name, func(t *testing.T) {
			_, err := registry.register(t.Context(), conflict.id, "Duplicate", conflict.address, conflict.key, 0)
			assertServerDomainCode(t, err, "NODE_ALREADY_REGISTERED")
		})
	}
	var count int
	if err := state.database.QueryRow(`SELECT count(*) FROM nodes WHERE kind='remote'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed registration left Node residue: count=%d, err=%v", count, err)
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM node_port_pools WHERE node_id=?`, id).Scan(&count); err != nil && err != sql.ErrNoRows || count != 1 {
		t.Fatalf("registered Node pool missing: count=%d, err=%v", count, err)
	}
}

func TestNodeRegistrationConstraintMappingRequiresMatchingRecord(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	id := "0123456789abcdef0123456789abcdef"
	key := make([]byte, 32)
	key[0] = 1
	address := "http://127.0.0.1:7600"
	if _, err := registry.register(ctx, id, "Remote", address, key, 0); err != nil {
		t.Fatal(err)
	}
	connection, err := state.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := serverEntOnConnection(connection)
	assertServerDomainCode(t, mapNodeRegistrationConstraintError(ctx, client, id, sqlite3.CONSTRAINT_PRIMARYKEY), "NODE_ALREADY_REGISTERED")
	assertServerDomainCode(t, mapRemoteNodeRegistrationConstraintError(ctx, client, "other", hex.EncodeToString(key), "other-address", sqlite3.CONSTRAINT_UNIQUE), "NODE_ALREADY_REGISTERED")
	assertServerDomainCode(t, mapRemoteNodeRegistrationConstraintError(ctx, client, "other", "other-key", address, sqlite3.CONSTRAINT_UNIQUE), "NODE_ALREADY_REGISTERED")

	for _, original := range []error{
		fmt.Errorf("nodes primary key unique: %w", sqlite3.CONSTRAINT_CHECK),
		sqlite3.CONSTRAINT_FOREIGNKEY,
		sqlite3.CONSTRAINT,
	} {
		for _, mapped := range []error{
			mapNodeRegistrationConstraintError(ctx, client, id, original),
			mapRemoteNodeRegistrationConstraintError(ctx, client, "other", hex.EncodeToString(key), address, original),
		} {
			var domain *ServerDomainError
			if errors.As(mapped, &domain) || !errors.Is(mapped, original) {
				t.Fatalf("mapped error = %v, want internal error preserving %v", mapped, original)
			}
		}
	}
	for _, mapped := range []error{
		mapNodeRegistrationConstraintError(ctx, client, "missing", sqlite3.CONSTRAINT_UNIQUE),
		mapRemoteNodeRegistrationConstraintError(ctx, client, "missing", "missing-key", "missing-address", sqlite3.CONSTRAINT_UNIQUE),
	} {
		var domain *ServerDomainError
		if errors.As(mapped, &domain) || !errors.Is(mapped, sqlite3.CONSTRAINT_UNIQUE) {
			t.Fatalf("unattributed unique error = %v, want internal error", mapped)
		}
	}
}
