package fs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

type fsLifecycleTestRecord struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Scope     string         `json:"scope"`
	Message   string         `json:"message"`
	Context   map[string]any `json:"context"`
}

func TestFSLifecycleStartupProjectsOrderedSafeRecords(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	runtime := logging.NewRuntime(logging.Options{
		Writer: &output,
		Format: logging.JSONFormat,
		Now:    func() time.Time { return base },
	})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), func() time.Time { return base })
	lifecycle.begin(Startup{
		URLs: []StartupURL{
			{Label: "Local", URL: "http://localhost:43124"},
			{Label: "Network", URL: "http://192.0.2.10:43124"},
			{Label: "Network", URL: "http://192.0.2.11:43124"},
		},
		Directory:         "/workspace/token=secret\nroot",
		BindingAddress:    "0.0.0.0\x1b[31m",
		Port:              43124,
		ManagementEnabled: true,
		ChunkedUploads:    false,
		SafeHTML:          true,
		Authentication:    false,
	})

	records := decodeFSLifecycleRecords(t, output.String())
	if got, want := fsLifecycleMessages(records), []string{
		"File Browser started",
		"Browse root configured",
		"File Browser capabilities configured",
		"File Browser authentication configured",
		"File Browser is accessible without authentication",
	}; !equalStrings(got, want) {
		t.Fatalf("startup messages = %#v, want %#v", got, want)
	}
	started := records[0]
	if started.Timestamp != "2026-09-03T09:00:00.000Z" || started.Level != "info" || started.Scope != "fs" || started.Context["localURL"] != "http://localhost:43124" || started.Context["bindingAddress"] != "0.0.0.0" || started.Context["port"] != float64(43124) {
		t.Fatalf("started record = %#v", started)
	}
	networkURLs, ok := started.Context["networkURLs"].([]any)
	if !ok || len(networkURLs) != 2 || networkURLs[0] != "http://192.0.2.10:43124" || networkURLs[1] != "http://192.0.2.11:43124" {
		t.Fatalf("network URL projection = %#v", started.Context["networkURLs"])
	}
	root := records[1]
	if root.Context["directory"] != "/workspace/token=[REDACTED]] root" {
		t.Fatalf("root record = %#v", root)
	}
	capabilities := records[2].Context
	if capabilities["managementEnabled"] != true || capabilities["chunkedUploadsEnabled"] != false || capabilities["htmlExecutionEnabled"] != false {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if _, present := capabilities["uploadChunkSizeBytes"]; present {
		t.Fatalf("disabled chunk size leaked into capabilities: %#v", capabilities)
	}
	authentication := records[3].Context
	if authentication["authenticationEnabled"] != false || len(authentication) != 1 {
		t.Fatalf("unauthenticated projection = %#v", authentication)
	}
	warning := records[4]
	if warning.Level != "warn" || warning.Context["bindingAddress"] != "0.0.0.0" || warning.Context["managementEnabled"] != true {
		t.Fatalf("public warning = %#v", warning)
	}
	encoded := output.String()
	if strings.Contains(encoded, "\x1b") || strings.Contains(encoded, "secret\\nroot") || strings.Contains(encoded, "password") {
		t.Fatalf("startup log retained unsafe content: %q", encoded)
	}
}

func TestFSLifecycleStartupAuthenticationAndChunkFields(t *testing.T) {
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), time.Now)
	lifecycle.begin(Startup{
		URLs:                []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}},
		Directory:           "/workspace",
		BindingAddress:      "127.0.0.1",
		Port:                43124,
		ChunkedUploads:      true,
		UploadChunkSize:     4 * 1024 * 1024,
		Authentication:      true,
		AccountCount:        2,
		SessionDirectory:    "/state/ycy/fs/sessions",
		SessionIdleDuration: 72 * time.Hour,
	})
	records := decodeFSLifecycleRecords(t, output.String())
	if len(records) != 4 {
		t.Fatalf("record count = %d, want 4", len(records))
	}
	capabilities := records[2].Context
	if capabilities["chunkedUploadsEnabled"] != true || capabilities["uploadChunkSizeBytes"] != float64(4*1024*1024) {
		t.Fatalf("chunk capability projection = %#v", capabilities)
	}
	authentication := records[3].Context
	if authentication["authenticationEnabled"] != true || authentication["accountCount"] != float64(2) || authentication["sessionDirectory"] != "/state/ycy/fs/sessions" || authentication["sessionIdleDurationMs"] != float64(72*60*60*1000) {
		t.Fatalf("authentication projection = %#v", authentication)
	}
}

