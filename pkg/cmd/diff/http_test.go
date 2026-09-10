package diff

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPHandlerServesStateWithCamelCaseSnapshotAndSecurityHeaders(t *testing.T) {
	baseline, target := comparisonRoots(t)
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	handler := NewHTTPHandler(workspace)

	idle := httpResponse(handler, http.MethodGet, "/api/state")
	assertHTTPAPIHeaders(t, idle)
	if idle.Code != http.StatusOK || strings.Contains(idle.Body.String(), "\"snapshot\"") {
		t.Fatalf("idle state = code %d, body %s", idle.Code, idle.Body.String())
	}
	idleBody := decodeHTTPState(t, idle)
	if idleBody.Version != 1 || idleBody.Workspace.Phase != string(PhaseIdle) || idleBody.Snapshot != nil {
		t.Fatalf("idle state body = %#v", idleBody)
	}

	snapshot := refreshWorkspace(t, workspace)
	ready := httpResponse(handler, http.MethodGet, "/api/state")
	assertHTTPAPIHeaders(t, ready)
	readyBody := decodeHTTPState(t, ready)
	if ready.Code != http.StatusOK || readyBody.Workspace.Phase != string(PhaseReady) || readyBody.Workspace.SnapshotID != snapshot.Summary().ID || readyBody.Snapshot == nil {
		t.Fatalf("ready state body = %#v", readyBody)
	}
	summary := snapshot.Summary()
	if got := readyBody.Snapshot; got.ID != summary.ID || got.BaselineDirectory != summary.BaselineDirectory || got.TargetDirectory != summary.TargetDirectory || got.CreatedAt != summary.CreatedAt || got.Counts != (httpStatusCounts{}) || got.Issues != 0 {
		t.Fatalf("snapshot body = %#v", got)
	}
	if strings.Contains(ready.Body.String(), "BaselineDirectory") || strings.Contains(ready.Body.String(), "SnapshotID") {
		t.Fatalf("state JSON did not use camelCase: %s", ready.Body.String())
	}
}

func TestHTTPHandlerRejectsUnsupportedMethodsAndUnknownAPIRoutes(t *testing.T) {
	baseline, target := comparisonRoots(t)
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	handler := NewHTTPHandler(workspace)

	for _, method := range []string{http.MethodPost, http.MethodHead} {
		response := httpResponse(handler, method, "/api/state")
		assertHTTPAPIHeaders(t, response)
		assertHTTPAPIError(t, response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
	}
	unknown := httpResponse(handler, http.MethodGet, "/api/missing")
	assertHTTPAPIHeaders(t, unknown)
	assertHTTPAPIError(t, unknown, http.StatusNotFound, "NOT_FOUND", "API route not found")
}

func TestHTTPJSONEncodingFailureRetainsTheAPIErrorEnvelope(t *testing.T) {
	response := httptest.NewRecorder()
	writeHTTPJSON(response, http.StatusOK, math.Inf(1))
	assertHTTPAPIHeaders(t, response)
	assertHTTPAPIError(t, response, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error")
}

type httpStateResponse struct {
	Version   int `json:"version"`
	Workspace struct {
		Phase      string `json:"phase"`
		SnapshotID string `json:"snapshotId"`
	} `json:"workspace"`
	Snapshot *struct {
		ID                string           `json:"id"`
		BaselineDirectory string           `json:"baselineDirectory"`
		TargetDirectory   string           `json:"targetDirectory"`
		CreatedAt         string           `json:"createdAt"`
		Counts            httpStatusCounts `json:"counts"`
		Issues            int              `json:"issues"`
	} `json:"snapshot"`
}

func httpResponse(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, target, nil))
	return response
}

func decodeHTTPState(t *testing.T, response *httptest.ResponseRecorder) httpStateResponse {
	t.Helper()
	var body httpStateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	return body
}

func assertHTTPAPIHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json;charset=utf-8" || response.Header().Get("Content-Security-Policy") != diffAPICSP || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("API headers = %v", response.Header())
	}
}

func assertHTTPAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("API status = %d, want %d", response.Code, status)
	}
	var body struct {
		Version int `json:"version"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Version != 1 || body.Error.Code != code || body.Error.Message != message {
		t.Fatalf("API error = %#v", body)
	}
}
