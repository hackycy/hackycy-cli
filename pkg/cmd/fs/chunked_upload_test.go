package fs

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkedUploadManagerOwnsOrderedOwnerBoundPublication(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewChunkedUploadManager(workspace, 8*1024*1024)
	size := chunkedUploadThreshold + 2
	created, err := manager.Create("anonymous", mustWorkspacePath(t, ""), "large.bin", size)
	if err != nil || created.Status != "uploading" || created.UploadedBytes != 0 || created.ChunkSizeBytes != 8*1024*1024 {
		t.Fatalf("Create() = %#v, %v", created, err)
	}
	if _, err := manager.Get("other", created.ID); !serviceErrorIs(err, "CHUNKED_UPLOAD_NOT_FOUND") {
		t.Fatalf("wrong-owner Get() error = %v", err)
	}
	if _, err := manager.Append("anonymous", created.ID, 1, 1, size, strings.NewReader("x")); !serviceErrorIs(err, "CHUNKED_UPLOAD_OFFSET_MISMATCH") {
		t.Fatalf("wrong-offset Append() error = %v", err)
	}
	var offset int64
	for offset < size {
		length := manager.chunkSize
		if remaining := size - offset; remaining < length {
			length = remaining
		}
		contents := bytes.Repeat([]byte{byte('A' + offset/(8*1024*1024))}, int(length))
		current, err := manager.Append("anonymous", created.ID, offset, offset+length-1, size, bytes.NewReader(contents))
		if err != nil || current.UploadedBytes != offset+length {
			t.Fatalf("Append(%d) = %#v, %v", offset, current, err)
		}
		offset += length
	}
	completed, err := manager.Complete("anonymous", created.ID)
	if err != nil || completed.Status != "complete" || completed.Result == nil || completed.Result.Path != "large.bin" || completed.Result.Size != size {
		t.Fatalf("Complete() = %#v, %v", completed, err)
	}
	replayed, err := manager.Complete("anonymous", created.ID)
	if err != nil || replayed.Result == nil || replayed.Result.Path != "large.bin" {
		t.Fatalf("replayed Complete() = %#v, %v", replayed, err)
	}
	if _, err := workspace.OpenFile(mustWorkspacePath(t, "large.bin")); err != nil {
		t.Fatalf("published file = %v", err)
	}
}

func TestChunkedUploadManagerCloseRemovesIncompleteStaging(t *testing.T) {
	root := t.TempDir()
	workspace := openReadOnlyWorkspace(t, root)
	manager := NewChunkedUploadManager(workspace, 4*1024*1024)
	created, err := manager.Create("anonymous", mustWorkspacePath(t, ""), "large.bin", chunkedUploadThreshold+1)
	if err != nil {
		t.Fatalf("Create returned an error: %v", err)
	}
	staging := filepath.Join(root, ".upload-"+created.ID+".tmp")
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("staging file before Close: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging file after Close = %v, want absent", err)
	}
	if _, err := manager.Create("anonymous", mustWorkspacePath(t, ""), "again.bin", chunkedUploadThreshold+1); !serviceErrorIs(err, "CHUNKED_UPLOAD_STOPPED") {
		t.Fatalf("Create after Close = %v, want CHUNKED_UPLOAD_STOPPED", err)
	}
}

func TestChunkedUploadHTTPProtocolChecksOriginRangeAndCapability(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewChunkedUploadManager(workspace, 4*1024*1024)
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "example.com", ChunkedUploads: manager})
	listing := readOnlyResponse(handler, http.MethodGet, "/api/directory", nil)
	if !strings.Contains(listing.Body.String(), `"chunkedUpload":{"thresholdBytes":20971520,"chunkSizeBytes":4194304}`) {
		t.Fatalf("directory capability = %s", listing.Body.String())
	}
	created := chunkedResponse(handler, http.MethodPost, "/api/uploads", `{"directoryPath":"","filename":"large.bin","size":20971522}`, map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"})
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"status":"uploading"`) {
		t.Fatalf("creation response = %d %s", created.Code, created.Body.String())
	}
	id := extractJSONField(t, created.Body.Bytes(), "id")
	wrongRange := chunkedResponse(handler, http.MethodPut, "/api/uploads/"+id, "x", map[string]string{"Content-Type": "application/octet-stream", "Content-Range": "bytes 1-1/20971522", "Origin": "http://example.com"})
	if wrongRange.Code != http.StatusConflict {
		t.Fatalf("wrong range = %d %s", wrongRange.Code, wrongRange.Body.String())
	}
	wrongOwner := chunkedResponse(handler, http.MethodGet, "/api/uploads/00000000-0000-0000-0000-000000000000", "", nil)
	if wrongOwner.Code != http.StatusNotFound {
		t.Fatalf("missing upload = %d %s", wrongOwner.Code, wrongOwner.Body.String())
	}
}

func chunkedResponse(handler http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://example.com"+target, strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