func TestFSLifecycleStartupCheckpointOrdering(t *testing.T) {
	events := &fsLifecycleEventRecorder{}
	runtime := logging.NewRuntime(logging.Options{Writer: events, Format: logging.JSONFormat})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), time.Now)
	lifecycle.begin(Startup{
		URLs:           []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}},
		Directory:      "/workspace",
		BindingAddress: "127.0.0.1",
		Port:           43124,
	})
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       events,
	})
	run := experience.Open(context.Background())
	if err := run.ResultCheckpoint("fs-startup", terminalFSStartupDocument(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}})); err != nil {
		t.Fatalf("startup checkpoint = %v", err)
	}
	lifecycle.commitStartup()
	if err := run.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if got := events.Values(); len(got) < 5 || !strings.HasPrefix(got[0], "log:") || !strings.HasPrefix(got[3], "log:") || !strings.HasPrefix(got[4], "result:") {
		t.Fatalf("event order = %#v", got)
	}
	if !lifecycle.startupCommitted() {
		t.Fatal("startup gate did not commit")
	}
}

func TestFSLifecycleTextUsesFSStatusSymbols(t *testing.T) {
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{
		Writer: &output,
		Now:    func() time.Time { return time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC) },
	})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), time.Now)
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	if !strings.Contains(output.String(), "●  File Browser started") || !strings.Contains(output.String(), "●  Browse root configured") || !strings.Contains(output.String(), "●  File Browser capabilities configured") || !strings.Contains(output.String(), "●  File Browser authentication configured") {
		t.Fatalf("text startup symbols = %q", output.String())
	}
	runtime.Logger("fs").Info("Download task accepted", map[string]any{"taskType": "download", "taskID": "download-1"})
	runtime.Logger("fs").Info("Download task started", map[string]any{"taskType": "download", "taskID": "download-1"})
	runtime.Logger("fs").Info("Download task completed", map[string]any{"taskType": "download", "taskID": "download-1"})
	runtime.Logger("fs").Info("Download task cancelled", map[string]any{"taskType": "download", "taskID": "download-1"})
	runtime.Logger("fs").Info("Extraction task accepted", map[string]any{"taskType": "extraction", "taskID": "extraction-1"})
	runtime.Logger("fs").Info("Extraction task started", map[string]any{"taskType": "extraction", "taskID": "extraction-1"})
	runtime.Logger("fs").Info("Extraction task completed", map[string]any{"taskType": "extraction", "taskID": "extraction-1"})
	runtime.Logger("fs").Info("Extraction task cancelled", map[string]any{"taskType": "extraction", "taskID": "extraction-1"})
	runtime.Logger("fs").Info("File Browser stopping", nil)
	runtime.Logger("fs").Info("File Browser stopped", nil)
	for _, expected := range []string{"●  Download task accepted", "●  Download task started", "✓  Download task completed", "⊘  Download task cancelled", "●  Extraction task accepted", "●  Extraction task started", "✓  Extraction task completed", "⊘  Extraction task cancelled", "●  File Browser stopping", "✓  File Browser stopped"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("text output omitted %q: %q", expected, output.String())
		}
	}
}

