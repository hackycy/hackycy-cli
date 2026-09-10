package fs

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceUploadStagesAndPublishesWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	workspace := openReadOnlyWorkspace(t, root)
	first, err := workspace.Upload(mustWorkspacePath(t, ""), "notes.txt", strings.NewReader("first"))
	if err != nil || first.Filename != "notes.txt" || first.Path != "notes.txt" || first.Size != 5 {
		t.Fatalf("first Upload() = %#v, %v", first, err)
	}
	second, err := workspace.Upload(mustWorkspacePath(t, ""), "notes.txt", strings.NewReader("second"))
	if err != nil || second.Filename != "notes (1).txt" || second.Path != "notes (1).txt" {
		t.Fatalf("second Upload() = %#v, %v", second, err)
	}
	for name, want := range map[string]string{"notes.txt": "first", "notes (1).txt": "second"} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(contents) != want {
			t.Fatalf("%s = %q, %v", name, contents, err)
		}
	}
	for _, name := range []string{"", "../outside", "nested/name", "\\windows"} {
		if _, err := workspace.Upload(mustWorkspacePath(t, ""), name, strings.NewReader("bad")); !serviceErrorIs(err, "INVALID_UPLOAD") {
			t.Fatalf("Upload(%q) error = %v", name, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Fatalf("staging file remained: %q", entry.Name())
		}
	}
}

func TestReadOnlyHandlerUploadsMultipartFileWhenManaged(t *testing.T) {
	root := t.TempDir()
	handler := NewReadOnlyHandler(openReadOnlyWorkspace(t, root), ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "example.com"})
	success := uploadResponse(handler, "notes.txt", "uploaded", map[string]string{"Origin": "http://example.com"})
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"filename":"notes.txt"`) || !strings.Contains(success.Body.String(), `"size":8`) {
		t.Fatalf("upload response = %d %s", success.Code, success.Body.String())
	}
	if contents, err := os.ReadFile(filepath.Join(root, "notes.txt")); err != nil || string(contents) != "uploaded" {
		t.Fatalf("uploaded contents = %q, %v", contents, err)
	}
	for _, testCase := range []struct {
		name    string
		handler http.Handler
		headers map[string]string
		status  int
	}{
		{name: "cross origin", handler: handler, headers: map[string]string{"Origin": "https://attacker.example"}, status: http.StatusForbidden},
		{name: "management disabled", handler: NewReadOnlyHandler(openReadOnlyWorkspace(t, t.TempDir()), ReadOnlyServerOptions{BindingAddress: "example.com"}), headers: map[string]string{"Origin": "http://example.com"}, status: http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := uploadResponse(testCase.handler, "ignored.txt", "ignored", testCase.headers)
			if response.Code != testCase.status {
				t.Fatalf("response = %d %s, want %d", response.Code, response.Body.String(), testCase.status)
			}
		})
	}
	missing := httptest.NewRequest(http.MethodPost, "http://example.com/api/upload?path=", strings.NewReader("not multipart"))
	missing.Header.Set("Origin", "http://example.com")
	missing.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, missing)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong type response = %d %s", response.Code, response.Body.String())
	}
}

func uploadResponse(handler http.Handler, filename, contents string, headers map[string]string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("ignored", "field")
	part, _ := writer.CreateFormFile("file", filename)
	_, _ = part.Write([]byte(contents))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "http://example.com/api/upload?path=", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
