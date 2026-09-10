package fs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceOperationsCreateRenameCopyMoveDeleteAndKeepPartialResults(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"source", "destination"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "source", "notes.txt"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "source", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "nested", "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	created := workspace.ApplyOperation(Operation{Action: "create-directory", ParentPath: "destination", Name: "created"})
	assertOperationOK(t, created, "destination/created")
	renamed := workspace.ApplyOperation(Operation{Action: "rename", Path: "source/notes.txt", NewName: "renamed.txt"})
	assertOperationOK(t, renamed, "source/renamed.txt")
	copied := workspace.ApplyOperation(Operation{Action: "copy", Paths: []string{"source/renamed.txt", "source/nested"}, DestinationPath: "destination"})
	if len(copied.Items) != 2 || copied.Items[0].Status != "ok" || copied.Items[0].DestinationPath != "destination/renamed.txt" || copied.Items[1].Status != "ok" || copied.Items[1].DestinationPath != "destination/nested" {
		t.Fatalf("copy result = %#v", copied)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "destination", "nested", "inside.txt")); err != nil || string(contents) != "inside" {
		t.Fatalf("copied nested file = %q, %v", contents, err)
	}
	copiedAgain := workspace.ApplyOperation(Operation{Action: "copy", Paths: []string{"source/renamed.txt"}, DestinationPath: "destination"})
	assertOperationOK(t, copiedAgain, "destination/renamed (1).txt")
	moved := workspace.ApplyOperation(Operation{Action: "move", Paths: []string{"source/renamed.txt"}, DestinationPath: "destination/created"})
	assertOperationOK(t, moved, "destination/created/renamed.txt")
	partial := workspace.ApplyOperation(Operation{Action: "delete", Paths: []string{"missing.txt", "destination/created/renamed.txt"}})
	if len(partial.Items) != 2 || partial.Items[0].Status != "error" || partial.Items[1].Status != "ok" {
		t.Fatalf("partial delete = %#v", partial)
	}
	if _, err := os.Stat(filepath.Join(root, "destination", "created", "renamed.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted entry stat = %v", err)
	}
	rootFailure := workspace.ApplyOperation(Operation{Action: "delete", Paths: []string{""}})
	if rootFailure.Items[0].Error == nil || rootFailure.Items[0].Error.Code != "ROOT_IMMUTABLE" {
		t.Fatalf("root delete = %#v", rootFailure)
	}
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "outside.txt")
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "outside-link")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		deletedLink := workspace.ApplyOperation(Operation{Action: "delete", Paths: []string{"outside-link"}})
		assertOperationOK(t, deletedLink, "")
		if contents, err := os.ReadFile(outside); err != nil || string(contents) != "outside" {
			t.Fatalf("outside target = %q, %v", contents, err)
		}
	}
}

func TestWorkspaceOperationsRejectInvalidNamesAndSelfDescendants(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "folder", "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	invalid := workspace.ApplyOperation(Operation{Action: "create-directory", ParentPath: "", Name: "../invalid"})
	if invalid.Items[0].Error == nil || invalid.Items[0].Error.Code != "INVALID_NAME" {
		t.Fatalf("invalid name = %#v", invalid)
	}
	for _, action := range []string{"copy", "move"} {
		result := workspace.ApplyOperation(Operation{Action: action, Paths: []string{"folder"}, DestinationPath: "folder/child"})
		if result.Items[0].Error == nil || result.Items[0].Error.Code != "INVALID_OPERATION" {
			t.Fatalf("%s self-descendant = %#v", action, result)
		}
	}
}

func TestReadOnlyHandlerServesStrictManagementOperations(t *testing.T) {
	root := t.TempDir()
	workspace := openReadOnlyWorkspace(t, root)
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "example.com"})
	success := operationResponse(handler, `{"action":"create-directory","parentPath":"","name":"created"}`, map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"})
	if success.Code != http.StatusOK || !strings.Contains(success.Body.String(), `"destinationPath":"created"`) {
		t.Fatalf("operation success = %d %s", success.Code, success.Body.String())
	}
	for _, testCase := range []struct {
		name    string
		handler http.Handler
		body    string
		headers map[string]string
		status  int
	}{
		{name: "unknown property", handler: handler, body: `{"action":"delete","paths":["created"],"extra":true}`, headers: map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"}, status: http.StatusBadRequest},
		{name: "wrong method", handler: handler, body: "", headers: nil, status: http.StatusMethodNotAllowed},
		{name: "cross origin", handler: handler, body: `{"action":"delete","paths":["created"]}`, headers: map[string]string{"Content-Type": "application/json", "Origin": "https://attacker.example"}, status: http.StatusForbidden},
		{name: "management disabled", handler: NewReadOnlyHandler(workspace, ReadOnlyServerOptions{BindingAddress: "example.com"}), body: `{"action":"delete","paths":["created"]}`, headers: map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"}, status: http.StatusForbidden},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			method := http.MethodPost
			if testCase.name == "wrong method" {
				method = http.MethodGet
			}
			response := operationResponseMethod(testCase.handler, method, testCase.body, testCase.headers)
			if response.Code != testCase.status {
				t.Fatalf("response = %d %s, want %d", response.Code, response.Body.String(), testCase.status)
			}
		})
	}
}

func assertOperationOK(t *testing.T, result OperationResult, destination string) {
	t.Helper()
	if len(result.Items) != 1 || result.Items[0].Status != "ok" || (destination != "" && result.Items[0].DestinationPath != destination) {
		t.Fatalf("operation result = %#v", result)
	}
}

func operationResponse(handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	return operationResponseMethod(handler, http.MethodPost, body, headers)
}

func operationResponseMethod(handler http.Handler, method, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://example.com/api/operations", strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