func TestFSLifecycleHonorsConfiguredLevels(t *testing.T) {
	startup := Startup{
		URLs:           []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}},
		BindingAddress: "0.0.0.0",
		Port:           43124,
	}
	all := []string{
		"File Browser started",
		"Browse root configured",
		"File Browser capabilities configured",
		"File Browser authentication configured",
		"File Browser is accessible without authentication",
		"File Browser stopping",
		"File Browser failed",
	}
	for _, testCase := range []struct {
		name  string
		level logging.Level
		want  []string
	}{
		{name: "debug", level: logging.Debug, want: all},
		{name: "info", level: logging.Info, want: all},
		{name: "warn", level: logging.Warn, want: []string{"File Browser is accessible without authentication", "File Browser failed"}},
		{name: "error", level: logging.Error, want: []string{"File Browser failed"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
			runtime.SetLevel(testCase.level)
			lifecycle := newFSLifecycle(runtime.Logger("fs"), time.Now)
			lifecycle.begin(startup)
			lifecycle.commitStartup()
			lifecycle.shutdownStarted("context-cancelled", fsShutdownSnapshot{})
			lifecycle.shutdownFinished(fsShutdownSummary{}, "release", errors.New("release failed"))
			if got := fsLifecycleMessages(decodeFSLifecycleRecords(t, output.String())); !equalStrings(got, testCase.want) {
				t.Fatalf("messages = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestFSLifecycleDownloadProjectsSafeOrderedCompletion(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	now := base
	runtime := logging.NewRuntime(logging.Options{
		Writer: &output,
		Format: logging.JSONFormat,
		Now:    func() time.Time { return now },
	})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), func() time.Time { return now })
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(workspace.rootDirectory, "exports", "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := NewDownloadManager(workspace)
	manager.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Length": {"3"}},
			Body:       io.NopCloser(strings.NewReader("abc")),
		}, nil
	})}
	manager.setLifecycle(lifecycle, func() time.Time { return now })
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	lifecycle.commitStartup()
	task, err := manager.Enqueue(DownloadRequest{
		URL:           "HTTPS://Example.test:8443/private/report?token=do-not-log#fragment",
		DirectoryPath: "exports/reports",
		Filename:      "result.bin",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	finished := waitDownload(t, manager, task.ID)
	if finished.Status != "done" {
		t.Fatalf("finished task = %#v", finished)
	}
	records := decodeFSLifecycleRecords(t, output.String())
	if got, want := fsLifecycleMessages(records), []string{"File Browser started", "Browse root configured", "File Browser capabilities configured", "File Browser authentication configured", "Download task accepted", "Download task started", "Download task completed"}; !equalStrings(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
	for _, record := range records[4:] {
		if record.Context["taskType"] != "download" || record.Context["taskID"] != task.ID || record.Context["sourceScheme"] != "https" || record.Context["sourceHost"] != "example.test:8443" {
			t.Fatalf("download context = %#v", record.Context)
		}
		if strings.Contains(output.String(), "private/report") || strings.Contains(output.String(), "do-not-log") || strings.Contains(output.String(), "fragment") {
			t.Fatalf("download source leaked: %q", output.String())
		}
	}
	if records[4].Context["destinationPath"] != "exports/reports" || records[5].Context["destinationPath"] != "exports/reports" {
		t.Fatalf("queued/running destination = %#v / %#v", records[4].Context, records[5].Context)
	}
	completed := records[6].Context
	if completed["destinationPath"] != "exports/reports/result.bin" || completed["filename"] != "result.bin" || completed["bytesDownloaded"] != float64(3) || completed["totalBytes"] != float64(3) || completed["durationMs"].(float64) < 0 {
		t.Fatalf("completion context = %#v", completed)
	}
}

func TestFSLifecycleDownloadGateCancellationFailureAndRetry(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat, Now: func() time.Time { return base }})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), func() time.Time { return base })
	manager := NewDownloadManager(openReadOnlyWorkspace(t, t.TempDir()))
	manager.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/fail" {
			return nil, errors.New("request https://example.test/fail?token=secret failed at /absolute/path")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	manager.setLifecycle(lifecycle, time.Now)
	// Events observed before begin are held until the startup checkpoint gate.
	failed, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/fail", DirectoryPath: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("pre-startup events were emitted: %q", output.String())
	}
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	if output.Len() == 0 {
		t.Fatal("startup records missing")
	}
	lifecycle.commitStartup()
	_ = waitDownload(t, manager, failed.ID)
	// A queued cancellation has no started record and still has one terminal.
	manager.mu.Lock()
	manager.running = 2
	manager.mu.Unlock()
	queued, err := manager.Enqueue(DownloadRequest{URL: "https://example.test/queued", DirectoryPath: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled, cancelErr := manager.Cancel(queued.ID); cancelErr != nil || cancelled.Status != "cancelled" {
		t.Fatalf("queued Cancel() = %#v, %v", cancelled, cancelErr)
	}
	manager.mu.Lock()
	manager.running = 0
	manager.mu.Unlock()
	// Retry the failed task and verify the new task points at the old ID.
	retried, err := manager.Retry(failed.ID)
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	_ = waitDownload(t, manager, retried.ID)
	records := decodeFSLifecycleRecords(t, output.String())
	failedRecords := lifecycleTestRecordsForTask(records, failed.ID)
	if countLifecycleMessage(failedRecords, "Download task accepted") != 1 || countLifecycleMessage(failedRecords, "Download task started") != 1 || countLifecycleMessage(failedRecords, "Download task failed") != 1 {
		t.Fatalf("failed task records = %#v", failedRecords)
	}
	for _, record := range failedRecords {
		if value, ok := record.Context["error"].(string); ok && (strings.Contains(value, "secret") || strings.Contains(value, "/absolute/path")) {
			t.Fatalf("failure leaked unsafe detail = %#v", record.Context)
		}
	}
	queuedRecords := lifecycleTestRecordsForTask(records, queued.ID)
	if countLifecycleMessage(queuedRecords, "Download task accepted") != 1 || countLifecycleMessage(queuedRecords, "Download task started") != 0 || countLifecycleMessage(queuedRecords, "Download task cancelled") != 1 {
		t.Fatalf("queued cancellation records = %#v", queuedRecords)
	}
	retryRecords := lifecycleTestRecordsForTask(records, retried.ID)
	if len(retryRecords) != 3 || retryRecords[0].Context["retryOf"] != failed.ID {
		t.Fatalf("retry records = %#v", retryRecords)
	}
}

func TestFSLifecycleExtractionProjectsSafeOrderedCompletion(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	now := base
	runtime := logging.NewRuntime(logging.Options{
		Writer: &output,
		Format: logging.JSONFormat,
		Now:    func() time.Time { return now },
	})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), func() time.Time { return now })
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	started := make(chan string, 1)
	manager := newExtractionManager(func(ctx context.Context, path WorkspacePath, options ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		started <- path.String()
		inspection := ArchiveInspection{UncompressedBytes: 123, EntryCount: 4}
		options.OnInspect(inspection)
		options.Progress(42)
		return ArchiveExtractionResult{Inspection: inspection, Destination: mustWorkspacePath(t, "out/report")}, nil
	})
	manager.setLifecycle(lifecycle, func() time.Time { return now })
	defer manager.Close()
	_ = workspace
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	lifecycle.commitStartup()
	task, err := manager.Enqueue([]string{"archives/token=secret.zip"})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if got := <-started; got != "archives/token=secret.zip" {
		t.Fatalf("started archive = %q", got)
	}
	finished := waitExtraction(t, manager, task[0].ID)
	if finished.Status != "done" || finished.DestinationPath != "out/report" {
		t.Fatalf("finished task = %#v", finished)
	}
	records := decodeFSLifecycleRecords(t, output.String())
	if got, want := fsLifecycleMessages(records), []string{"File Browser started", "Browse root configured", "File Browser capabilities configured", "File Browser authentication configured", "Extraction task accepted", "Extraction task started", "Extraction task completed"}; !equalStrings(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
	for _, record := range records[4:] {
		archivePath, _ := record.Context["archivePath"].(string)
		if record.Context["taskType"] != "extraction" || record.Context["taskID"] != task[0].ID || !strings.HasPrefix(archivePath, "archives/token=") || !strings.Contains(archivePath, "[REDACTED]") {
			t.Fatalf("extraction context = %#v", record.Context)
		}
		if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "entry-name") {
			t.Fatalf("extraction log retained unsafe content: %q", output.String())
		}
	}
	if _, present := records[4].Context["destinationPath"]; present {
		t.Fatalf("accepted record published destination: %#v", records[4].Context)
	}
	if _, present := records[5].Context["progress"]; present {
		t.Fatalf("started record published progress: %#v", records[5].Context)
	}
	completed := records[6].Context
	if completed["destinationPath"] != "out/report" || completed["entryCount"] != float64(4) || completed["uncompressedBytes"] != float64(123) || completed["durationMs"].(float64) < 0 {
		t.Fatalf("completion context = %#v", completed)
	}
}

