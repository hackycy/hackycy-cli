package fs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerHandlerComposesTheFSProtocolAndEmbeddedShell(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	handler, err := NewServerHandler(workspace, ReadOnlyServerOptions{BindingAddress: "127.0.0.1"})
	if err != nil {
		t.Fatalf("NewServerHandler returned an error: %v", err)
	}

	directory := httptest.NewRecorder()
	handler.ServeHTTP(directory, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/directory?path=", nil))
	if directory.Code != http.StatusOK || directory.Header().Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" || !strings.Contains(directory.Body.String(), `"name":"hello.txt"`) {
		t.Fatalf("directory response = code=%d headers=%v body=%s", directory.Code, directory.Header(), directory.Body.String())
	}

	original := httptest.NewRecorder()
	handler.ServeHTTP(original, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/files/hello.txt", nil))
	if original.Code != http.StatusOK || original.Body.String() != "hello" {
		t.Fatalf("original response = code=%d body=%q", original.Code, original.Body.String())
	}

	shell := httptest.NewRecorder()
	handler.ServeHTTP(shell, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/browse", nil))
	if shell.Code != http.StatusOK || shell.Header().Get("Content-Security-Policy") != "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; worker-src 'self'; img-src 'self' blob: data:; media-src 'self'; frame-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'" || !strings.Contains(shell.Body.String(), "HACKYCY CLI - FILE BROWSER") {
		t.Fatalf("shell response = code=%d headers=%v body=%q", shell.Code, shell.Header(), shell.Body.String())
	}
}

func TestServerHandlerRequiresAWorkspace(t *testing.T) {
	if handler, err := NewServerHandler(nil, ReadOnlyServerOptions{}); err == nil || handler != nil {
		t.Fatalf("NewServerHandler(nil) = (%v, %v), want nil handler and error", handler, err)
	}
}
