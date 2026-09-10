package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDownloadManagerValidatesTargetsAndPublishesQueuedResult(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewDownloadManager(workspace)
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Length": []string{"3"}, "Content-Disposition": []string{`attachment; filename="remote.bin"`}}, Body: io.NopCloser(bytes.NewReader([]byte{7, 8, 9}))}, nil
	})}
	if _, err := manager.Enqueue(DownloadRequest{URL: "http://localhost/blocked", DirectoryPath: ""}); !serviceErrorIs(err, "URL_FORBIDDEN") {
		t.Fatalf("localhost Enqueue() error = %v", err)
	}
	task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: "", Filename: "requested.bin"})
	if err != nil || task.Status != "running" && task.Status != "queued" {
		t.Fatalf("Enqueue() = %#v, %v", task, err)
	}
	finished := waitDownload(t, manager, task.ID)
	if finished.Status != "done" || finished.Filename != "requested.bin" || finished.DestinationPath != "requested.bin" || finished.BytesDownloaded != 3 {
		t.Fatalf("finished task = %#v", finished)
	}
	manager.ClearTerminal()
	if len(manager.List()) != 0 {
		t.Fatalf("ClearTerminal() left %#v", manager.List())
	}
}

func TestDownloadUsesDedicatedStagingAndResponseMetadata(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	result, err := workspace.Download(mustWorkspacePath(t, ""), "remote.bin", strings.NewReader("download"), nil)
	if err != nil || result.Path != "remote.bin" || result.Size != 8 {
		t.Fatalf("Download() = %#v, %v", result, err)
	}
	for _, name := range []string{".download-550e8400-e29b-41d4-a716-446655440000.tmp", ".upload-550e8400-e29b-41d4-a716-446655440000.tmp"} {
		if _, err := workspace.OpenFile(mustWorkspacePath(t, name)); err == nil {
			t.Fatalf("staging entry %q remained", name)
		}
	}
	for _, test := range []struct {
		name        string
		disposition string
		location    string
		want        string
	}{
		{name: "utf8 extended wins", disposition: `attachment; filename="plain.txt"; filename*=UTF-8''report%20%E7%9B%AE%E5%BD%95.txt`, location: "https://example.test/fallback.bin", want: "report 目录.txt"},
		{name: "plain path is stripped", disposition: `attachment; filename="nested\\report.txt"`, location: "https://example.test/fallback.bin", want: "report.txt"},
		{name: "malformed extended falls back to URL", disposition: `attachment; filename="plain.txt"; filename*=UTF-8''bad%zz`, location: "https://example.test/fallback%20name.bin", want: "fallback name.bin"},
		{name: "url fallback", location: "https://example.test/downloads/report%20name.txt", want: "report name.txt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := &http.Response{Header: http.Header{"Content-Disposition": {test.disposition}}}
			if got := responseFilename(response, mustDownloadURL(t, test.location)); got != test.want {
				t.Fatalf("responseFilename() = %q, want %q", got, test.want)
			}
		})
	}
	for _, test := range []struct {
		name   string
		header http.Header
		want   *int64
		code   string
	}{
		{name: "safe content length", header: http.Header{"Content-Length": {"4"}}, want: int64Pointer(4)},
		{name: "encoded response omits length", header: http.Header{"Content-Length": {"4"}, "Content-Encoding": {"gzip"}}},
		{name: "unsafe content length fails", header: http.Header{"Content-Length": {"9007199254740992"}}, code: "DOWNLOAD_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := responseSize(&http.Response{Header: test.header})
			if test.code != "" {
				if !serviceErrorIs(err, test.code) {
					t.Fatalf("responseSize() error = %v", err)
				}
				return
			}
			if err != nil || (got == nil) != (test.want == nil) || got != nil && *got != *test.want {
				t.Fatalf("responseSize() = %v, %v; want %v, nil", got, err, test.want)
			}
		})
	}
}

