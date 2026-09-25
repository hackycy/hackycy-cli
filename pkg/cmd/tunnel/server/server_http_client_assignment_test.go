package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestServerHTTPClientNodeAssignmentEnforcesOwnerAndOfflinePending(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	_, err = accounts.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), alice.ID, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`
		INSERT INTO nodes(node_id, kind, name, advertised_frp_host, advertised_frp_port, created_at, updated_at)
		VALUES('remote', 'remote', 'Remote', 'remote.example.test', 7000, 'now', 'now');
		INSERT INTO remote_nodes(node_id, node_public_key, management_address, frp_bind_port, http_vhost_port, port_start, port_end, active_token)
		VALUES('remote', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'http://127.0.0.1:7600', 7000, 8080, 20000, 20010, 'remote-token');
		INSERT INTO node_port_pools(node_id, port_start, port_end) VALUES('remote', 20000, 20010);
	`); err != nil {
		t.Fatal(err)
	}
	observations, err := newServerNodeObservations(state.database)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	nodes := newServerNodeService(registry, observations, nil)
	sessions := openServerSessions(t, accounts, state)
	aliceGrant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatal(err)
	}
	bobGrant, err := sessions.SignIn(context.Background(), "bob", "bob-secret")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewServerHTTPHandler(ServerHTTPOptions{Sessions: sessions, Accounts: accounts, ControlPlane: plane, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	aliceStream, aliceEvents := openServerHTTPEvents(t, server.Client(), server.URL, aliceGrant.Token)
	defer aliceStream.Body.Close()
	bobStream, bobEvents := openServerHTTPEvents(t, server.Client(), server.URL, bobGrant.Token)
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "changed" {
		t.Fatalf("owner initial SSE event = %q", event)
	}
	if event := waitForServerHTTPEvent(t, bobEvents); event != "changed" {
		t.Fatalf("other owner initial SSE event = %q", event)
	}
	publicNodes := serverHTTPServeRequest(handler, serverHTTPJSONRequest(http.MethodGet, "/api/nodes", bobGrant.Token, ""))
	if publicNodes.Code != http.StatusOK || !strings.Contains(publicNodes.Body.String(), `"id":"remote"`) || strings.Contains(publicNodes.Body.String(), "remote-token") {
		t.Fatalf("ordinary user Node list = (%d, %s)", publicNodes.Code, publicNodes.Body.String())
	}
	request := serverHTTPJSONRequest(http.MethodPut, "/api/clients/"+client.ID+"/node-assignment", aliceGrant.Token, `{"nodeId":"remote"}`)
	response := serverHTTPServeRequest(handler, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"pendingNodeId":"remote"`) {
		t.Fatalf("offline assignment = (%d, %s)", response.Code, response.Body.String())
	}
	if event := waitForServerHTTPEvent(t, aliceEvents); event != "changed" {
		t.Fatalf("owner assignment SSE event = %q", event)
	}
	assertNoServerHTTPEvent(t, bobEvents, bobStream.Body)
	if err := aliceStream.Body.Close(); err != nil {
		t.Fatal(err)
	}
	refetched := serverHTTPServeRequest(handler, serverHTTPJSONRequest(http.MethodGet, "/api/clients/"+client.ID, aliceGrant.Token, ""))
	if refetched.Code != http.StatusOK || !strings.Contains(refetched.Body.String(), `"pendingNodeId":"remote"`) || !strings.Contains(refetched.Body.String(), `"desiredNodeId":"local"`) {
		t.Fatalf("disconnect/re-GET assignment = (%d, %s)", refetched.Code, refetched.Body.String())
	}
	forbidden := serverHTTPServeRequest(handler, serverHTTPJSONRequest(http.MethodPut, "/api/clients/"+client.ID+"/node-assignment", bobGrant.Token, `{"nodeId":"remote"}`))
	if forbidden.Code != http.StatusNotFound {
		t.Fatalf("foreign assignment status = %d, body = %s", forbidden.Code, forbidden.Body.String())
	}
	cancel := serverHTTPServeRequest(handler, serverHTTPJSONRequest(http.MethodDelete, "/api/clients/"+client.ID+"/node-assignment/pending", aliceGrant.Token, ""))
	if cancel.Code != http.StatusOK || strings.Contains(cancel.Body.String(), `"pendingNodeId":"remote"`) {
		t.Fatalf("cancel pending = (%d, %s)", cancel.Code, cancel.Body.String())
	}
	if _, err := state.database.Exec(`UPDATE remote_nodes SET desired_snapshot='{}' WHERE node_id='remote'`); err != nil {
		t.Fatal(err)
	}
	connectedHandler, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions: sessions, Accounts: accounts, ControlPlane: plane, Nodes: nodes,
		Runtime: serverHTTPTestClientRuntime{states: map[string]ServerClientRuntimeState{
			client.ID: {ConnectionState: ServerClientConnected, ProcessState: tunnelruntime.FRPProcessStopped},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/clients/" + client.ID + "/node-assignment"
	unavailable := serverHTTPServeRequest(connectedHandler, serverHTTPJSONRequest(http.MethodPut, path, aliceGrant.Token, `{"nodeId":"remote"}`))
	if unavailable.Code != http.StatusServiceUnavailable || !strings.Contains(unavailable.Body.String(), `"code":"NODE_TARGET_UNAVAILABLE"`) {
		t.Fatalf("unknown target preflight = (%d, %s)", unavailable.Code, unavailable.Body.String())
	}
	unchanged, err := plane.GetClient(t.Context(), client.ID)
	if err != nil || unchanged.NodeID != "local" || unchanged.PendingNodeID != nil {
		t.Fatalf("unknown target changed assignment = (%+v, %v)", unchanged, err)
	}
	if err := observations.recordStatus(t.Context(), "remote", nodeStatus{Claimed: true, FRPSProcess: "running", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	foreignOrigin := serverHTTPJSONRequest(http.MethodPut, path, aliceGrant.Token, `{"nodeId":"remote"}`)
	foreignOrigin.Header.Set("Origin", "http://foreign.example.test")
	if response := serverHTTPServeRequest(connectedHandler, foreignOrigin); response.Code != http.StatusForbidden {
		t.Fatalf("foreign Origin assignment status = %d", response.Code)
	}
	assigned := serverHTTPServeRequest(connectedHandler, serverHTTPJSONRequest(http.MethodPut, path, aliceGrant.Token, `{"nodeId":"remote"}`))
	if assigned.Code != http.StatusOK || !strings.Contains(assigned.Body.String(), `"assignment":{"nodeId":"remote"`) || strings.Contains(assigned.Body.String(), "remote-token") {
		t.Fatalf("online assignment = (%d, %s)", assigned.Code, assigned.Body.String())
	}
	committed, err := plane.GetClient(t.Context(), client.ID)
	if err != nil || committed.NodeID != "remote" || committed.DesiredRevision != 1 {
		t.Fatalf("online assignment persistence = (%+v, %v)", committed, err)
	}
}

func serverHTTPJSONRequest(method, path, token, body string) *http.Request {
	request := httptest.NewRequest(method, "http://tunnel.example.test"+path, strings.NewReader(body))
	request.Header.Set("Origin", "http://tunnel.example.test")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
	return request
}

func serverHTTPServeRequest(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
