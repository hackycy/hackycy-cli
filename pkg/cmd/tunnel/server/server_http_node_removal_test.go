package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerHTTPNodeRemovalRequiresAdminOriginAndNoClients(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatal(err)
	}
	sessions := openServerSessions(t, accounts, state)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := newServerNodeObservations(state.database)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := newServerNodeCoordinator(state.sessions.Directory(), registry, observations)
	if err != nil {
		t.Fatal(err)
	}
	service := newServerNodeService(registry, observations, coordinator)
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: openServerControlPlane(t, state), Nodes: service})
	if err != nil {
		t.Fatal(err)
	}
	const nodeID = "0123456789abcdef0123456789abcdef"
	if _, err := registry.register(ctx, nodeID, "Remote", "http://127.0.0.1:7600", make([]byte, 32), 0); err != nil {
		t.Fatal(err)
	}
	admin, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, id, token, origin string) *httptest.ResponseRecorder {
		t.Helper()
		result := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(method, "http://tunnel.example.test/api/nodes/"+id, nil)
		if origin != "" {
			httpRequest.Header.Set("Origin", origin)
		}
		httpRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		handler.ServeHTTP(result, httpRequest)
		return result
	}
	if response := request(http.MethodDelete, nodeID, user.Token, ""); response.Code != http.StatusForbidden {
		t.Fatalf("user remove = %d: %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodDelete, nodeID, admin.Token, "http://foreign.example.test"); response.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin remove = %d: %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodDelete, "local", admin.Token, ""); response.Code != http.StatusConflict {
		t.Fatalf("Local remove = %d: %s", response.Code, response.Body.String())
	}
	if _, err := state.database.Exec(`INSERT INTO clients(internal_id,owner_account_id,node_id,token,created_at) VALUES('client',?,?,'token','now')`, environmentAdministratorID, nodeID); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodDelete, nodeID, admin.Token, ""); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "NODE_HAS_CLIENTS") {
		t.Fatalf("assigned Client remove = %d: %s", response.Code, response.Body.String())
	}
	if _, err := state.database.Exec(`DELETE FROM clients WHERE internal_id='client'`); err != nil {
		t.Fatal(err)
	}
	response := request(http.MethodDelete, nodeID, admin.Token, "")
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"state":"pending"`) || !strings.Contains(response.Body.String(), `"desiredRevision":1`) {
		t.Fatalf("remove = %d: %s", response.Code, response.Body.String())
	}
	if repeat := request(http.MethodDelete, nodeID, admin.Token, ""); repeat.Code != http.StatusAccepted || repeat.Body.String() != response.Body.String() {
		t.Fatalf("repeated remove = %d: %s", repeat.Code, repeat.Body.String())
	}
}

func TestServerHTTPNodeForceForgetOnlyDeletesServerRecordOnFaultOrRemoval(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatal(err)
	}
	sessions := openServerSessions(t, accounts, state)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := newServerNodeObservations(state.database)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := newServerNodeCoordinator(state.sessions.Directory(), registry, observations)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: openServerControlPlane(t, state), Nodes: newServerNodeService(registry, observations, coordinator)})
	if err != nil {
		t.Fatal(err)
	}
	const nodeID = "0123456789abcdef0123456789abcdef"
	if _, err := registry.register(ctx, nodeID, "Remote", "http://127.0.0.1:7600", make([]byte, 32), 0); err != nil {
		t.Fatal(err)
	}
	admin, err := sessions.SignIn(ctx, "admin", "environment-secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := sessions.SignIn(ctx, "alice", "alice-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := func(id, token, origin, body string) *httptest.ResponseRecorder {
		t.Helper()
		result := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test/api/nodes/"+id+"/force-forget", strings.NewReader(body))
		httpRequest.Header.Set("Content-Type", "application/json")
		if origin != "" {
			httpRequest.Header.Set("Origin", origin)
		}
		httpRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		handler.ServeHTTP(result, httpRequest)
		return result
	}
	body := `{"confirmNodeId":"` + nodeID + `"}`
	if response := request(nodeID, user.Token, "", body); response.Code != http.StatusForbidden {
		t.Fatalf("user forget = %d: %s", response.Code, response.Body.String())
	}
	if response := request(nodeID, admin.Token, "http://foreign.example.test", body); response.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin forget = %d: %s", response.Code, response.Body.String())
	}
	if response := request(nodeID, admin.Token, "", `{}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed forget = %d: %s", response.Code, response.Body.String())
	}
	if response := request(nodeID, admin.Token, "", body); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "NODE_FORCE_FORGET_UNAVAILABLE") {
		t.Fatalf("healthy active forget = %d: %s", response.Code, response.Body.String())
	}
	if _, err := state.database.Exec(`INSERT INTO clients(internal_id,owner_account_id,node_id,token,created_at) VALUES('client',?,?,'token','now')`, environmentAdministratorID, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := observations.recordFailure(ctx, nodeID, "NODE_UNREACHABLE"); err != nil {
		t.Fatal(err)
	}
	if response := request(nodeID, admin.Token, "", body); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "NODE_HAS_CLIENTS") {
		t.Fatalf("dependent forget = %d: %s", response.Code, response.Body.String())
	}
	if _, err := state.database.Exec(`DELETE FROM clients WHERE internal_id='client'`); err != nil {
		t.Fatal(err)
	}
	if response := request(nodeID, admin.Token, "", body); response.Code != http.StatusNoContent {
		t.Fatalf("fault forget = %d: %s", response.Code, response.Body.String())
	}
	if _, err := registry.get(ctx, nodeID); err == nil || !hasServerDomainCode(err, "NOT_FOUND") {
		t.Fatalf("forgotten Node remains: %v", err)
	}
	if response := request("local", admin.Token, "", `{"confirmNodeId":"local"}`); response.Code != http.StatusConflict {
		t.Fatalf("Local forget = %d: %s", response.Code, response.Body.String())
	}
}
