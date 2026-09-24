package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerHTTPNodeTokenRotationRequiresAdminOriginAndExpectedRevision(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatal(err)
	}
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
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
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane, Nodes: newServerNodeService(registry, observations, coordinator)})
	if err != nil {
		t.Fatal(err)
	}
	id := "e123456789abcdef0123456789abcdef"
	if _, err := registry.register(ctx, id, "Remote", "http://127.0.0.1:7600", make([]byte, 32), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.saveDesired(ctx, id, 0, serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 20001}); err != nil {
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
	request := func(method, token, origin, body string) *httptest.ResponseRecorder {
		t.Helper()
		result := httptest.NewRecorder()
		httpRequest := httptest.NewRequest(method, "http://tunnel.example.test/api/nodes/"+id+"/token-rotation", bytes.NewBufferString(body))
		httpRequest.Header.Set("Content-Type", "application/json")
		if origin != "" {
			httpRequest.Header.Set("Origin", origin)
		}
		httpRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		handler.ServeHTTP(result, httpRequest)
		return result
	}
	body := `{"expectedRevision":1}`
	if response := request(http.MethodPost, user.Token, "", body); response.Code != http.StatusForbidden {
		t.Fatalf("user rotation = %d: %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, admin.Token, "http://foreign.example.test", body); response.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin rotation = %d: %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, admin.Token, "", `{}`); response.Code != http.StatusBadRequest {
		t.Fatalf("missing revision = %d: %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, admin.Token, "", body); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method = %d: %s", response.Code, response.Body.String())
	}
	response := request(http.MethodPost, admin.Token, "", body)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"desiredRevision":2`)) {
		t.Fatalf("rotation stage = %d: %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, admin.Token, "", body); response.Code != http.StatusConflict {
		t.Fatalf("duplicate rotation = %d: %s", response.Code, response.Body.String())
	}
	var active, staged string
	if err := state.database.QueryRow(`SELECT active_token,staged_token FROM remote_nodes WHERE node_id=?`, id).Scan(&active, &staged); err != nil || active == staged || bytes.Contains(response.Body.Bytes(), []byte(active)) || bytes.Contains(response.Body.Bytes(), []byte(staged)) {
		t.Fatalf("rotation response leaked a credential or stage was not saved: %v", err)
	}
}
