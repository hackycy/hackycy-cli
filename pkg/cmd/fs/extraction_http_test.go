package fs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractionHTTPControlsUseStrictManagementAndOriginGates(t *testing.T) {
	manager := newExtractionManager(func(context.Context, WorkspacePath, ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		return ArchiveExtractionResult{Inspection: ArchiveInspection{UncompressedBytes: 2, EntryCount: 1}, Destination: mustWorkspacePath(t, "published")}, nil
	})
	handler := NewReadOnlyHandler(openReadOnlyWorkspace(t, t.TempDir()), ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "127.0.0.1", Extractions: manager})
	for _, test := range []struct {
		name    string
		method  string
		path    string
		body    string
		headers map[string]string
		status  int
		code    string
	}{
		{name: "list", method: http.MethodGet, path: "/api/extractions", status: http.StatusOK},
		{name: "missing origin", method: http.MethodPost, path: "/api/extractions", body: `{"paths":["archive.zip"]}`, headers: map[string]string{"Content-Type": "application/json"}, status: http.StatusForbidden, code: "ORIGIN_FORBIDDEN"},
		{name: "wrong content type", method: http.MethodPost, path: "/api/extractions", body: `{"paths":["archive.zip"]}`, headers: fsRequestHeaders("text/plain"), status: http.StatusUnsupportedMediaType, code: "UNSUPPORTED_MEDIA_TYPE"},
		{name: "extra field", method: http.MethodPost, path: "/api/extractions", body: `{"paths":["archive.zip"],"extra":true}`, headers: fsRequestHeaders("application/json"), status: http.StatusBadRequest, code: "INVALID_EXTRACTION"},
		{name: "invalid paths", method: http.MethodPost, path: "/api/extractions", body: `{"paths":[]}`, headers: fsRequestHeaders("application/json"), status: http.StatusBadRequest, code: "INVALID_EXTRACTION"},
		{name: "clear requires terminal", method: http.MethodDelete, path: "/api/extractions", headers: fsRequestHeaders(""), status: http.StatusBadRequest, code: "INVALID_EXTRACTION"},
		{name: "missing task", method: http.MethodPost, path: "/api/extractions/missing/cancel", headers: fsRequestHeaders(""), status: http.StatusNotFound, code: "EXTRACTION_NOT_FOUND"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Host = "127.0.0.1:1204"
			for key, value := range test.headers {
				request.Header.Set(key, value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if test.code != "" && extractionHTTPErrorCode(t, response) != test.code {
				t.Fatalf("error code = %q, want %q", extractionHTTPErrorCode(t, response), test.code)
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/api/extractions", strings.NewReader(`{"paths":["archive.zip"]}`))
	request.Host = "127.0.0.1:1204"
	for key, value := range fsRequestHeaders("application/json") {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create status = %d: %s", response.Code, response.Body.String())
	}
	var created struct {
		Tasks []ExtractionTask `json:"tasks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || len(created.Tasks) != 1 {
		t.Fatalf("create response = %s, %v", response.Body.String(), err)
	}
	clear := httptest.NewRequest(http.MethodDelete, "/api/extractions?terminal=1", nil)
	clear.Host = "127.0.0.1:1204"
	clear.Header.Set("Origin", "http://127.0.0.1:1204")
	cleared := httptest.NewRecorder()
	handler.ServeHTTP(cleared, clear)
	if cleared.Code != http.StatusNoContent || cleared.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("clear response = %d, %#v", cleared.Code, cleared.Header())
	}
}

func TestExtractionHTTPIsAbsentOrDisabledWithoutItsOwner(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	for _, test := range []struct {
		options ReadOnlyServerOptions
		status  int
	}{
		{options: ReadOnlyServerOptions{}, status: http.StatusNotFound},
		{options: ReadOnlyServerOptions{ManagementEnabled: false, Extractions: newExtractionManager(nil)}, status: http.StatusForbidden},
	} {
		response := httptest.NewRecorder()
		NewReadOnlyHandler(workspace, test.options).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/extractions", nil))
		if response.Code != test.status {
			t.Fatalf("status = %d, want %d", response.Code, test.status)
		}
	}
}

func fsRequestHeaders(contentType string) map[string]string {
	headers := map[string]string{"Origin": "http://127.0.0.1:1204"}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	return headers
}

func extractionHTTPErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var value struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value.Error.Code
}
