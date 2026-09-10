package diff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHTTPHandlerOwnsRefreshLifecycle(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "before")
	writeComparisonFile(t, target, "changed.txt", "after")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	handler := NewHTTPHandler(workspace).(*diffHTTPHandler)

	comparing := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
	})
	var comparingOnce sync.Once
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if state.Phase == PhaseComparing {
			comparingOnce.Do(func() {
				close(comparing)
				<-release
			})
		}
	})
	t.Cleanup(unsubscribe)

	accepted := httpRefreshResponse(handler, http.MethodPost, "http://127.0.0.1:3311")
	assertHTTPAPIHeaders(t, accepted)
	if accepted.Code != http.StatusAccepted || !decodeHTTPAccepted(t, accepted) {
		t.Fatalf("accepted refresh = code %d, body %s", accepted.Code, accepted.Body.String())
	}
	select {
	case <-comparing:
	case <-time.After(time.Second):
		t.Fatal("refresh did not reach comparing")
	}

	active := httpRefreshResponse(handler, http.MethodPost, "http://127.0.0.1:3311")
	assertHTTPAPIHeaders(t, active)
	assertHTTPAPIError(t, active, http.StatusConflict, "REFRESH_ACTIVE", "A refresh is already active")

	canceled := httpRefreshResponse(handler, http.MethodDelete, "http://127.0.0.1:3311")
	assertHTTPRefreshNoContentHeaders(t, canceled)
	if canceled.Code != http.StatusNoContent || canceled.Body.Len() != 0 {
		t.Fatalf("cancel response = code %d, headers %v, body %q", canceled.Code, canceled.Header(), canceled.Body.String())
	}
	releaseOnce.Do(func() { close(release) })
	waitForWorkspacePhase(t, workspace, PhaseCanceled)
	waitForHTTPRefreshClear(t, handler)

	writeComparisonFile(t, target, "added.txt", "new")
	followup := httpRefreshResponse(handler, http.MethodPost, "")
	assertHTTPAPIHeaders(t, followup)
	if followup.Code != http.StatusAccepted || !decodeHTTPAccepted(t, followup) {
		t.Fatalf("origin-less refresh = code %d, body %s", followup.Code, followup.Body.String())
	}
	waitForWorkspacePhase(t, workspace, PhaseReady)
	if snapshot := workspace.Snapshot(); snapshot == nil || snapshot.Summary().Counts != (StatusCounts{Added: 1, Modified: 1}) {
		t.Fatalf("follow-up snapshot = %#v", snapshot)
	}
}

func TestProtocolHandlersLetRESTCancelMCPRefresh(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "before")
	writeComparisonFile(t, target, "changed.txt", "after")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	handlers := NewProtocolHandlers(workspace, "127.0.0.1")
	rest := handlers.REST.(*diffHTTPHandler)
	mcpServer := httptest.NewServer(handlers.MCP)
	defer mcpServer.Close()

	comparing := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
	})
	var comparingOnce sync.Once
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if state.Phase == PhaseComparing {
			comparingOnce.Do(func() {
				close(comparing)
				<-release
			})
		}
	})
	t.Cleanup(unsubscribe)

	started := mcpToolCall(t, mcpServer.URL, "1", "refresh_comparison", `{}`)
	assertMCPResponseHeaders(t, started)
	var startedRPC mcpRPCResponse
	decodeMCPResponse(t, started, &startedRPC)
	assertMCPRefreshResult(t, startedRPC, "Refresh accepted", true, false)
	select {
	case <-comparing:
	case <-time.After(time.Second):
		t.Fatal("MCP refresh did not reach comparing")
	}

	canceled := httpRefreshResponse(rest, http.MethodDelete, "")
	assertHTTPRefreshNoContentHeaders(t, canceled)
	if canceled.Code != http.StatusNoContent || canceled.Body.Len() != 0 {
		t.Fatalf("REST cancellation = code %d, body %q", canceled.Code, canceled.Body.String())
	}
	releaseOnce.Do(func() { close(release) })
	waitForWorkspacePhase(t, workspace, PhaseCanceled)
	waitForHTTPRefreshClear(t, rest)

	followup := httpRefreshResponse(rest, http.MethodPost, "")
	assertHTTPAPIHeaders(t, followup)
	if followup.Code != http.StatusAccepted || !decodeHTTPAccepted(t, followup) {
		t.Fatalf("REST follow-up refresh = code %d, body %s", followup.Code, followup.Body.String())
	}
	waitForWorkspacePhase(t, workspace, PhaseReady)
}

func TestHTTPHandlerRejectsInvalidRefreshMethodsAndOrigins(t *testing.T) {
	baseline, target := comparisonRoots(t)
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	handler := NewHTTPHandler(workspace)

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut} {
		response := httpRefreshResponse(handler, method, "")
		assertHTTPAPIHeaders(t, response)
		assertHTTPAPIError(t, response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST or DELETE")
	}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		response := httpRefreshResponse(handler, method, "https://attacker.example")
		assertHTTPAPIHeaders(t, response)
		assertHTTPAPIError(t, response, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Refresh requests must be same-origin")
	}
	if state := workspace.State(); state.Phase != PhaseIdle {
		t.Fatalf("forbidden refresh changed workspace state: %#v", state)
	}
}

func httpRefreshResponse(handler http.Handler, method, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/refresh", nil)
	request.Host = "127.0.0.1:3311"
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeHTTPAccepted(t *testing.T, response *httptest.ResponseRecorder) bool {
	t.Helper()
	var body struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode accepted response: %v", err)
	}
	return body.Accepted
}

func assertHTTPRefreshNoContentHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Security-Policy") != diffAPICSP || response.Header().Get("Content-Type") != "" || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("refresh no-content headers = %v", response.Header())
	}
}

func waitForWorkspacePhase(t *testing.T, workspace *Workspace, phase WorkspacePhase) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if workspace.State().Phase == phase {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("workspace phase = %q, want %q", workspace.State().Phase, phase)
}

func waitForHTTPRefreshClear(t *testing.T, handler *diffHTTPHandler) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		handler.refresh.mu.Lock()
		active := handler.refresh.active
		handler.refresh.mu.Unlock()
		if active == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("HTTP refresh tracker remained active")
}
