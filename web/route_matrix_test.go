package webassets

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	diffContentSecurityPolicy   = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; worker-src 'self'; img-src 'self' blob: data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	fsContentSecurityPolicy     = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; worker-src 'self'; img-src 'self' blob: data:; media-src 'self'; frame-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	tunnelContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	apiContentSecurityPolicy    = "default-src 'none'; frame-ancestors 'none'"
)

func TestDiffRouteMatrix(t *testing.T) {
	site, err := Load("diff")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	handler, err := NewReadinessHandler("diff", ReadinessHandlerOptions{MCP: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-MCP-Handler", "reached")
		writer.WriteHeader(http.StatusNoContent)
	})})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	for _, testCase := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/"},
		{method: http.MethodGet, path: "/deep/link"},
		{method: http.MethodGet, path: "/mcp/missing"},
		{method: http.MethodGet, path: "/assets/missing.js"},
		{method: http.MethodPost, path: "/assets/real.js"},
		{method: http.MethodOptions, path: "/anything"},
	} {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			assertShell(t, routeResponse(handler, testCase.method, testCase.path), "HACKYCY CLI — DIFF SERVER", diffContentSecurityPolicy)
		})
	}
	assertHeadShell(t, routeResponse(handler, http.MethodHead, "/deep/link"), diffContentSecurityPolicy)
	assertAsset(t, routeResponse(handler, http.MethodGet, "/"+firstAsset(t, site.files)))
	assertHeadAsset(t, routeResponse(handler, http.MethodHead, "/"+firstAsset(t, site.files)))
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/api/missing"), http.StatusNotFound, "NOT_FOUND", "API route not found")
	mcp := routeResponse(handler, http.MethodGet, "/mcp")
	if mcp.Code != http.StatusNoContent || mcp.Header().Get("X-MCP-Handler") != "reached" {
		t.Fatalf("exact MCP route did not retain priority: code=%d headers=%v", mcp.Code, mcp.Header())
	}
}

func TestFSRouteMatrix(t *testing.T) {
	site, err := Load("fs")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	handler, err := NewReadinessHandler("fs", ReadinessHandlerOptions{})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	for _, path := range []string{"/", "/browse", "/browse/docs/examples"} {
		t.Run("GET "+path, func(t *testing.T) {
			assertShell(t, routeResponse(handler, http.MethodGet, path), "HACKYCY CLI - FILE BROWSER", fsContentSecurityPolicy)
		})
	}
	assertHeadShell(t, routeResponse(handler, http.MethodHead, "/browse/docs"), fsContentSecurityPolicy)
	assertAsset(t, routeResponse(handler, http.MethodGet, "/"+firstAsset(t, site.files)))
	assertHeadAsset(t, routeResponse(handler, http.MethodHead, "/"+firstAsset(t, site.files)))
	assertRouteError(t, routeResponse(handler, http.MethodPost, "/"), http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
	assertRouteError(t, routeResponse(handler, http.MethodOptions, "/browse"), http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
	assertRouteError(t, routeResponse(handler, http.MethodPost, "/"+firstAsset(t, site.files)), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/assets/missing.js"), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/api/missing"), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/files/missing"), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/not-fs"), http.StatusNotFound, "NOT_FOUND", "Route not found")
}

func TestTunnelRouteMatrix(t *testing.T) {
	site, err := Load("tunnel-server")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	handler, err := NewReadinessHandler("tunnel-server", ReadinessHandlerOptions{})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	for _, path := range []string{"/", "/clients", "/clients/client-1", "/accounts", "/server"} {
		t.Run("GET "+path, func(t *testing.T) {
			assertShell(t, routeResponse(handler, http.MethodGet, path), "HACKYCY CLI - TUNNEL CONTROL PLANE", tunnelContentSecurityPolicy)
		})
	}
	assertHeadShell(t, routeResponse(handler, http.MethodHead, "/clients/client-1"), tunnelContentSecurityPolicy)
	assertAsset(t, routeResponse(handler, http.MethodGet, "/"+firstAsset(t, site.files)))
	assertHeadAsset(t, routeResponse(handler, http.MethodHead, "/"+firstAsset(t, site.files)))
	assertRouteError(t, routeResponse(handler, http.MethodPost, "/"), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodOptions, "/clients"), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodPost, "/"+firstAsset(t, site.files)), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/assets/missing.js"), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/api/missing"), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/not-tunnel"), http.StatusNotFound, "NOT_FOUND", "Route not found")
}

func routeResponse(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
	return response
}

func assertShell(t *testing.T, response *httptest.ResponseRecorder, title, contentSecurityPolicy string) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Content-Security-Policy") != contentSecurityPolicy || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Body.String(), title) {
		t.Fatalf("unexpected shell response: code=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func assertHeadShell(t *testing.T, response *httptest.ResponseRecorder, contentSecurityPolicy string) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Security-Policy") != contentSecurityPolicy || response.Body.Len() != 0 {
		t.Fatalf("unexpected shell HEAD response: code=%d headers=%v body=%d", response.Code, response.Header(), response.Body.Len())
	}
}

func assertAsset(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Body.Len() == 0 {
		t.Fatalf("unexpected asset response: code=%d headers=%v body=%d", response.Code, response.Header(), response.Body.Len())
	}
}

func assertHeadAsset(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || response.Body.Len() != 0 {
		t.Fatalf("unexpected asset HEAD response: code=%d headers=%v body=%d", response.Code, response.Header(), response.Body.Len())
	}
}

func assertRouteError(t *testing.T, response *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	if response.Code != status || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Security-Policy") != apiContentSecurityPolicy || response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected route error: code=%d headers=%v", response.Code, response.Header())
	}
	var body struct {
		Version int `json:"version"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode route error: %v", err)
	}
	if body.Version != 1 || body.Error.Code != code || body.Error.Message != message {
		t.Fatalf("unexpected route error body: %#v", body)
	}
}
