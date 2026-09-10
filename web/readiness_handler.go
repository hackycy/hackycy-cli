package webassets

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	diffReadinessCSP   = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; worker-src 'self'; img-src 'self' blob: data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	fsReadinessCSP     = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; worker-src 'self'; img-src 'self' blob: data:; media-src 'self'; frame-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	tunnelReadinessCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"
	readinessAPICSP    = "default-src 'none'; frame-ancestors 'none'"
)

// ReadinessHandlerOptions supplies an already-owned exact MCP handler for the
// Diff route matrix. It cannot add command APIs to the readiness harness.
type ReadinessHandlerOptions struct {
	MCP http.Handler
}

// NewFSProductionHandler joins the FS command adapter to the retained FS
// application without widening the browser shell's route ownership.
func NewFSProductionHandler(adapter http.Handler) (http.Handler, error) {
	if adapter == nil {
		return nil, fmt.Errorf("FS production handler requires an adapter")
	}
	site, err := Load("fs")
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if fsAdapterRoute(request.URL.Path) {
			adapter.ServeHTTP(writer, request)
			return
		}
		if site.ServeAsset(writer, request) {
			return
		}
		if request.URL.Path == "/" || request.URL.Path == "/browse" || strings.HasPrefix(request.URL.Path, "/browse/") {
			if request.Method != http.MethodGet && request.Method != http.MethodHead {
				writeReadinessRouteError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
				return
			}
			site.ServeShell(writer, request, fsReadinessCSP)
			return
		}
		writeReadinessRouteError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}), nil
}

// NewTunnelProductionHandler joins the Tunnel control-plane adapter to the
// retained Tunnel server application without widening shell route ownership.
func NewTunnelProductionHandler(adapter http.Handler) (http.Handler, error) {
	if adapter == nil {
		return nil, fmt.Errorf("Tunnel production handler requires an adapter")
	}
	site, err := Load("tunnel-server")
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if tunnelAdapterRoute(request.URL.Path) {
			adapter.ServeHTTP(writer, request)
			return
		}
		if site.ServeAsset(writer, request) {
			return
		}
		if tunnelShellRoute(request.URL.Path) && (request.Method == http.MethodGet || request.Method == http.MethodHead) {
			site.ServeShell(writer, request, tunnelReadinessCSP)
			return
		}
		writeReadinessRouteError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}), nil
}

func fsAdapterRoute(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/files" || strings.HasPrefix(path, "/files/") ||
		path == "/thumbnails" || strings.HasPrefix(path, "/thumbnails/")
}

func tunnelAdapterRoute(path string) bool {
	return path == "/healthz" || path == "/api" || strings.HasPrefix(path, "/api/")
}

func tunnelShellRoute(path string) bool {
	return path == "/" || path == "/clients" || strings.HasPrefix(path, "/clients/") || path == "/accounts" || path == "/server"
}

// NewReadinessHandler creates the G19 static-only route harness for one
// embedded application. Command adapters continue to own their APIs and
// lifecycle behavior, so this handler intentionally returns only route errors
// for every command namespace.
func NewReadinessHandler(application string, options ReadinessHandlerOptions) (http.Handler, error) {
	site, err := Load(application)
	if err != nil {
		return nil, err
	}
	switch application {
	case "diff":
		return diffReadinessHandler(site, options.MCP), nil
	case "fs":
		return fsReadinessHandler(site), nil
	case "tunnel-server":
		return tunnelReadinessHandler(site), nil
	default:
		return nil, fmt.Errorf("unsupported readiness application %q", application)
	}
}

func diffReadinessHandler(site *Site, mcp http.Handler) http.Handler {
	if mcp == nil {
		mcp = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writeReadinessRouteError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
		})
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") {
			writeReadinessRouteError(writer, http.StatusNotFound, "NOT_FOUND", "API route not found")
			return
		}
		if request.URL.Path == "/mcp" {
			mcp.ServeHTTP(writer, request)
			return
		}
		if site.ServeAsset(writer, request) {
			return
		}
		site.ServeShell(writer, request, diffReadinessCSP)
	})
}

func fsReadinessHandler(site *Site) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") || strings.HasPrefix(request.URL.Path, "/files/") || strings.HasPrefix(request.URL.Path, "/thumbnails/") {
			writeReadinessRouteError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
			return
		}
		if site.ServeAsset(writer, request) {
			return
		}
		if request.URL.Path == "/" || request.URL.Path == "/browse" || strings.HasPrefix(request.URL.Path, "/browse/") {
			if request.Method != http.MethodGet && request.Method != http.MethodHead {
				writeReadinessRouteError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
				return
			}
			site.ServeShell(writer, request, fsReadinessCSP)
			return
		}
		writeReadinessRouteError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
	})
}

func tunnelReadinessHandler(site *Site) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") {
			writeReadinessRouteError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
			return
		}
		if site.ServeAsset(writer, request) {
			return
		}
		if request.Method == http.MethodGet || request.Method == http.MethodHead {
			if request.URL.Path == "/" || request.URL.Path == "/clients" || strings.HasPrefix(request.URL.Path, "/clients/") || request.URL.Path == "/accounts" || request.URL.Path == "/server" {
				site.ServeShell(writer, request, tunnelReadinessCSP)
				return
			}
		}
		writeReadinessRouteError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
	})
}

func writeReadinessRouteError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", readinessAPICSP)
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"version": 1,
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
