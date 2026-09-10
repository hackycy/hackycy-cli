package webassets

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFSProductionHandlerReservesOnlyCommandRoutesForTheAdapter(t *testing.T) {
	var calls []string
	handler, err := NewFSProductionHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.RequestURI())
		writer.Header().Set("X-FS-Adapter", "reached")
		writer.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatalf("NewFSProductionHandler returned an error: %v", err)
	}

	for _, testCase := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api"},
		{method: http.MethodPost, path: "/api/session?refresh=true"},
		{method: http.MethodGet, path: "/files"},
		{method: http.MethodHead, path: "/files/reports%20and%20notes.txt"},
		{method: http.MethodGet, path: "/thumbnails"},
		{method: http.MethodGet, path: "/thumbnails/cover.webp"},
	} {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			response := routeResponse(handler, testCase.method, testCase.path)
			if response.Code != http.StatusNoContent || response.Header().Get("X-FS-Adapter") != "reached" {
				t.Fatalf("adapter route response: code=%d headers=%v", response.Code, response.Header())
			}
		})
	}
	if want := []string{
		"GET /api",
		"POST /api/session?refresh=true",
		"GET /files",
		"HEAD /files/reports%20and%20notes.txt",
		"GET /thumbnails",
		"GET /thumbnails/cover.webp",
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

func TestFSProductionHandlerServesOnlyTheFSShellAndEmbeddedAssets(t *testing.T) {
	handler, err := NewFSProductionHandler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatalf("NewFSProductionHandler returned an error: %v", err)
	}
	site, err := Load("fs")
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	for _, path := range []string{"/", "/browse", "/browse/docs/examples"} {
		t.Run("shell "+path, func(t *testing.T) {
			assertShell(t, routeResponse(handler, http.MethodGet, path), "HACKYCY CLI - FILE BROWSER", fsContentSecurityPolicy)
		})
	}
	assertHeadShell(t, routeResponse(handler, http.MethodHead, "/browse/docs"), fsContentSecurityPolicy)
	assertAsset(t, routeResponse(handler, http.MethodGet, "/"+firstAsset(t, site.files)))
	assertHeadAsset(t, routeResponse(handler, http.MethodHead, "/"+firstAsset(t, site.files)))
	assertRouteError(t, routeResponse(handler, http.MethodPost, "/browse"), http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/not-fs"), http.StatusNotFound, "NOT_FOUND", "Route not found")
	assertRouteError(t, routeResponse(handler, http.MethodGet, "/assets/missing.js"), http.StatusNotFound, "NOT_FOUND", "Route not found")
}

func TestFSProductionHandlerRequiresAnAdapter(t *testing.T) {
	if handler, err := NewFSProductionHandler(nil); err == nil || handler != nil {
		t.Fatalf("NewFSProductionHandler(nil) = (%v, %v), want nil handler and error", handler, err)
	}
}

func TestFSProductionHandlerDoesNotRewriteAdapterResponses(t *testing.T) {
	handler, err := NewFSProductionHandler(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Cache-Control", "private, max-age=0")
		writer.Header().Set("Content-Type", "application/custom")
		writer.WriteHeader(http.StatusTeapot)
	}))
	if err != nil {
		t.Fatalf("NewFSProductionHandler returned an error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/custom", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTeapot || response.Header().Get("Cache-Control") != "private, max-age=0" || response.Header().Get("Content-Type") != "application/custom" {
		t.Fatalf("adapter response was changed: code=%d headers=%v", response.Code, response.Header())
	}
}
