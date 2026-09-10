package fs

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceSaveTextPreservesEncodingLineEndingModeAndRevision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte{0xef, 0xbb, 0xbf, 'o', 'n', 'e', '\r', '\n', 't', 'w', 'o', '\r', '\n'}, 0o640); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	preview, err := workspace.ReadText(mustWorkspacePath(t, "notes.txt"))
	if err != nil || preview.Status != "ready" || preview.Encoding != "utf-8" {
		t.Fatalf("ReadText() = %#v, %v", preview, err)
	}
	result, err := workspace.SaveText(mustWorkspacePath(t, "notes.txt"), "replaced\n\n", preview.Revision)
	if err != nil || result.Encoding != "utf-8" || result.Size != int64(len([]byte{0xef, 0xbb, 0xbf})+len("replaced\r\n")) || len(result.Revision) != 64 {
		t.Fatalf("SaveText() = %#v, %v", result, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(contents, []byte{0xef, 0xbb, 0xbf, 'r', 'e', 'p', 'l', 'a', 'c', 'e', 'd', '\r', '\n'}) {
		t.Fatalf("saved bytes = %q, %v", contents, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("saved mode = %v, %v", info.Mode(), err)
	}
	if runtime.GOOS == "windows" {
		if info.Mode().Perm()&0o200 == 0 {
			t.Fatalf("saved Windows file is read-only: %v", info.Mode())
		}
	} else if info.Mode().Perm() != 0o640 {
		t.Fatalf("saved mode = %v, want 0640", info.Mode())
	}
	if _, err := workspace.SaveText(mustWorkspacePath(t, "notes.txt"), "stale", preview.Revision); !serviceErrorIs(err, "REVISION_MISMATCH") {
		t.Fatalf("stale SaveText() error = %v", err)
	}
}

func TestWorkspaceSaveTextPreservesUTF16AndRejectsUnsupportedSources(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "utf16.txt"), []byte{0xff, 0xfe, 'o', 0, 'l', 0, 'd', 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.txt"), []byte{0xc3, 0x28}, 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	preview, err := workspace.ReadText(mustWorkspacePath(t, "utf16.txt"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := workspace.SaveText(mustWorkspacePath(t, "utf16.txt"), "new", preview.Revision)
	if err != nil || result.Encoding != "utf-16le" {
		t.Fatalf("SaveText(utf16) = %#v, %v", result, err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "utf16.txt")); err != nil || !bytes.Equal(contents, []byte{0xff, 0xfe, 'n', 0, 'e', 0, 'w', 0}) {
		t.Fatalf("utf16 bytes = %q, %v", contents, err)
	}
	if _, err := workspace.SaveText(mustWorkspacePath(t, "binary.txt"), "updated", "irrelevant"); !serviceErrorIs(err, "UNSUPPORTED_TEXT") {
		t.Fatalf("SaveText(binary) error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("utf16.txt", filepath.Join(root, "linked.txt")); err != nil {
			t.Fatal(err)
		}
		if _, err := workspace.SaveText(mustWorkspacePath(t, "linked.txt"), "updated", "irrelevant"); !errors.Is(err, ErrWorkspacePathNotFile) {
			t.Fatalf("SaveText(symlink) error = %v", err)
		}
	}
}

func TestReadOnlyHandlerConditionallySavesText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "example.com"})
	preview := readOnlyResponse(handler, http.MethodGet, "/api/text?path=notes.txt", nil)
	revision := extractJSONField(t, preview.Body.Bytes(), "revision")
	saved := textSaveResponse(handler, "updated\n\n", map[string]string{
		"Content-Type": "text/plain; charset=utf-8",
		"Origin":       "http://example.com",
		"If-Match":     revision,
	})
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"encoding":"utf-8"`) {
		t.Fatalf("save response = %d %s", saved.Code, saved.Body.String())
	}
	if contents, err := os.ReadFile(filepath.Join(root, "notes.txt")); err != nil || string(contents) != "updated\n" {
		t.Fatalf("saved contents = %q, %v", contents, err)
	}
	for _, testCase := range []struct {
		name    string
		handler http.Handler
		headers map[string]string
		status  int
	}{
		{name: "management disabled", handler: NewReadOnlyHandler(workspace, ReadOnlyServerOptions{BindingAddress: "example.com"}), headers: map[string]string{"Content-Type": "text/plain", "Origin": "http://example.com", "If-Match": revision}, status: http.StatusForbidden},
		{name: "cross origin", handler: handler, headers: map[string]string{"Content-Type": "text/plain", "Origin": "https://attacker.example", "If-Match": revision}, status: http.StatusForbidden},
		{name: "missing revision", handler: handler, headers: map[string]string{"Content-Type": "text/plain", "Origin": "http://example.com"}, status: http.StatusPreconditionRequired},
		{name: "wrong media type", handler: handler, headers: map[string]string{"Content-Type": "application/json", "Origin": "http://example.com", "If-Match": revision}, status: http.StatusUnsupportedMediaType},
		{name: "stale revision", handler: handler, headers: map[string]string{"Content-Type": "text/plain", "Origin": "http://example.com", "If-Match": revision}, status: http.StatusPreconditionFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := textSaveResponse(testCase.handler, "blocked", testCase.headers)
			if response.Code != testCase.status {
				t.Fatalf("response = %d %s, want %d", response.Code, response.Body.String(), testCase.status)
			}
		})
	}
}

func textSaveResponse(handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPut, "http://example.com/api/text?path=notes.txt", strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func serviceErrorIs(err error, code string) bool {
	var service *ServiceError
	return errors.As(err, &service) && service.Code == code
}

func extractJSONField(t *testing.T, bytes []byte, key string) string {
	t.Helper()
	needle := `"` + key + `":"`
	start := strings.Index(string(bytes), needle)
	if start < 0 {
		t.Fatalf("JSON field %q absent from %s", key, bytes)
	}
	rest := string(bytes[start+len(needle):])
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("JSON field %q malformed in %s", key, bytes)
	}
	return rest[:end]
}
