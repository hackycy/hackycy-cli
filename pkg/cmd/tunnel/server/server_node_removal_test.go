package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestServerNodeRemovalRequiresNoClientDependenciesAndStagesDisabled(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	const nodeID = "0123456789abcdef0123456789abcdef"
	publicKey := make([]byte, 32)
	if _, err := registry.register(context.Background(), nodeID, "Remote", "http://127.0.0.1:7600", publicKey, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`
		INSERT INTO accounts(internal_id,kind,username,username_key,role,password_hash,created_at,updated_at)
		VALUES('owner','local','owner','owner','user','hash','now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`
		INSERT INTO clients(internal_id,owner_account_id,node_id,token,created_at)
		VALUES('client','owner',?, 'token','now')`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.requestNodeRemoval(context.Background(), nodeID); err == nil || !hasServerDomainCode(err, "NODE_HAS_CLIENTS") {
		t.Fatalf("removal with assigned Client = %v", err)
	}
	var lifecycle string
	if err := state.database.QueryRow(`SELECT lifecycle FROM nodes WHERE node_id=?`, nodeID).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "active" {
		t.Fatalf("failed removal changed lifecycle to %q", lifecycle)
	}
	if _, err := state.database.Exec(`UPDATE clients SET node_id='local',pending_node_id=? WHERE internal_id='client'`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.requestNodeRemoval(context.Background(), nodeID); err == nil || !hasServerDomainCode(err, "NODE_HAS_CLIENTS") {
		t.Fatalf("removal with pending Client = %v", err)
	}
	if _, err := state.database.Exec(`DELETE FROM clients WHERE internal_id='client'`); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.requestNodeRemoval(context.Background(), nodeID); err != nil {
		t.Fatal(err)
	}
	record, err := registry.get(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Lifecycle != "removing" || record.DesiredRevision != 5 || !record.DesiredSnapshot.Valid || record.DesiredHash.String == "" {
		t.Fatalf("removal state = %+v", record)
	}
	var snapshot serverDesiredNodeSnapshot
	if err := json.Unmarshal([]byte(record.DesiredSnapshot.String), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "disabled" || snapshot.NodeID != nodeID || snapshot.Revision != 5 || snapshot.Token != "" || snapshot.FRPVersion != tunnelruntime.FRPVersion {
		t.Fatalf("disabled snapshot = %+v", snapshot)
	}
	if revision, err := registry.requestNodeRemoval(context.Background(), nodeID); err != nil || revision != 5 {
		t.Fatalf("duplicate removal = (%d, %v)", revision, err)
	}
	if _, err := registry.requestNodeRemoval(context.Background(), "local"); err == nil || !hasServerDomainCode(err, "NODE_REMOVE_FORBIDDEN") {
		t.Fatalf("Local removal = %v", err)
	}
}

func hasServerDomainCode(err error, code string) bool {
	var domain *ServerDomainError
	return err != nil && errors.As(err, &domain) && domain.Code == code
}
