package fs

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
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

func TestChunkedPublicationReplaysSuccessAndRetriesStagingCleanup(t *testing.T) {
	for _, retry := range []string{"replay", "expiry", "close"} {
		t.Run(retry, func(t *testing.T) {
			root := t.TempDir()
			workspace := openReadOnlyWorkspace(t, root)
			failure := &publicationFailureRoot{workspaceRoot: workspace.root, removeErr: errors.New("cleanup failed")}
			workspace.root = failure
			var output bytes.Buffer
			logger := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
			now := time.Now()
			lifecycle := newFSLifecycle(logger.Logger("fs"), func() time.Time { return now })
			workspace.stagingCleanupWarning = lifecycle.stagingCleanupFailed
			lifecycle.begin(Startup{BindingAddress: "127.0.0.1"})
			lifecycle.commitStartup()
			manager := newChunkedUploadManager(workspace, 32*1024*1024, func() time.Time { return now }, lifecycle)
			t.Cleanup(func() { _ = manager.Close() })
			size := chunkedUploadThreshold + 1
			created, err := manager.Create("owner", mustWorkspacePath(t, ""), "large.bin", size)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Append("owner", created.ID, 0, size-1, size, bytes.NewReader(bytes.Repeat([]byte("x"), int(size)))); err != nil {
				t.Fatal(err)
			}
			completed, err := manager.Complete("owner", created.ID)
			if err != nil || completed.Status != "complete" || completed.Result == nil || completed.Result.Path != "large.bin" {
				t.Fatalf("Complete() = %#v, %v", completed, err)
			}
			current, err := manager.Get("owner", created.ID)
			if err != nil || current.Status != "complete" {
				t.Fatalf("Get() = %#v, %v", current, err)
			}
			replayed, err := manager.Complete("owner", created.ID)
			if err != nil || replayed.Result == nil || *replayed.Result != *completed.Result || failure.linkCalls != 1 {
				t.Fatalf("replayed Complete() = %#v, %v, links = %d", replayed, err, failure.linkCalls)
			}
			if strings.Count(output.String(), "Chunked upload completed") != 1 || strings.Count(output.String(), "Staging file cleanup failed") != 2 {
				t.Fatalf("completion or cleanup warning events = %s", output.String())
			}
			staging := filepath.Join(root, ".upload-"+created.ID+".tmp")
			if _, err := os.Stat(staging); err != nil {
				t.Fatal(err)
			}
			failure.removeErr = nil
			switch retry {
			case "replay":
				if _, err := manager.Complete("owner", created.ID); err != nil {
					t.Fatal(err)
				}
			case "expiry":
				now = now.Add(6 * time.Minute)
				if _, err := manager.Get("owner", created.ID); !serviceErrorIs(err, "CHUNKED_UPLOAD_NOT_FOUND") {
					t.Fatalf("expired Get() = %v", err)
				}
			case "close":
				if removed, err := manager.closeWithStats(); err != nil || removed != 0 {
					t.Fatalf("closeWithStats() = %d, %v", removed, err)
				}
			}
			if _, err := os.Stat(staging); !os.IsNotExist(err) {
				t.Fatalf("staging file remained after retry: %v", err)
			}
			contents, err := os.ReadFile(filepath.Join(root, "large.bin"))
			if err != nil || int64(len(contents)) != size || bytes.Count(contents, []byte("x")) != int(size) {
				t.Fatalf("final file changed: size = %d, err = %v", len(contents), err)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 || failure.linkCalls != 1 {
				t.Fatalf("publication was repeated: %v, %v, links = %d", entries, err, failure.linkCalls)
			}
		})
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