func TestDownloadTimeoutsAndRejectsMalformedRedirects(t *testing.T) {
	t.Run("headers", func(t *testing.T) {
		workspace := openReadOnlyWorkspace(t, t.TempDir())
		manager := NewDownloadManager(workspace)
		manager.headerTimeout = 10 * time.Millisecond
		manager.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})}
		task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""})
		if err != nil {
			t.Fatal(err)
		}
		if finished := waitDownload(t, manager, task.ID); finished.Status != "error" {
			t.Fatalf("header timeout task = %#v", finished)
		}
	})
	t.Run("body idle", func(t *testing.T) {
		workspace := openReadOnlyWorkspace(t, t.TempDir())
		manager := NewDownloadManager(workspace)
		manager.idleTimeout = 10 * time.Millisecond
		body := &stalledDownloadBody{closed: make(chan struct{})}
		manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Disposition": {`attachment; filename="remote.bin"`}}, Body: body}, nil
		})}
		task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""})
		if err != nil {
			t.Fatal(err)
		}
		if finished := waitDownload(t, manager, task.ID); finished.Status != "error" || !body.wasClosed() {
			t.Fatalf("idle timeout task = %#v, closed = %t", finished, body.wasClosed())
		}
	})
	t.Run("malformed redirect", func(t *testing.T) {
		workspace := openReadOnlyWorkspace(t, t.TempDir())
		manager := NewDownloadManager(workspace)
		manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"%"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""})
		if err != nil {
			t.Fatal(err)
		}
		if finished := waitDownload(t, manager, task.ID); finished.Status != "error" {
			t.Fatalf("malformed redirect task = %#v", finished)
		}
	})
}

func TestDownloadKeepsResponseContextOpenUntilTheBodyFinishes(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewDownloadManager(workspace)
	manager.headerTimeout = 10 * time.Millisecond
	manager.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Disposition": {`attachment; filename="remote.bin"`}},
			Body:       &contextDownloadBody{context: request.Context(), reader: strings.NewReader("download")},
		}, nil
	})}
	task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""})
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitDownload(t, manager, task.ID); finished.Status != "done" || finished.BytesDownloaded != int64(len("download")) {
		t.Fatalf("download task = %#v", finished)
	}
}

func TestDownloadProgressIsThrottledAndForcedAtStateBoundaries(t *testing.T) {
	manager := NewDownloadManager(openReadOnlyWorkspace(t, t.TempDir()))
	manager.progressInterval = time.Hour
	total := int64(10)
	task := &DownloadTask{TotalBytes: &total}
	started := time.Now().Add(-time.Second)
	manager.updateProgress(task, 1, started, false)
	manager.updateProgress(task, 2, started, false)
	if task.BytesDownloaded != 1 {
		t.Fatalf("throttled bytes = %d, want 1", task.BytesDownloaded)
	}
	manager.updateProgress(task, 10, started, true)
	if task.BytesDownloaded != 10 || task.Progress == nil || *task.Progress != 100 || task.SpeedBytesPerSecond == nil || *task.SpeedBytesPerSecond <= 0 {
		t.Fatalf("forced progress task = %#v", task)
	}
}

func TestDownloadLifecycleCancelsStreamsAndRetriesTerminalTasks(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewDownloadManager(workspace)
	bodyStarted := make(chan *stalledDownloadBody, 1)
	requests := 0
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			body := &stalledDownloadBody{closed: make(chan struct{})}
			bodyStarted <- body
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Disposition": {`attachment; filename="remote.bin"`}}, Body: body}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("retried"))}, nil
	})}
	task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""})
	if err != nil {
		t.Fatal(err)
	}
	body := <-bodyStarted
	cancelled, err := manager.Cancel(task.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("Cancel() = %#v, %v", cancelled, err)
	}
	waitDownloadManagerIdle(t, manager)
	if finished := waitDownload(t, manager, task.ID); finished.Status != "cancelled" || !body.wasClosed() {
		t.Fatalf("cancelled task = %#v, body closed = %t", finished, body.wasClosed())
	}
	retried, err := manager.Retry(task.ID)
	if err != nil || retried.ID == task.ID {
		t.Fatalf("Retry() = %#v, %v", retried, err)
	}
	if finished := waitDownload(t, manager, retried.ID); finished.Status != "done" || finished.DestinationPath != "remote.bin" {
		t.Fatalf("retried task = %#v", finished)
	}
}

