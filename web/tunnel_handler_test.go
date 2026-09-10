package webassets

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTunnelProductionHandlerReservesControlPlaneRoutesForTheAdapter(t *testing.T) {
	var calls []string
	handler, err := NewTunnelProductionHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.RequestURI())
		writer.Header().Set("X-Tunnel-Adapter", "reached")
		writer.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatalf("NewTunnelProductionHandler returned an error: %v", err)
	}

	for _, testCase := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/healthz"},
		{method: http.MethodPost, path: "/healthz"},
		{method: http.MethodGet, path: "/api"},
		{method: http.MethodPost, path: "/api/session?refresh=true"},
		{method: http.MethodGet, path: "/api/agent"},
		{method: http.MethodGet, path: "/api/events"},
	} {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			response := routeResponse(handler, testCase.method, testCase.path)
			if response.Code != http.StatusNoContent || response.Header().Get("X-Tunnel-Adapter") != "reached" {
				t.Fatalf("adapter route response: code=%d headers=%v", response.Code, response.Header())
			}
		})
	}
	if want := []string{
		"GET /healthz",
		"POST /healthz",
		"GET /api",
		"POST /api/session?refresh=true",
		"GET /api/agent",
		"GET /api/events",
	}; len(calls) != len(want) {
		t.Fatalf("adapter calls = %#v, want %#v", calls, want)
	} else {
		for index := range want {
			if calls[index] != want[index] {
				t.Fatalf("adapter calls = %#v, want %#v", calls, want)
			}
		}
	}
}

func TestTunnelProductionHandlerServesOnlyTheTunnelShellAndEmbeddedAssets(t *testing.T) {
	handler, err := NewTunnelProductionHandler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatalf("NewTunnelProductionHandler returned an error: %v", err)
	}
	site, err := Load("tunnel-server")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	for _, path := range []string{"/", "/clients", "/clients/client-1", "/accounts", "/server"} {
		t.Run("shell "+path, func(t *testing.T) {
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
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/not-tunnel"), http.StatusNotFound, "NOT_FOUND", "Route not found")
}

func TestTunnelProductionHandlerRequiresAnAdapter(t *testing.T) {
	if handler, err := NewTunnelProductionHandler(nil); err == nil || handler != nil {
		t.Fatalf("NewTunnelProductionHandler(nil) = (%v, %v), want nil handler and error", handler, err)
	}
}

func TestTunnelProductionHandlerDoesNotRewriteAdapterResponses(t *testing.T) {
	handler, err := NewTunnelProductionHandler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "private, max-age=0")
		writer.Header().Set("Content-Type", "application/custom")
		writer.WriteHeader(http.StatusTeapot)
	}))
	if err != nil {
		t.Fatalf("NewTunnelProductionHandler returned an error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/custom", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTeapot || response.Header().Get("Cache-Control") != "private, max-age=0" || response.Header().Get("Content-Type") != "application/custom" {
		t.Fatalf("adapter response was changed: code=%d headers=%v", response.Code, response.Header())
	}
}