func TestFSLifecycleExtractionGateCancellationFailureAndRetry(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 13, 0, 0, 0, time.UTC)
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat, Now: func() time.Time { return base }})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), func() time.Time { return base })
	started := make(chan string, 2)
	var cancelCalls int
	manager := newExtractionManager(func(ctx context.Context, path WorkspacePath, options ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		started <- path.String()
		switch path.String() {
		case "failed.zip":
			return ArchiveExtractionResult{}, errors.New("7-Zip leaked /absolute/path token=secret")
		case "cancelled.zip":
			cancelCalls++
			if cancelCalls == 1 {
				<-ctx.Done()
				return ArchiveExtractionResult{}, ctx.Err()
			}
			return ArchiveExtractionResult{Inspection: ArchiveInspection{UncompressedBytes: 8, EntryCount: 2}, Destination: mustWorkspacePath(t, "cancelled.out")}, nil
		default:
			return ArchiveExtractionResult{}, nil
		}
	})
	manager.setLifecycle(lifecycle, time.Now)
	defer manager.Close()
	failed, err := manager.Enqueue([]string{"failed.zip"})
	if err != nil {
		t.Fatal(err)
	}
	_ = <-started
	if output.Len() != 0 {
		t.Fatalf("pre-startup extraction events were emitted: %q", output.String())
	}
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	lifecycle.commitStartup()
	_ = waitExtraction(t, manager, failed[0].ID)
	cancelled, err := manager.Enqueue([]string{"cancelled.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "cancelled.zip" {
		t.Fatalf("cancelled extraction started = %q", got)
	}
	if task, cancelErr := manager.Cancel(cancelled[0].ID); cancelErr != nil || task.Status != "cancelled" {
		t.Fatalf("Cancel() = %#v, %v", task, cancelErr)
	}
	_ = waitExtraction(t, manager, cancelled[0].ID)
	retried, err := manager.Retry(cancelled[0].ID)
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if got := <-started; got != "cancelled.zip" {
		t.Fatalf("retried extraction started = %q", got)
	}
	if task := waitExtraction(t, manager, retried.ID); task.Status != "done" {
		t.Fatalf("retried extraction = %#v", task)
	}
	records := decodeFSLifecycleRecords(t, output.String())
	failedRecords := lifecycleTestRecordsForTask(records, failed[0].ID)
	if countLifecycleMessage(failedRecords, "Extraction task accepted") != 1 || countLifecycleMessage(failedRecords, "Extraction task started") != 1 || countLifecycleMessage(failedRecords, "Extraction task failed") != 1 {
		t.Fatalf("failed extraction records = %#v", failedRecords)
	}
	if failure := failedRecords[2].Context; failure["code"] != "UNAVAILABLE" || failure["error"] != "Extraction could not be completed" {
		t.Fatalf("failure projection = %#v", failure)
	}
	cancelledRecords := lifecycleTestRecordsForTask(records, cancelled[0].ID)
	if countLifecycleMessage(cancelledRecords, "Extraction task accepted") != 1 || countLifecycleMessage(cancelledRecords, "Extraction task started") != 1 || countLifecycleMessage(cancelledRecords, "Extraction task cancelled") != 1 {
		t.Fatalf("cancelled extraction records = %#v", cancelledRecords)
	}
	if cancelledRecords[2].Context["cancelSource"] != "client" {
		t.Fatalf("cancel source = %#v", cancelledRecords[2].Context)
	}
	retryRecords := lifecycleTestRecordsForTask(records, retried.ID)
	if len(retryRecords) != 3 || retryRecords[0].Context["retryOf"] != cancelled[0].ID || retryRecords[2].Message != "Extraction task completed" {
		t.Fatalf("retry extraction records = %#v", retryRecords)
	}
	if strings.Contains(output.String(), "absolute/path") || strings.Contains(output.String(), "secret") {
		t.Fatalf("failure details leaked: %q", output.String())
	}
}