func TestDownloadQueueCapacityCancellationAndTerminalHistory(t *testing.T) {
	t.Run("queue slots", func(t *testing.T) {
		workspace := openReadOnlyWorkspace(t, t.TempDir())
		manager := NewDownloadManager(workspace)
		bodyStarted := make(chan *stalledDownloadBody, 2)
		manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			body := &stalledDownloadBody{closed: make(chan struct{})}
			bodyStarted <- body
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Disposition": {`attachment; filename="remote.bin"`}}, Body: body}, nil
		})}
		for range 2 {
			if _, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""}); err != nil {
				t.Fatal(err)
			}
		}
		firstBody, secondBody := <-bodyStarted, <-bodyStarted
		queued := make([]DownloadTask, 0, maxQueuedDownloads)
		for index := range maxQueuedDownloads {
			task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: "", Filename: fmt.Sprintf("queued-%d", index)})
			if err != nil {
				t.Fatalf("queue task %d: %v", index, err)
			}
			queued = append(queued, task)
		}
		if _, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""}); !serviceErrorIs(err, "DOWNLOAD_QUEUE_FULL") {
			t.Fatalf("full queue error = %v", err)
		}
		if cancelled, err := manager.Cancel(queued[0].ID); err != nil || cancelled.Status != "cancelled" {
			t.Fatalf("Cancel(queued) = %#v, %v", cancelled, err)
		}
		if _, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""}); err != nil {
			t.Fatalf("enqueue after queued cancellation: %v", err)
		}
		manager.Close()
		waitDownloadManagerIdle(t, manager)
		if !firstBody.wasClosed() || !secondBody.wasClosed() {
			t.Fatalf("Close() did not abort active download bodies")
		}
	})
	t.Run("oldest terminal is pruned and snapshots are newest first", func(t *testing.T) {
		manager := NewDownloadManager(openReadOnlyWorkspace(t, t.TempDir()))
		manager.mu.Lock()
		for index := range maxDownloadTasks + 2 {
			id := fmt.Sprintf("task-%03d", index)
			manager.tasks[id] = &DownloadTask{ID: id, Status: "done", CreatedAt: "2026-08-24T00:00:00.000Z", order: uint64(index)}
		}
		manager.pruneTerminalLocked()
		manager.mu.Unlock()
		tasks := manager.List()
		if len(tasks) != maxDownloadTasks || tasks[0].ID != "task-101" || tasks[len(tasks)-1].ID != "task-002" {
			t.Fatalf("pruned task snapshots = %#v", tasks)
		}
	})
}

