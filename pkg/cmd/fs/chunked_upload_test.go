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

func TestChunkedUploadAppendRollsBackRejectedChunkBeforeRetry(t *testing.T) {
	root := t.TempDir()
	workspace := openReadOnlyWorkspace(t, root)
	manager := NewChunkedUploadManager(workspace, chunkedUploadThreshold+1)
	size := chunkedUploadThreshold + 1
	created, err := manager.Create("owner", mustWorkspacePath(t, ""), "large.bin", size)
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(root, ".upload-"+created.ID+".tmp")
	if _, err := manager.Append("owner", created.ID, 0, size-1, size, bytes.NewReader(bytes.Repeat([]byte{'x'}, int(size+1)))); !serviceErrorIs(err, "CHUNKED_UPLOAD_OFFSET_MISMATCH") {
		t.Fatalf("rejected chunk error = %v", err)
	}
	current, err := manager.Get("owner", created.ID)
	if err != nil || current.UploadedBytes != 0 {
		t.Fatalf("upload after rejected chunk = %#v, %v", current, err)
	}
	if info, err := os.Stat(staging); err != nil || info.Size() != 0 {
		t.Fatalf("staging after rejected chunk = size %d, err %v", info.Size(), err)
	}

	contents := bytes.Repeat([]byte{'y'}, int(size))
	if _, err := manager.Append("owner", created.ID, 0, size-1, size, bytes.NewReader(contents)); err != nil {
		t.Fatalf("valid retry error = %v", err)
	}
	completed, err := manager.Complete("owner", created.ID)
	if err != nil || completed.Status != "complete" || completed.Result == nil || completed.Result.Size != size {
		t.Fatalf("Complete() = %#v, %v", completed, err)
	}
	published, err := os.ReadFile(filepath.Join(root, "large.bin"))
	if err != nil || int64(len(published)) != size || !bytes.Equal(published, contents) {
		t.Fatalf("published file = size %d, err %v", len(published), err)
	}
}

func TestChunkedUploadAppendRollsBackReaderFailure(t *testing.T) {
	root := t.TempDir()
	workspace := openReadOnlyWorkspace(t, root)
	manager := NewChunkedUploadManager(workspace, 4*1024*1024)
	size := chunkedUploadThreshold + 1
	created, err := manager.Create("owner", mustWorkspacePath(t, ""), "large.bin", size)
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(root, ".upload-"+created.ID+".tmp")
	failed := &chunkedUploadFailingReader{remaining: bytes.Repeat([]byte{'x'}, 1024)}
	if _, err := manager.Append("owner", created.ID, 0, manager.chunkSize-1, size, failed); err == nil || !strings.Contains(err.Error(), errChunkedUploadReaderFailed.Error()) {
		t.Fatalf("reader failure = %v", err)
	}
	current, err := manager.Get("owner", created.ID)
	if err != nil || current.UploadedBytes != 0 {
		t.Fatalf("upload after reader failure = %#v, %v", current, err)
	}
	if info, err := os.Stat(staging); err != nil || info.Size() != 0 {
		t.Fatalf("staging after reader failure = size %d, err %v", info.Size(), err)
	}
	if _, err := manager.Append("owner", created.ID, 0, manager.chunkSize-1, size, bytes.NewReader(bytes.Repeat([]byte{'y'}, int(manager.chunkSize)))); err != nil {
		t.Fatalf("retry after reader failure = %v", err)
	}
}

func TestChunkedUploadAppendValidatesPublicRange(t *testing.T) {
	size := chunkedUploadThreshold + 1
	for _, test := range []struct {
		name      string
		start     int64
		end       int64
		total     int64
		chunkSize int64
	}{
		{name: "end equals total", start: 0, end: size, total: size, chunkSize: size + 1},
		{name: "start at total", start: size, end: size, total: size, chunkSize: size + 1},
		{name: "start negative", start: -1, end: 0, total: size, chunkSize: size + 1},
		{name: "range exceeds chunk", start: 0, end: size / 2, total: size, chunkSize: size / 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := NewChunkedUploadManager(openReadOnlyWorkspace(t, t.TempDir()), test.chunkSize)
			created, err := manager.Create("owner", mustWorkspacePath(t, ""), test.name+".bin", size)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Append("owner", created.ID, test.start, test.end, test.total, strings.NewReader("")); !serviceErrorIs(err, "CHUNKED_UPLOAD_OFFSET_MISMATCH") {
				t.Fatalf("Append() error = %v", err)
			}
		})
	}
}

func TestChunkedUploadCompleteChecksActualStagingSize(t *testing.T) {
	root := t.TempDir()
	workspace := openReadOnlyWorkspace(t, root)
	manager := NewChunkedUploadManager(workspace, chunkedUploadThreshold+1)
	size := chunkedUploadThreshold + 1
	created, err := manager.Create("owner", mustWorkspacePath(t, ""), "large.bin", size)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Append("owner", created.ID, 0, size-1, size, bytes.NewReader(bytes.Repeat([]byte{'x'}, int(size)))); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(root, ".upload-"+created.ID+".tmp")
	file, err := os.OpenFile(staging, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{'z'}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Complete("owner", created.ID); !serviceErrorIs(err, "CHUNKED_UPLOAD_INCOMPLETE") {
		t.Fatalf("Complete() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "large.bin")); !os.IsNotExist(err) {
		t.Fatalf("published file after actual-size mismatch = %v", err)
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

func TestChunkedUploadHTTPUnknownContentLengthUsesAppendLimit(t *testing.T) {
	root := t.TempDir()
	workspace := openReadOnlyWorkspace(t, root)
	manager := NewChunkedUploadManager(workspace, 4*1024*1024)
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "example.com", ChunkedUploads: manager})
	created := chunkedResponse(handler, http.MethodPost, "/api/uploads", `{"directoryPath":"","filename":"large.bin","size":20971521}`, map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"})
	if created.Code != http.StatusCreated {
		t.Fatalf("creation response = %d %s", created.Code, created.Body.String())
	}
	id := extractJSONField(t, created.Body.Bytes(), "id")
	chunk := bytes.Repeat([]byte{'x'}, int(manager.chunkSize+1))
	request := httptest.NewRequest(http.MethodPut, "http://example.com/api/uploads/"+id, bytes.NewReader(chunk))
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Content-Range", "bytes 0-4194303/20971521")
	request.Header.Set("Origin", "http://example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("unknown-length oversized chunk = %d %s", response.Code, response.Body.String())
	}
	current, err := manager.Get("anonymous", id)
	if err != nil || current.UploadedBytes != 0 {
		t.Fatalf("upload after unknown-length oversized chunk = %#v, %v", current, err)
	}
	valid := httptest.NewRequest(http.MethodPut, "http://example.com/api/uploads/"+id, bytes.NewReader(chunk[:manager.chunkSize]))
	valid.ContentLength = -1
	valid.Header.Set("Content-Type", "application/octet-stream")
	valid.Header.Set("Content-Range", "bytes 0-4194303/20971521")
	valid.Header.Set("Origin", "http://example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, valid)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"uploadedBytes":4194304`) {
		t.Fatalf("unknown-length valid chunk = %d %s", response.Code, response.Body.String())
	}
}

var errChunkedUploadReaderFailed = errors.New("reader failed")

type chunkedUploadFailingReader struct {
	remaining []byte
}

func (reader *chunkedUploadFailingReader) Read(buffer []byte) (int, error) {
	if len(reader.remaining) == 0 {
		return 0, errChunkedUploadReaderFailed
	}
	count := copy(buffer, reader.remaining)
	reader.remaining = reader.remaining[count:]
	return count, nil
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