func TestFSLifecycleChunkedUploadProjectsSafeStartAndCompletion(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC)
	now := base
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat, Now: func() time.Time { return now }})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), func() time.Time { return now })
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	if err := os.Mkdir(filepath.Join(workspace.rootDirectory, "uploads"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := newChunkedUploadManager(workspace, 32*1024*1024, func() time.Time { return now }, lifecycle)
	defer manager.Close()
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	lifecycle.commitStartup()
	size := chunkedUploadThreshold + 1
	upload, err := manager.Create("owner-token=do-not-log", mustWorkspacePath(t, "uploads"), "token=secret.bin", size)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := manager.Append("owner-token=do-not-log", upload.ID, 0, size-1, size, bytes.NewReader(make([]byte, size))); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	completed, err := manager.Complete("owner-token=do-not-log", upload.ID)
	if err != nil || completed.Result == nil || completed.Result.Path != "uploads/token=secret.bin" {
		t.Fatalf("Complete() = %#v, %v", completed, err)
	}
	if _, err := manager.Complete("owner-token=do-not-log", upload.ID); err != nil {
		t.Fatalf("replayed Complete() error = %v", err)
	}
	records := decodeFSLifecycleRecords(t, output.String())
	taskRecords := lifecycleTestRecordsForTask(records, upload.ID)
	if got, want := fsLifecycleMessages(taskRecords), []string{"Chunked upload started", "Chunked upload completed"}; !equalStrings(got, want) {
		t.Fatalf("chunked upload messages = %#v, want %#v", got, want)
	}
	started := taskRecords[0].Context
	if started["taskType"] != "chunkedUpload" || started["destinationPath"] != "uploads" || started["totalBytes"] != float64(size) || started["chunkSizeBytes"] != float64(32*1024*1024) {
		t.Fatalf("started projection = %#v", started)
	}
	if filename, _ := started["filename"].(string); !strings.Contains(filename, "[REDACTED]") {
		t.Fatalf("started filename = %#v", started)
	}
	finished := taskRecords[1].Context
	if destination, _ := finished["destinationPath"].(string); !strings.HasPrefix(destination, "uploads/token=") || !strings.Contains(destination, "[REDACTED]") || finished["totalBytes"] != float64(size) || finished["durationMs"].(float64) < 0 {
		t.Fatalf("completion projection = %#v", finished)
	}
	if _, present := finished["filename"]; present {
		t.Fatalf("completion retained filename: %#v", finished)
	}
	for _, unsafe := range []string{"do-not-log", "secret", ".upload-", "uploadedBytes"} {
		if strings.Contains(output.String(), unsafe) {
			t.Fatalf("chunked upload log retained %q: %q", unsafe, output.String())
		}
	}
}

func TestFSLifecycleChunkedUploadGateCancellationAndExpiry(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 15, 0, 0, 0, time.UTC)
	now := base
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat, Now: func() time.Time { return now }})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), func() time.Time { return now })
	manager := newChunkedUploadManager(openReadOnlyWorkspace(t, t.TempDir()), 4*1024*1024, func() time.Time { return now }, lifecycle)
	defer manager.Close()
	size := chunkedUploadThreshold + 1
	preStartup, err := manager.Create("anonymous", mustWorkspacePath(t, ""), "cancelled.bin", size)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("pre-startup chunked event was emitted: %q", output.String())
	}
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	lifecycle.commitStartup()
	if _, err := manager.Append("anonymous", preStartup.ID, 0, 0, size, strings.NewReader("x")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := manager.Cancel("anonymous", preStartup.ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	expiring, err := manager.Create("anonymous", mustWorkspacePath(t, ""), "expired.bin", size)
	if err != nil {
		t.Fatalf("Create(expiring) error = %v", err)
	}
	now = now.Add(31 * time.Minute)
	if _, err := manager.Get("anonymous", expiring.ID); !serviceErrorIs(err, "CHUNKED_UPLOAD_NOT_FOUND") {
		t.Fatalf("Get(expired) error = %v", err)
	}
	records := decodeFSLifecycleRecords(t, output.String())
	cancelledRecords := lifecycleTestRecordsForTask(records, preStartup.ID)
	if got, want := fsLifecycleMessages(cancelledRecords), []string{"Chunked upload started", "Chunked upload cancelled"}; !equalStrings(got, want) {
		t.Fatalf("cancelled messages = %#v, want %#v", got, want)
	}
	if cancelled := cancelledRecords[1].Context; cancelled["cancelSource"] != "client" || cancelled["uploadedBytes"] != float64(1) || cancelled["totalBytes"] != float64(size) {
		t.Fatalf("cancelled projection = %#v", cancelled)
	}
	expiredRecords := lifecycleTestRecordsForTask(records, expiring.ID)
	if got, want := fsLifecycleMessages(expiredRecords), []string{"Chunked upload started", "Chunked upload expired"}; !equalStrings(got, want) {
		t.Fatalf("expired messages = %#v, want %#v", got, want)
	}
	if expired := expiredRecords[1].Context; expired["uploadedBytes"] != float64(0) || expired["totalBytes"] != float64(size) {
		t.Fatalf("expired projection = %#v", expired)
	}
}

func TestFSLifecycleShutdownProjectsCountsAndFinalOrdering(t *testing.T) {
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), time.Now)
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	lifecycle.commitStartup()
	lifecycle.shutdownStarted("context-cancelled", fsShutdownSnapshot{
		QueuedDownloads: 2, ActiveDownloads: 1, QueuedExtractions: 3, ActiveExtractions: 1, IncompleteChunkedUploads: 4,
	})
	lifecycle.shutdownFinished(fsShutdownSummary{CancelledDownloads: 3, CancelledExtractions: 4, RemovedChunkedUploads: 4}, "", nil)
	lifecycle.shutdownFinished(fsShutdownSummary{}, "serve", errors.New("late error"))
	records := decodeFSLifecycleRecords(t, output.String())
	if got, want := fsLifecycleMessages(records), []string{"File Browser started", "Browse root configured", "File Browser capabilities configured", "File Browser authentication configured", "File Browser stopping", "File Browser stopped"}; !equalStrings(got, want) {
		t.Fatalf("shutdown messages = %#v, want %#v", got, want)
	}
	stopping := records[4].Context
	if stopping["reason"] != "context-cancelled" || stopping["queuedDownloads"] != float64(2) || stopping["activeDownloads"] != float64(1) || stopping["queuedExtractions"] != float64(3) || stopping["activeExtractions"] != float64(1) || stopping["incompleteChunkedUploads"] != float64(4) {
		t.Fatalf("stopping context = %#v", stopping)
	}
	stopped := records[5].Context
	if stopped["cancelledDownloads"] != float64(3) || stopped["cancelledExtractions"] != float64(4) || stopped["removedChunkedUploads"] != float64(4) {
		t.Fatalf("stopped context = %#v", stopped)
	}
}