func TestDownloadProtocolBoundaryValidation(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewDownloadManager(workspace)
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "example.com", Downloads: manager})
	for _, body := range []string{
		`{"url":"https://example.test/file"}`,
		`{"url":"https://example.test/file","directoryPath":"","filename":null}`,
		`{"url":"https://example.test/file","directoryPath":"","extra":true}`,
	} {
		response := downloadResponse(handler, http.MethodPost, body, map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("strict body %s returned %d", body, response.Code)
		}
	}
	if _, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: strings.Repeat("a", maxDownloadPathLength+1)}); !serviceErrorIs(err, "INVALID_DOWNLOAD") {
		t.Fatalf("long directory error = %v", err)
	}
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("remote unavailable")
	})}
	task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: "", Filename: " remote.bin "})
	if err != nil || task.Filename != "remote.bin" {
		t.Fatalf("normalized filename task = %#v, %v", task, err)
	}
	_ = waitDownload(t, manager, task.ID)
	manager.Close()

	for _, value := range []string{
		"https://10.0.0.1/file",
		"https://[2001:db8::1]/file",
		"http://127.1/file",
		"http://2130706433/file",
		"http://0x7f.1/file",
		"http://1.2.3.4.5/file",
		"http://example.test:65536/file",
		"ftp://example.test/file",
	} {
		if _, err := validateDownloadURL(value); err == nil {
			t.Fatalf("validateDownloadURL(%q) succeeded", value)
		}
	}
	normalized, err := validateDownloadURL("HTTP://BÜCHER.example:80/a/../report")
	if err != nil || normalized.String() != "http://xn--bcher-kva.example/report" {
		t.Fatalf("normalized URL = %v, %v", normalized, err)
	}

	manager = NewDownloadManager(workspace)
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotModified, Header: http.Header{"Location": {"https://example.test/follow"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	redirect, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""})
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitDownload(t, manager, redirect.ID); finished.Status != "error" {
		t.Fatalf("304 download task = %#v", finished)
	}

	response := httptest.NewRecorder()
	writeWorkspaceError(response, &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "remote error"})
	if response.Code != http.StatusBadGateway {
		t.Fatalf("DOWNLOAD_UNAVAILABLE status = %d", response.Code)
	}
}

func TestDownloadFollowsOnlyAllowedValidatedRedirects(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			workspace := openReadOnlyWorkspace(t, t.TempDir())
			manager := NewDownloadManager(workspace)
			requests := 0
			manager.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.URL.Path == "/start" {
					return &http.Response{StatusCode: status, Header: http.Header{"Location": {"/final"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Disposition": {`attachment; filename="remote.bin"`}}, Body: io.NopCloser(strings.NewReader("done"))}, nil
			})}
			task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/start", DirectoryPath: ""})
			if err != nil {
				t.Fatal(err)
			}
			if finished := waitDownload(t, manager, task.ID); finished.Status != "done" || requests != 2 {
				t.Fatalf("redirect task = %#v, requests = %d", finished, requests)
			}
		})
	}
	t.Run("rejects unsafe and too many redirects", func(t *testing.T) {
		workspace := openReadOnlyWorkspace(t, t.TempDir())
		manager := NewDownloadManager(workspace)
		requests := 0
		manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"http://127.1/blocked"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/start", DirectoryPath: ""})
		if err != nil {
			t.Fatal(err)
		}
		if finished := waitDownload(t, manager, task.ID); finished.Status != "error" || requests != 1 {
			t.Fatalf("unsafe redirect task = %#v, requests = %d", finished, requests)
		}

		manager = NewDownloadManager(workspace)
		requests = 0
		manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/again"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		task, err = manager.Enqueue(DownloadRequest{URL: "https://example.test/start", DirectoryPath: ""})
		if err != nil {
			t.Fatal(err)
		}
		if finished := waitDownload(t, manager, task.ID); finished.Status != "error" || requests != 6 {
			t.Fatalf("redirect limit task = %#v, requests = %d", finished, requests)
		}
	})
}

