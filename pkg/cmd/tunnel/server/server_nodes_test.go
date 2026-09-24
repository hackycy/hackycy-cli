package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"testing"
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
	if _, err := registry.register(context.Background(), "fedcba9876543210fedcba9876543210", "Duplicate address", "http://127.0.0.1:7600", publicKey, 0); err == nil {
		t.Fatal("duplicate identity/address was accepted")
	}
	var count int
	if err := state.database.QueryRow(`SELECT count(*) FROM nodes WHERE kind='remote'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed registration left Node residue: count=%d, err=%v", count, err)
	}
	if err := state.database.QueryRow(`SELECT count(*) FROM node_port_pools WHERE node_id=?`, id).Scan(&count); err != nil && err != sql.ErrNoRows || count != 1 {
		t.Fatalf("registered Node pool missing: count=%d, err=%v", count, err)
	}
}