func TestFSLifecycleDropsPendingAndLateTasksAfterStopping(t *testing.T) {
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	lifecycle := newFSLifecycle(runtime.Logger("fs"), time.Now)

	// A premature commit must not open the gate before startup has begun.
	lifecycle.commitStartup()
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	lifecycle.downloadAccepted(downloadLifecycleTask{ID: "queued-before-start"})
	if lifecycle.startupCommitted() {
		t.Fatal("startup gate committed before begin")
	}

	lifecycle.shutdownStarted("context-cancelled", fsShutdownSnapshot{})
	lifecycle.downloadStarted(downloadLifecycleTask{ID: "late-task"})
	lifecycle.downloadCompleted(downloadLifecycleTask{ID: "late-task"})
	lifecycle.shutdownFinished(fsShutdownSummary{}, "", nil)

	records := decodeFSLifecycleRecords(t, output.String())
	if got, want := fsLifecycleMessages(records), []string{
		"File Browser started",
		"Browse root configured",
		"File Browser capabilities configured",
		"File Browser authentication configured",
		"File Browser stopping",
		"File Browser stopped",
	}; !equalStrings(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
}

func TestRuntimeShutdownFoldsRealManagedTaskCounts(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 17, 0, 0, 0, time.UTC)
	logRuntime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat, Now: func() time.Time { return base }})
	lifecycle := newFSLifecycle(logRuntime.Logger("fs"), func() time.Time { return base })
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})
	lifecycle.commitStartup()

	workspace := openReadOnlyWorkspace(t, t.TempDir())
	downloads := newDownloadManager(workspace, func() time.Time { return base }, lifecycle)
	downloads.tasks["queued-download"] = &DownloadTask{ID: "queued-download", Status: "queued"}
	downloads.tasks["active-download"] = &DownloadTask{ID: "active-download", Status: "running"}
	downloads.queue = []string{"queued-download"}

	extractions := newExtractionManagerWithLifecycle(func(context.Context, WorkspacePath, ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		return ArchiveExtractionResult{}, nil
	}, func() time.Time { return base }, lifecycle)
	extractions.tasks["queued-extraction"] = &ExtractionTask{ID: "queued-extraction", Status: "queued"}
	extractions.tasks["active-extraction"] = &ExtractionTask{ID: "active-extraction", Status: "running"}
	extractions.queue = []string{"queued-extraction"}
	extractions.active = true

	stagingName := ".upload-shutdown.tmp"
	if err := os.WriteFile(filepath.Join(workspace.rootDirectory, stagingName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	chunkedUploads := newChunkedUploadManager(workspace, 4*1024*1024, func() time.Time { return base }, lifecycle)
	chunkedUploads.uploads["pending-upload"] = &chunkedUpload{
		id:        "pending-upload",
		directory: mustWorkspacePath(t, ""),
		temporary: mustWorkspacePath(t, stagingName),
		filename:  "pending.bin",
		size:      chunkedUploadThreshold + 1,
		started:   base,
		updated:   base,
	}

	runtime := &Runtime{
		workspace:      workspace,
		downloads:      downloads,
		extractions:    extractions,
		chunkedUploads: chunkedUploads,
		shutdownReason: "context-cancelled",
		lifecycle:      lifecycle,
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	records := decodeFSLifecycleRecords(t, output.String())
	if got, want := fsLifecycleMessages(records), []string{
		"File Browser started",
		"Browse root configured",
		"File Browser capabilities configured",
		"File Browser authentication configured",
		"File Browser stopping",
		"File Browser stopped",
	}; !equalStrings(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
	stopping := records[4].Context
	if stopping["queuedDownloads"] != float64(1) || stopping["activeDownloads"] != float64(1) || stopping["queuedExtractions"] != float64(1) || stopping["activeExtractions"] != float64(1) || stopping["incompleteChunkedUploads"] != float64(1) {
		t.Fatalf("stopping context = %#v", stopping)
	}
	stopped := records[5].Context
	if stopped["cancelledDownloads"] != float64(2) || stopped["cancelledExtractions"] != float64(2) || stopped["removedChunkedUploads"] != float64(1) {
		t.Fatalf("stopped context = %#v", stopped)
	}
}

func TestRuntimeShutdownJoinsCheckpointAndReleaseFailuresInOneLifecycleRecord(t *testing.T) {
	var output bytes.Buffer
	logRuntime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	lifecycle := newFSLifecycle(logRuntime.Logger("fs"), time.Now)
	lifecycle.begin(Startup{URLs: []StartupURL{{Label: "Local", URL: "http://127.0.0.1:43124"}}, BindingAddress: "127.0.0.1", Port: 43124})

	workspace := openReadOnlyWorkspace(t, t.TempDir())
	releaseErr := errors.New("workspace release failed")
	workspace.root = failingFSWorkspaceRoot{workspaceRoot: workspace.root, err: releaseErr}
	runtime := &Runtime{
		workspace:      workspace,
		shutdownReason: "startup-output-failed",
		lifecycle:      lifecycle,
	}
	checkpointErr := errors.New("startup checkpoint write failed")
	runtime.recordShutdownCause(checkpointErr)
	if err := runtime.Close(); !errors.Is(err, releaseErr) {
		t.Fatalf("Close() error = %v, want release error", err)
	}

	records := decodeFSLifecycleRecords(t, output.String())
	if got := records[len(records)-1]; got.Message != "File Browser failed" || got.Level != "error" || got.Context["stage"] != "release" || !strings.Contains(got.Context["error"].(string), checkpointErr.Error()) || !strings.Contains(got.Context["error"].(string), releaseErr.Error()) {
		t.Fatalf("failure record = %#v", got)
	}
}

func TestFSOperationContextCancellationEmitsShutdownPair(t *testing.T) {
	var output bytes.Buffer
	base := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC)
	logRuntime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat, Now: func() time.Time { return base }})
	ctx, cancel := context.WithCancel(context.Background())
	module, err := New(Dependencies{
		NetworkInterfaces: func() ([]NetworkInterface, error) { return nil, nil },
		Logger:            logRuntime.Logger("fs"),
		Now:               func() time.Time { return base },
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := module.Start(ctx, Input{Directory: t.TempDir(), Address: "127.0.0.1", Port: 0})
	if err != nil || operation == nil {
		t.Fatalf("Start() = %#v, %v", operation, err)
	}
	operation.lifecycle.commitStartup()
	cancel()
	if err := operation.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	records := decodeFSLifecycleRecords(t, output.String())
	messages := fsLifecycleMessages(records)
	if len(messages) < 6 || messages[len(messages)-2] != "File Browser stopping" || messages[len(messages)-1] != "File Browser stopped" {
		t.Fatalf("context shutdown messages = %#v", messages)
	}
	if records[len(records)-2].Context["reason"] != "context-cancelled" {
		t.Fatalf("stopping reason = %#v", records[len(records)-2].Context)
	}
}

func TestFSOperationUnexpectedServeFailureIsReportedOnce(t *testing.T) {
	var output bytes.Buffer
	logRuntime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	module, err := New(Dependencies{
		NetworkInterfaces: func() ([]NetworkInterface, error) { return nil, nil },
		Logger:            logRuntime.Logger("fs"),
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := module.Start(context.Background(), Input{Directory: t.TempDir(), Address: "127.0.0.1", Port: 0})
	if err != nil || operation == nil {
		t.Fatalf("Start() = %#v, %v", operation, err)
	}
	operation.lifecycle.commitStartup()
	if err := operation.server.listener.Close(); err != nil {
		t.Fatal(err)
	}
	waitErr := operation.Wait(context.Background())
	if waitErr == nil {
		t.Fatal("Wait() returned nil after listener failure")
	}
	records := decodeFSLifecycleRecords(t, output.String())
	messages := fsLifecycleMessages(records)
	if messages[len(messages)-1] != "File Browser failed" || countLifecycleMessage(records, "File Browser failed") != 1 {
		t.Fatalf("serve failure messages = %#v", messages)
	}
	last := records[len(records)-1]
	if last.Level != "error" || last.Context["stage"] != "serve" || last.Context["error"] == "" {
		t.Fatalf("serve failure record = %#v", last)
	}
}

func lifecycleTestRecordsForTask(records []fsLifecycleTestRecord, taskID string) []fsLifecycleTestRecord {
	result := make([]fsLifecycleTestRecord, 0)
	for _, record := range records {
		if record.Context["taskID"] == taskID {
			result = append(result, record)
		}
	}
	return result
}

func countLifecycleMessage(records []fsLifecycleTestRecord, message string) int {
	count := 0
	for _, record := range records {
		if record.Message == message {
			count++
		}
	}
	return count
}

func decodeFSLifecycleRecords(t *testing.T, value string) []fsLifecycleTestRecord {
	t.Helper()
	var records []fsLifecycleTestRecord
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		if line == "" {
			continue
		}
		var record fsLifecycleTestRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode lifecycle record %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func fsLifecycleMessages(records []fsLifecycleTestRecord) []string {
	messages := make([]string, 0, len(records))
	for _, record := range records {
		messages = append(messages, record.Message)
	}
	return messages
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type fsLifecycleEventRecorder struct {
	mu     sync.Mutex
	values []string
}

type failingFSWorkspaceRoot struct {
	workspaceRoot
	err error
}

func (root failingFSWorkspaceRoot) Close() error {
	if root.workspaceRoot != nil {
		_ = root.workspaceRoot.Close()
	}
	return root.err
}

func (recorder *fsLifecycleEventRecorder) Write(value []byte) (int, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(value) > 0 && value[0] == '{' {
		recorder.values = append(recorder.values, "log:"+string(value))
	} else {
		recorder.values = append(recorder.values, "result:"+string(value))
	}
	return len(value), nil
}

func (recorder *fsLifecycleEventRecorder) Values() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.values...)
}