func TestDownloadHTTPControlsRetryAndClearOnlyTerminalTasks(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewDownloadManager(workspace)
	requests := 0
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return nil, errors.New("remote unavailable")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Disposition": {`attachment; filename="remote.bin"`}}, Body: io.NopCloser(strings.NewReader("done"))}, nil
	})}
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "example.com", Downloads: manager})
	task, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/file", DirectoryPath: ""})
	if err != nil {
		t.Fatal(err)
	}
	if failed := waitDownload(t, manager, task.ID); failed.Status != "error" {
		t.Fatalf("initial task = %#v", failed)
	}
	retry := httptest.NewRequest(http.MethodPost, "http://example.com/api/downloads/"+task.ID+"/retry", nil)
	retry.Header.Set("Origin", "http://example.com")
	retryResponse := httptest.NewRecorder()
	handler.ServeHTTP(retryResponse, retry)
	if retryResponse.Code != http.StatusAccepted {
		t.Fatalf("retry response = %d %s", retryResponse.Code, retryResponse.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		tasks := manager.List()
		if len(tasks) == 2 && tasks[0].ID != task.ID && tasks[0].Status == "done" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	missing := httptest.NewRequest(http.MethodPost, "http://example.com/api/downloads/missing/cancel", nil)
	missing.Header.Set("Origin", "http://example.com")
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing cancellation response = %d", missingResponse.Code)
	}
	withoutTerminal := httptest.NewRequest(http.MethodDelete, "http://example.com/api/downloads", nil)
	withoutTerminal.Header.Set("Origin", "http://example.com")
	withoutTerminalResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutTerminalResponse, withoutTerminal)
	if withoutTerminalResponse.Code != http.StatusBadRequest {
		t.Fatalf("clear without terminal response = %d", withoutTerminalResponse.Code)
	}
	clear := httptest.NewRequest(http.MethodDelete, "http://example.com/api/downloads?terminal=1", nil)
	clear.Header.Set("Origin", "http://example.com")
	clearResponse := httptest.NewRecorder()
	handler.ServeHTTP(clearResponse, clear)
	if clearResponse.Code != http.StatusNoContent || len(manager.List()) != 0 {
		t.Fatalf("clear response = %d, tasks = %#v", clearResponse.Code, manager.List())
	}
}

func int64Pointer(value int64) *int64 { return &value }

type stalledDownloadBody struct {
	closed chan struct{}
}

type contextDownloadBody struct {
	context context.Context
	reader  io.Reader
}

func (body *contextDownloadBody) Read(bytes []byte) (int, error) {
	select {
	case <-body.context.Done():
		return 0, body.context.Err()
	default:
		return body.reader.Read(bytes)
	}
}

func (*contextDownloadBody) Close() error { return nil }

func (body *stalledDownloadBody) Read([]byte) (int, error) {
	<-body.closed
	return 0, errors.New("download body closed")
}

func (body *stalledDownloadBody) Close() error {
	select {
	case <-body.closed:
	default:
		close(body.closed)
	}
	return nil
}

func (body *stalledDownloadBody) wasClosed() bool {
	select {
	case <-body.closed:
		return true
	default:
		return false
	}
}

func waitDownloadManagerIdle(t *testing.T, manager *DownloadManager) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		running := manager.running
		manager.mu.Unlock()
		if running == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("download manager did not stop active tasks")
}

func mustDownloadURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestDownloadHTTPValidatesManagementAndReturnsTaskSnapshots(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewDownloadManager(workspace)
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("done"))}, nil
	})}
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{ManagementEnabled: true, BindingAddress: "example.com", Downloads: manager})
	created := downloadResponse(handler, http.MethodPost, `{"url":"https://example.test/file","directoryPath":"","filename":"download.txt"}`, map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"})
	if created.Code != http.StatusAccepted || !strings.Contains(created.Body.String(), `"task"`) {
		t.Fatalf("create response = %d %s", created.Code, created.Body.String())
	}
	list := downloadResponse(handler, http.MethodGet, "", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"tasks"`) {
		t.Fatalf("list response = %d %s", list.Code, list.Body.String())
	}
	tasks := manager.List()
	if len(tasks) != 1 {
		t.Fatalf("download tasks = %#v", tasks)
	}
	_ = waitDownload(t, manager, tasks[0].ID)
	forbidden := downloadResponse(handler, http.MethodPost, `{"url":"https://example.test/file","directoryPath":""}`, map[string]string{"Content-Type": "application/json", "Origin": "https://attacker.example"})
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("origin response = %d", forbidden.Code)
	}
}

func waitDownload(t *testing.T, manager *DownloadManager, id string) DownloadTask {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, task := range manager.List() {
			if task.ID == id && task.Status != "queued" && task.Status != "running" {
				return task
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("download %s did not finish", id)
	return DownloadTask{}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func downloadResponse(handler http.Handler, method, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://example.com/api/downloads", strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
