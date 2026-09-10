package diff

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestServerHandlerComposesDiffProtocolsAndEmbeddedShell(t *testing.T) {
	baseline, target := comparisonRoots(t)
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	handler, err := NewServerHandler(workspace, "127.0.0.1")
	if err != nil {
		t.Fatalf("NewServerHandler() error = %v", err)
	}

	state := serverHandlerResponse(handler, http.MethodGet, "/api/state", nil)
	if state.Code != http.StatusOK || state.Header().Get("Content-Type") != "application/json;charset=utf-8" || state.Header().Get("Content-Security-Policy") != diffAPICSP {
		t.Fatalf("state response = code %d, headers %v, body %q", state.Code, state.Header(), state.Body.String())
	}
	var stateBody struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(state.Body.Bytes(), &stateBody); err != nil || stateBody.Version != 1 {
		t.Fatalf("state JSON = %#v, error = %v", stateBody, err)
	}

	apiMiss := serverHandlerResponse(handler, http.MethodGet, "/api/missing", nil)
	if apiMiss.Code != http.StatusNotFound || apiMiss.Header().Get("Content-Type") != "application/json;charset=utf-8" || !strings.Contains(apiMiss.Body.String(), `"API route not found"`) {
		t.Fatalf("API miss response = code %d, headers %v, body %q", apiMiss.Code, apiMiss.Header(), apiMiss.Body.String())
	}

	shell := serverHandlerResponse(handler, http.MethodGet, "/deep/link", nil)
	if shell.Code != http.StatusOK || shell.Header().Get("Content-Type") != "text/html; charset=utf-8" || shell.Header().Get("Cache-Control") != "no-store" || shell.Header().Get("Content-Security-Policy") != diffAPICSP || !strings.Contains(shell.Body.String(), "HACKYCY CLI — DIFF SERVER") {
		t.Fatalf("shell response = code %d, headers %v, body %q", shell.Code, shell.Header(), shell.Body.String())
	}

	assetMatch := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindStringSubmatch(shell.Body.String())
	if len(assetMatch) != 2 {
		t.Fatalf("shell does not reference a generated asset: %q", shell.Body.String())
	}
	asset := serverHandlerResponse(handler, http.MethodGet, assetMatch[1], nil)
	if asset.Code != http.StatusOK || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || asset.Body.Len() == 0 {
		t.Fatalf("asset response = code %d, headers %v, bytes %d", asset.Code, asset.Header(), asset.Body.Len())
	}

	legacyMCPFallback := serverHandlerResponse(handler, http.MethodGet, "/mcp/missing", nil)
	if legacyMCPFallback.Code != http.StatusOK || legacyMCPFallback.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("MCP fallback response = code %d, headers %v, body %q", legacyMCPFallback.Code, legacyMCPFallback.Header(), legacyMCPFallback.Body.String())
	}

	initialize := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"server-handler-test","version":"1.0.0"}}}`)
	mcp := serverHandlerResponse(handler, http.MethodPost, "/mcp", initialize)
	if mcp.Code != http.StatusOK || mcp.Header().Get("Content-Type") != "application/json" || mcp.Header().Get("Cache-Control") != "no-store" || !strings.Contains(mcp.Body.String(), `"ycy-directory-diff"`) {
		t.Fatalf("MCP response = code %d, headers %v, body %q", mcp.Code, mcp.Header(), mcp.Body.String())
	}
}

func serverHandlerResponse(handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://127.0.0.1"+path, bytes.NewReader(body))
	if path == "/mcp" {
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
