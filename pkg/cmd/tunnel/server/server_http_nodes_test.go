package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestServerHTTPNodePreviewAndClaimRequireAdminAndPinnedFingerprint(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	if _, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
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
	admin, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatal(err)
	}
	address, stopNode := startNodeForMetadataTest(t, filepath.Join(t.TempDir(), "node"))
	defer stopNode()
	post := func(path, token, origin string, body any) *httptest.ResponseRecorder {
		t.Helper()
		contents, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "http://tunnel.example.test"+path, bytes.NewReader(contents))
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	requestBody := map[string]string{"managementAddress": address}
	if response := post("/api/nodes/claim-previews", user.Token, "", requestBody); response.Code != http.StatusForbidden {
		t.Fatalf("user preview = %d: %s", response.Code, response.Body.String())
	}
	if response := post("/api/nodes/claim-previews", admin.Token, "http://foreign.example.test", requestBody); response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin preview = %d: %s", response.Code, response.Body.String())
	}
	response := post("/api/nodes/claim-previews", admin.Token, "", requestBody)
	if response.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", response.Code, response.Body.String())
	}
	var preview struct {
		PreviewID       string `json:"previewId"`
		NodeFingerprint string `json:"nodeFingerprint"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || preview.PreviewID == "" || preview.NodeFingerprint == "" {
		t.Fatalf("preview response = (%+v, %v): %s", preview, err, response.Body.String())
	}
	claimBody := map[string]string{"previewId": preview.PreviewID, "name": "Remote", "confirmedFingerprint": preview.NodeFingerprint, "mode": "claim"}
	if response := post("/api/nodes", user.Token, "", claimBody); response.Code != http.StatusForbidden {
		t.Fatalf("user claim = %d: %s", response.Code, response.Body.String())
	}
	claimBody["confirmedFingerprint"] = "SHA256:wrong"
	if response := post("/api/nodes", admin.Token, "", claimBody); response.Code != http.StatusConflict {
		t.Fatalf("wrong fingerprint claim = %d: %s", response.Code, response.Body.String())
	}
	response = post("/api/nodes/claim-previews", admin.Token, "", requestBody)
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	claimBody["previewId"] = preview.PreviewID
	claimBody["confirmedFingerprint"] = preview.NodeFingerprint
	response = post("/api/nodes", admin.Token, "", claimBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("claim = %d: %s", response.Code, response.Body.String())
	}
	var count int
	if err := state.database.QueryRow(`SELECT count(*) FROM remote_nodes`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("claimed Node not registered: count=%d, err=%v", count, err)
	}
	var created struct {
		Node struct {
			ID string `json:"id"`
		} `json:"node"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.Node.ID == "" {
		t.Fatalf("created Node ID = (%+v, %v)", created, err)
	}
	patch := func(token, origin string) *httptest.ResponseRecorder {
		t.Helper()
		contents := []byte(`{"name":"Renamed","managementAddress":"http://127.0.0.1:7601","advertisedFrpAddress":{"host":"edge.example.com","port":7000}}`)
		request := httptest.NewRequest(http.MethodPatch, "http://tunnel.example.test/api/nodes/"+created.Node.ID, bytes.NewReader(contents))
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		return result
	}
	if response := patch(user.Token, ""); response.Code != http.StatusForbidden {
		t.Fatalf("user patch = %d: %s", response.Code, response.Body.String())
	}
	if response := patch(admin.Token, "http://foreign.example.test"); response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin patch = %d: %s", response.Code, response.Body.String())
	}
	if response := patch(admin.Token, ""); response.Code != http.StatusOK {
		t.Fatalf("admin patch = %d: %s", response.Code, response.Body.String())
	}
	record, err := registry.get(context.Background(), created.Node.ID)
	if err != nil || record.Name != "Renamed" || record.PendingManagementAddress != "http://127.0.0.1:7601" || record.ManagementAddress != address || record.AdvertisedFRPHost.String != "edge.example.com" {
		t.Fatalf("patch persistence = (%+v, %v)", record, err)
	}
	mutate := func(method, path, token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var contents []byte
		if body != nil {
			var err error
			contents, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		request := httptest.NewRequest(method, "http://tunnel.example.test"+path, bytes.NewReader(contents))
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		return result
	}
	desiredPath := "/api/nodes/" + created.Node.ID + "/desired"
	desiredBody := map[string]any{"expectedRevision": 0, "settings": serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 20100}}
	if result := mutate(http.MethodPut, desiredPath, user.Token, desiredBody); result.Code != http.StatusForbidden {
		t.Fatalf("user desired save = %d: %s", result.Code, result.Body.String())
	}
	result := mutate(http.MethodPut, desiredPath, admin.Token, desiredBody)
	if result.Code != http.StatusOK || !bytes.Contains(result.Body.Bytes(), []byte(`"configuration":"pending"`)) {
		t.Fatalf("saved desired = %d: %s", result.Code, result.Body.String())
	}
	if result := mutate(http.MethodPut, desiredPath, admin.Token, desiredBody); result.Code != http.StatusConflict {
		t.Fatalf("stale revision save = %d: %s", result.Code, result.Body.String())
	}
	result = mutate(http.MethodPost, "/api/nodes/"+created.Node.ID+"/reapply", admin.Token, nil)
	if result.Code != http.StatusOK || !bytes.Contains(result.Body.Bytes(), []byte(`"desiredRevision":2`)) {
		t.Fatalf("reapply = %d: %s", result.Code, result.Body.String())
	}
	record, err = registry.get(context.Background(), created.Node.ID)
	if err != nil || record.DesiredRevision != 2 || !record.DesiredSnapshot.Valid {
		t.Fatalf("reapply was not saved: (%+v, %v)", record, err)
	}
	get := func(path, token string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "http://tunnel.example.test"+path, nil)
		request.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: token})
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, request)
		return result
	}
	result = get("/api/nodes", user.Token)
	if result.Code != http.StatusOK {
		t.Fatalf("public nodes = %d: %s", result.Code, result.Body.String())
	}
	var list struct {
		Nodes []serverNodeSummary `json:"nodes"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &list); err != nil || len(list.Nodes) != 2 {
		t.Fatalf("public nodes = (%+v, %v)", list, err)
	}
	var token string
	if err := state.database.QueryRow(`SELECT active_token FROM remote_nodes WHERE node_id=?`, created.Node.ID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{address, preview.NodeFingerprint, token, "pendingManagementAddress", "desiredRevision", "failureCode"} {
		if bytes.Contains(result.Body.Bytes(), []byte(secret)) {
			t.Fatalf("public Node list leaked %q: %s", secret, result.Body.String())
		}
	}
	result = get("/api/nodes/"+created.Node.ID, user.Token)
	if result.Code != http.StatusOK || !bytes.Contains(result.Body.Bytes(), []byte(`"selectable":false`)) {
		t.Fatalf("public Node detail = %d: %s", result.Code, result.Body.String())
	}
	managementPath := "/api/nodes/" + created.Node.ID + "/management"
	if result := get(managementPath, user.Token); result.Code != http.StatusForbidden {
		t.Fatalf("user management detail = %d: %s", result.Code, result.Body.String())
	}
	if err := observations.recordStatus(context.Background(), created.Node.ID, nodeStatus{
		Claimed: true, HighestAcceptedRevision: 2, SHA256: record.DesiredHash.String,
		AppliedRevision: 1, FailedRevision: 2, Phase: "failed", FailureCode: "NODE_APPLY_FAILED",
		FRPSProcess: "running", ObservedAt: "2026-09-24T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	result = get(managementPath, admin.Token)
	if result.Code != http.StatusOK {
		t.Fatalf("admin management detail = %d: %s", result.Code, result.Body.String())
	}
	var management struct {
		Node serverNodeManagementView `json:"node"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &management); err != nil || management.Node.Desired.Revision != 2 || management.Node.Observed.Configuration != "apply_failed_old_running" || management.Node.Observed.AppliedRevision != 1 || management.Node.Observed.Error == nil || management.Node.Observed.Error.Code != "NODE_APPLY_FAILED" {
		t.Fatalf("admin management projection = (%+v, %v): %s", management, err, result.Body.String())
	}
	if bytes.Contains(result.Body.Bytes(), []byte(token)) || !bytes.Contains(result.Body.Bytes(), []byte(preview.NodeFingerprint)) {
		t.Fatalf("admin detail leaked token or omitted identity: %s", result.Body.String())
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	streamCtx, cancelStream := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStream()
	streamRequest, err := http.NewRequestWithContext(streamCtx, http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	streamRequest.AddCookie(&http.Cookie{Name: serverSessionCookieName, Value: user.Token})
	streamResponse, err := http.DefaultClient.Do(streamRequest)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(streamResponse.Body)
	if line, err := reader.ReadString('\n'); err != nil || !bytes.Contains([]byte(line), []byte(`"event":"changed"`)) {
		t.Fatalf("initial SSE event = (%q, %v)", line, err)
	}
	_, _ = reader.ReadString('\n')
	if err := observations.recordFailure(context.Background(), created.Node.ID, "NODE_PROTOCOL_INCOMPATIBLE"); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || !bytes.Contains([]byte(line), []byte(`"event":"changed"`)) {
		t.Fatalf("Node change SSE event = (%q, %v)", line, err)
	}
	cancelStream()
	_ = streamResponse.Body.Close()
	result = get("/api/nodes", user.Token)
	if result.Code != http.StatusOK || !bytes.Contains(result.Body.Bytes(), []byte(`"state":"incompatible"`)) || !bytes.Contains(result.Body.Bytes(), []byte(`"lastKnownFrps":{"state":"running"`)) {
		t.Fatalf("disconnect/re-GET lost Node state: %d: %s", result.Code, result.Body.String())
	}
}
