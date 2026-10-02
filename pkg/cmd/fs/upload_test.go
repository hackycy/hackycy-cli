package fs

import (
	"bytes"
	"errors"
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

func TestWorkspacePublicationKeepsSuccessWhenStagingCleanupFails(t *testing.T) {
	for _, test := range []struct {
		name    string
		publish func(*Workspace) (UploadResult, error)
	}{
		{"upload", func(workspace *Workspace) (UploadResult, error) {
			return workspace.Upload(mustWorkspacePath(t, ""), "notes.txt", strings.NewReader("published"))
		}},
		{"download", func(workspace *Workspace) (UploadResult, error) {
			return workspace.Download(mustWorkspacePath(t, ""), "notes.txt", strings.NewReader("published"), nil)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			workspace := openReadOnlyWorkspace(t, root)
			if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			failure := &publicationFailureRoot{workspaceRoot: workspace.root, removeErr: errors.New("cleanup failed")}
			workspace.root = failure
			warnings := 0
			workspace.stagingCleanupWarning = func(WorkspacePath) { warnings++ }
			result, err := test.publish(workspace)
			if err != nil || result.Path != "notes (1).txt" || result.Size != 9 || warnings != 1 {
				t.Fatalf("publication = %#v, %v, warnings = %d", result, err, warnings)
			}
			for name, want := range map[string]string{"notes.txt": "original", result.Path: "published"} {
				if got, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(got) != want {
					t.Fatalf("%s = %q, %v", name, got, err)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 3 {
				t.Fatalf("expected two final files and a retained staging file: %v, %v", entries, err)
			}
		})
	}
}

func TestWorkspacePublicationReportsLinkFailureAndRemovesStaging(t *testing.T) {
	root := t.TempDir()
	workspace := openReadOnlyWorkspace(t, root)
	workspace.root = &publicationFailureRoot{workspaceRoot: workspace.root, linkErr: errors.New("link failed")}
	result, err := workspace.Upload(mustWorkspacePath(t, ""), "notes.txt", strings.NewReader("unpublished"))
	if !errors.Is(err, ErrWorkspaceUnavailable) || result != (UploadResult{}) {
		t.Fatalf("Upload() = %#v, %v", result, err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("failed publication left files: %v, %v", entries, err)
	}
}

type publicationFailureRoot struct {
	workspaceRoot
	removeErr error
	linkErr   error
	linkCalls int
}

func (root *publicationFailureRoot) Remove(name string) error {
	if root.removeErr != nil {
		return root.removeErr
	}
	return root.workspaceRoot.Remove(name)
}

func (root *publicationFailureRoot) Link(source, destination string) error {
	root.linkCalls++
	if root.linkErr != nil {
		return root.linkErr
	}
	return root.workspaceRoot.Link(source, destination)
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
