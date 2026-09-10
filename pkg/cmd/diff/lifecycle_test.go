package diff

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

type lifecycleTestRecord struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Scope     string         `json:"scope"`
	Message   string         `json:"message"`
	Context   map[string]any `json:"context"`
}

func TestDiffLifecycleQueuesImmediateInitialRefreshUntilStartupCommit(t *testing.T) {
	base := time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC)
	now := base
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{
		Writer: &output,
		Now:    func() time.Time { return now },
		Format: logging.JSONFormat,
	})
	runtime.SetLevel(logging.Debug)
	lifecycle := newDiffLifecycle(runtime.Logger("diff"), func() time.Time { return now })
	attempt := &diffRefreshAttempt{
		lifecycle: lifecycle,
		ordinal:   1,
		source:    "initial",
		startedAt: base,
		phaseSeen: make(map[WorkspacePhase]bool),
	}
	run := &RefreshRun{attempt: attempt, done: make(chan struct{})}

	// These callbacks model the initial Refresh completing before the startup
	// result writer has accepted its durable checkpoint.
	lifecycle.state(run, WorkspaceState{Phase: PhaseDiscovering, Progress: &WorkspaceProgress{DiscoveredEntries: 2}})
	lifecycle.state(run, WorkspaceState{Phase: PhaseDiscovering, Progress: &WorkspaceProgress{DiscoveredEntries: 3}})
	lifecycle.state(run, WorkspaceState{Phase: PhaseComparing, Progress: &WorkspaceProgress{
		DiscoveredEntries: 3,
		ComparedEntries:   1,
		TotalEntries:      intPointer(4),
		ComparedBytes:     12,
		TotalBytes:        int64Pointer(48),
	}})
	lifecycle.state(run, WorkspaceState{Phase: PhaseComparing, Progress: &WorkspaceProgress{
		DiscoveredEntries: 3,
		ComparedEntries:   2,
		TotalEntries:      intPointer(4),
		ComparedBytes:     24,
		TotalBytes:        int64Pointer(48),
	}})
	lifecycle.state(run, WorkspaceState{Phase: PhasePublishing, Progress: &WorkspaceProgress{
		DiscoveredEntries: 3,
		ComparedEntries:   4,
		TotalEntries:      intPointer(4),
		ComparedBytes:     48,
		TotalBytes:        int64Pointer(48),
		Issues:            1,
	}})
	snapshot := &Snapshot{summary: SnapshotSummary{
		ID: "snapshot-1",
		Counts: StatusCounts{
			Added:     1,
			Deleted:   2,
			Modified:  3,
			Unchanged: 4,
		},
		Issues: 1,
	}}
	run.mu.Lock()
	run.snapshot = snapshot
	run.mu.Unlock()
	now = base.Add(125 * time.Millisecond)
	lifecycle.state(run, WorkspaceState{Phase: PhaseReady})
	lifecycle.attemptStarted(attempt, run)
	lifecycle.begin(Startup{
		LocalURL:          "http://127.0.0.1:43123",
		NetworkURLs:       []string{},
		BaselineDirectory: "/workspace/baseline",
		TargetDirectory:   "/workspace/target",
		Port:              43123,
	}, run)

	records := decodeLifecycleTestRecords(t, output.String())
	if got, want := lifecycleTestMessages(records), []string{
		"Directory diff started",
		"Diff endpoints available",
		"Comparison workspace configured",
		"Initial comparison refresh started",
	}; !equalStrings(got, want) {
		t.Fatalf("pre-commit messages = %#v, want %#v", got, want)
	}
	lifecycle.commitStartup()
	records = decodeLifecycleTestRecords(t, output.String())
	wantMessages := []string{
		"Directory diff started",
		"Diff endpoints available",
		"Comparison workspace configured",
		"Initial comparison refresh started",
		"Comparison refresh phase",
		"Comparison refresh phase",
		"Comparison refresh phase",
		"Comparison snapshot ready",
		"Comparison snapshot contains issues",
	}
	if got := lifecycleTestMessages(records); !equalStrings(got, wantMessages) {
		t.Fatalf("messages = %#v, want %#v", got, wantMessages)
	}
	ready := records[7]
	if ready.Level != "info" || ready.Scope != "diff" || ready.Context["refresh"] != float64(1) || ready.Context["source"] != "initial" || ready.Context["durationMs"] != float64(125) || ready.Context["snapshotID"] != "snapshot-1" || ready.Context["added"] != float64(1) || ready.Context["deleted"] != float64(2) || ready.Context["modified"] != float64(3) || ready.Context["unchanged"] != float64(4) || ready.Context["issues"] != float64(1) || ready.Context["totalEntries"] != float64(4) || ready.Context["comparedBytes"] != float64(48) || ready.Context["totalBytes"] != float64(48) {
		t.Fatalf("ready context = %#v", ready.Context)
	}
	warning := records[8]
	if warning.Level != "warn" || warning.Context["refresh"] != float64(1) || warning.Context["snapshotID"] != "snapshot-1" || warning.Context["issues"] != float64(1) {
		t.Fatalf("warning = %#v", warning)
	}
}

func TestDiffLifecycleKeepsReadyWhenShutdownFollowsRefreshCompletion(t *testing.T) {
	base := time.Date(2026, 9, 3, 2, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Now: func() time.Time { return base }, Format: logging.JSONFormat})
	lifecycle := newDiffLifecycle(runtime.Logger("diff"), func() time.Time { return base })
	attempt := &diffRefreshAttempt{
		lifecycle: lifecycle,
		ordinal:   1,
		source:    "initial",
		startedAt: base,
		phaseSeen: make(map[WorkspacePhase]bool),
	}
	run := &RefreshRun{attempt: attempt, done: make(chan struct{})}
	lifecycle.begin(Startup{LocalURL: "http://127.0.0.1:43123", BaselineDirectory: "/b", TargetDirectory: "/t", Port: 43123}, run)
	lifecycle.commitStartup()
	attempt.markCancellation("shutdown", true)
	run.mu.Lock()
	run.snapshot = &Snapshot{summary: SnapshotSummary{ID: "ready-after-shutdown"}}
	run.mu.Unlock()
	lifecycle.state(run, WorkspaceState{Phase: PhaseReady})
	lifecycle.stopping("context-cancelled")
	lifecycle.stopped("")

	records := decodeLifecycleTestRecords(t, output.String())
	messages := lifecycleTestMessages(records)
	if !containsString(messages, "Comparison snapshot ready") || containsString(messages, "Comparison refresh cancelled") {
		t.Fatalf("shutdown race messages = %#v", messages)
	}
	if messages[len(messages)-2] != "Directory diff stopping" || messages[len(messages)-1] != "Directory diff stopped" {
		t.Fatalf("shutdown tail = %#v", messages)
	}
}

func TestDiffLifecycleStartupOutputFailureDropsLateRefreshEvents(t *testing.T) {
	base := time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Now: func() time.Time { return base }, Format: logging.JSONFormat})
	lifecycle := newDiffLifecycle(runtime.Logger("diff"), func() time.Time { return base })
	attempt := &diffRefreshAttempt{
		lifecycle: lifecycle,
		ordinal:   1,
		source:    "initial",
		startedAt: base,
		phaseSeen: make(map[WorkspacePhase]bool),
	}
	run := &RefreshRun{attempt: attempt, done: make(chan struct{})}
	lifecycle.begin(Startup{LocalURL: "http://127.0.0.1:43123", BaselineDirectory: "/b", TargetDirectory: "/t", Port: 43123}, run)
	lifecycle.startupOutputFailureStarted()
	run.mu.Lock()
	run.snapshot = &Snapshot{summary: SnapshotSummary{ID: "late"}}
	run.mu.Unlock()
	lifecycle.state(run, WorkspaceState{Phase: PhaseReady})
	lifecycle.startupOutputFailureFinished()
	lifecycle.startupOutputFailureFinished()

	records := decodeLifecycleTestRecords(t, output.String())
	if got, want := lifecycleTestMessages(records), []string{
		"Directory diff started",
		"Diff endpoints available",
		"Comparison workspace configured",
		"Initial comparison refresh started",
		"Directory diff stopping",
		"Directory diff stopped",
	}; !equalStrings(got, want) {
		t.Fatalf("startup failure messages = %#v, want %#v", got, want)
	}
	for _, record := range records[4:] {
		if record.Context["reason"] != "startup-output-failed" {
			t.Fatalf("startup failure record = %#v", record)
		}
	}
}

func TestDiffRefreshCoordinatorProjectsSourcesOrdinalsRejectionAndRESTCancellation(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "before")
	writeComparisonFile(t, target, "changed.txt", "after")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	lifecycle := newDiffLifecycle(runtime.Logger("diff"), time.Now)
	lifecycle.begin(Startup{LocalURL: "http://127.0.0.1:43123", BaselineDirectory: baseline, TargetDirectory: target, Port: 43123}, nil)
	lifecycle.commitStartup()
	coordinator := newRefreshCoordinator(workspace, lifecycle)

	comparing := make(chan struct{})
	release := make(chan struct{})
	var comparingOnce sync.Once
	var releaseOnce sync.Once
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if state.Phase == PhaseComparing {
			comparingOnce.Do(func() {
				close(comparing)
				<-release
			})
		}
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		unsubscribe()
	})

	if err := coordinator.StartSource("rest"); err != nil {
		t.Fatalf("StartSource(rest) error = %v", err)
	}
	coordinator.mu.Lock()
	first := coordinator.lastStarted
	coordinator.mu.Unlock()
	if first == nil {
		t.Fatal("first refresh run was not recorded")
	}
	select {
	case <-comparing:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not reach comparing")
	}
	if err := coordinator.StartSource("mcp"); !errors.Is(err, ErrRefreshActive) {
		t.Fatalf("active MCP refresh error = %v, want %v", err, ErrRefreshActive)
	}
	coordinator.CancelSource("rest")
	releaseOnce.Do(func() { close(release) })
	if _, err := first.Wait(nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("first refresh wait error = %v, want context cancellation", err)
	}
	waitForRefreshCoordinatorClear(t, coordinator)

	if err := coordinator.StartSource("mcp"); err != nil {
		t.Fatalf("follow-up MCP refresh error = %v", err)
	}
	coordinator.mu.Lock()
	second := coordinator.lastStarted
	coordinator.mu.Unlock()
	if second == nil {
		t.Fatal("follow-up refresh run was not recorded")
	}
	if _, err := second.Wait(nil); err != nil {
		t.Fatalf("follow-up refresh wait error = %v", err)
	}

	records := decodeLifecycleTestRecords(t, output.String())
	messages := lifecycleTestMessages(records)
	if countString(messages, "Comparison refresh started") != 2 || countString(messages, "Comparison refresh cancelled") != 1 || countString(messages, "Comparison snapshot ready") != 1 {
		t.Fatalf("refresh messages = %#v", messages)
	}
	starts := lifecycleTestRecordsForMessage(records, "Comparison refresh started")
	if starts[0].Context["refresh"] != float64(1) || starts[0].Context["source"] != "rest" || starts[1].Context["refresh"] != float64(2) || starts[1].Context["source"] != "mcp" {
		t.Fatalf("refresh starts = %#v", starts)
	}
	canceled := lifecycleTestRecordsForMessage(records, "Comparison refresh cancelled")
	if canceled[0].Context["refresh"] != float64(1) || canceled[0].Context["source"] != "rest" || canceled[0].Context["cancelSource"] != "rest" || canceled[0].Context["hasPreviousSnapshot"] != false {
		t.Fatalf("cancellation record = %#v", canceled[0])
	}
}

func TestDiffLifecycleCancellationAndFailureRetainPreviousSnapshot(t *testing.T) {
	base := time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC)
	now := base
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Now: func() time.Time { return now }, Format: logging.JSONFormat})
	lifecycle := newDiffLifecycle(runtime.Logger("diff"), func() time.Time { return now })
	lifecycle.begin(Startup{LocalURL: "http://127.0.0.1:43123", BaselineDirectory: "/b", TargetDirectory: "/t", Port: 43123}, nil)
	lifecycle.commitStartup()

	canceledAttempt := &diffRefreshAttempt{
		lifecycle:          lifecycle,
		ordinal:            2,
		source:             "rest",
		startedAt:          base,
		previousSnapshotID: "published-1",
		phaseSeen:          make(map[WorkspacePhase]bool),
	}
	canceledRun := &RefreshRun{attempt: canceledAttempt, done: make(chan struct{})}
	lifecycle.attemptStarted(canceledAttempt, canceledRun)
	lifecycle.state(canceledRun, WorkspaceState{Phase: PhaseComparing, Progress: &WorkspaceProgress{ComparedEntries: 3, Issues: 1}})
	canceledAttempt.markCancellation("rest", false)
	canceledRun.mu.Lock()
	canceledRun.err = context.Canceled
	canceledRun.mu.Unlock()
	now = base.Add(25 * time.Millisecond)
	lifecycle.state(canceledRun, WorkspaceState{Phase: PhaseCanceled})

	failedAttempt := &diffRefreshAttempt{
		lifecycle:          lifecycle,
		ordinal:            3,
		source:             "mcp",
		startedAt:          base,
		previousSnapshotID: "published-1",
		phaseSeen:          make(map[WorkspacePhase]bool),
	}
	failedRun := &RefreshRun{attempt: failedAttempt, done: make(chan struct{})}
	lifecycle.attemptStarted(failedAttempt, failedRun)
	failure := errors.New("token=do-not-log\ncomparison failed")
	failedRun.mu.Lock()
	failedRun.err = failure
	failedRun.mu.Unlock()
	now = base.Add(50 * time.Millisecond)
	lifecycle.state(failedRun, WorkspaceState{Phase: PhaseError, Error: failure.Error()})

	records := decodeLifecycleTestRecords(t, output.String())
	canceled := lifecycleTestRecordsForMessage(records, "Comparison refresh cancelled")
	if len(canceled) != 1 || canceled[0].Context["refresh"] != float64(2) || canceled[0].Context["source"] != "rest" || canceled[0].Context["durationMs"] != float64(25) || canceled[0].Context["cancelSource"] != "rest" || canceled[0].Context["hasPreviousSnapshot"] != true || canceled[0].Context["previousSnapshotID"] != "published-1" || canceled[0].Context["comparedEntries"] != float64(3) {
		t.Fatalf("cancellation context = %#v", canceled)
	}
	failed := lifecycleTestRecordsForMessage(records, "Comparison refresh failed")
	if len(failed) != 1 || failed[0].Context["refresh"] != float64(3) || failed[0].Context["source"] != "mcp" || failed[0].Context["durationMs"] != float64(50) || failed[0].Context["hasPreviousSnapshot"] != true || failed[0].Context["previousSnapshotID"] != "published-1" || !strings.Contains(failed[0].Context["error"].(string), "[REDACTED]") || strings.Contains(failed[0].Context["error"].(string), "do-not-log") {
		t.Fatalf("failure context = %#v", failed)
	}
}

func TestDiffSessionFoldsActiveRefreshCancellationIntoShutdown(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "before")
	writeComparisonFile(t, target, "changed.txt", "after")
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	module, err := New(Dependencies{
		NetworkInterfaces: func() ([]NetworkInterface, error) { return nil, nil },
		Logger:            runtime.Logger("diff"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	operation, err := module.Start(context.Background(), Input{BaselineDirectory: baseline, TargetDirectory: target, Port: 0})
	if err != nil || operation == nil {
		t.Fatalf("Start() = %#v, error = %v", operation, err)
	}
	operation.session.lifecycle.commitStartup()
	waitForWorkspacePhase(t, operation.session.workspace, PhaseReady)

	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	var releaseOnce sync.Once
	unsubscribe := operation.session.workspace.Subscribe(func(state WorkspaceState) {
		if state.Phase == PhaseComparing {
			enteredOnce.Do(func() {
				close(entered)
				<-release
			})
		}
	})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		unsubscribe()
		_ = operation.session.server.Close()
	})
	if err := operation.session.server.refresh.StartSource("rest"); err != nil {
		t.Fatalf("start REST refresh: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not reach comparing")
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- operation.session.shutdown("context-cancelled") }()
	select {
	case err := <-shutdown:
		t.Fatalf("shutdown returned before refresh release: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-shutdown; err != nil {
		t.Fatalf("shutdown error = %v", err)
	}

	records := decodeLifecycleTestRecords(t, output.String())
	messages := lifecycleTestMessages(records)
	if countString(messages, "Comparison refresh cancelled") != 0 || messages[len(messages)-2] != "Directory diff stopping" || messages[len(messages)-1] != "Directory diff stopped" {
		t.Fatalf("shutdown lifecycle messages = %#v", messages)
	}
}

func TestDiffSessionReportsUnexpectedServeFailureOnce(t *testing.T) {
	baseline, target := comparisonRoots(t)
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	module, err := New(Dependencies{
		NetworkInterfaces: func() ([]NetworkInterface, error) { return nil, nil },
		Logger:            runtime.Logger("diff"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	operation, err := module.Start(context.Background(), Input{BaselineDirectory: baseline, TargetDirectory: target, Port: 0})
	if err != nil || operation == nil {
		t.Fatalf("Start() = %#v, error = %v", operation, err)
	}
	operation.session.lifecycle.commitStartup()
	waitForWorkspacePhase(t, operation.session.workspace, PhaseReady)
	if err := operation.session.server.listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	waitErr := operation.Wait(context.Background())
	if waitErr == nil {
		t.Fatal("Wait() returned nil after unexpected listener failure")
	}
	var reported reportedDiffError
	if !errors.As(waitErr, &reported) {
		t.Fatalf("Wait() error = %T %v, want reportedDiffError", waitErr, waitErr)
	}
	records := decodeLifecycleTestRecords(t, output.String())
	messages := lifecycleTestMessages(records)
	if messages[len(messages)-1] != "Directory diff failed" || countString(messages, "Directory diff failed") != 1 {
		t.Fatalf("serve failure lifecycle messages = %#v", messages)
	}
	last := records[len(records)-1]
	if last.Level != "error" || last.Context["stage"] != "serve" || last.Context["error"] == "" {
		t.Fatalf("serve failure record = %#v", last)
	}
}

func TestDiffRunStartupOutputFailureEmitsOnlyShutdownPairAndPreservesWriteError(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "same.txt", "same")
	writeComparisonFile(t, target, "same.txt", "same")
	writeFailure := errors.New("startup output unavailable")
	output := &diffFailingWriter{err: writeFailure}
	var diagnostics bytes.Buffer
	logRuntime := logging.NewRuntime(logging.Options{Writer: &diagnostics, Format: logging.JSONFormat})
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       output,
		Diagnostics:  &diagnostics,
	})
	err := runDiff(&Options{
		Context: context.Background(),
		Input: Input{
			BaselineDirectory: baseline,
			TargetDirectory:   target,
			Port:              0,
		},
		Terminal:          experience,
		NetworkInterfaces: func() ([]NetworkInterface, error) { return nil, nil },
		Logger:            logRuntime.Logger("diff"),
	})
	if !errors.Is(err, writeFailure) || output.calls != 1 {
		t.Fatalf("runDiff() error = %v, output calls = %d", err, output.calls)
	}
	records := decodeLifecycleTestRecords(t, diagnostics.String())
	messages := lifecycleTestMessages(records)
	if countString(messages, "Directory diff stopping") != 1 || countString(messages, "Directory diff stopped") != 1 || containsString(messages, "Comparison snapshot ready") || containsString(messages, "Comparison refresh cancelled") || containsString(messages, "Directory diff failed") {
		t.Fatalf("startup write failure messages = %#v", messages)
	}
	for _, record := range records {
		if (record.Message == "Directory diff stopping" || record.Message == "Directory diff stopped") && record.Context["reason"] != "startup-output-failed" {
			t.Fatalf("startup write failure record = %#v", record)
		}
	}
}

func TestDiffLifecycleSanitizesBoundedFieldsAndLoggingWriterFailureIsBestEffort(t *testing.T) {
	base := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat})
	lifecycle := newDiffLifecycle(runtime.Logger("diff"), func() time.Time { return base })
	long := strings.Repeat("x", diffLifecycleFieldLimit+200)
	lifecycle.begin(Startup{
		LocalURL:          "http://127.0.0.1:43123\x1b[31m\npassword=secret",
		NetworkURLs:       []string{"http://network.example/\x1b[2J", long},
		BaselineDirectory: "/tmp/token=secret/baseline",
		TargetDirectory:   "/tmp/target\r\n",
		Port:              43123,
	}, nil)
	if got := len(decodeLifecycleTestRecords(t, output.String())); got != 3 {
		t.Fatalf("startup record count = %d, want 3", got)
	}
	records := decodeLifecycleTestRecords(t, output.String())
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		if strings.ContainsRune(string(encoded), '\x1b') || strings.ContainsRune(string(encoded), '\r') || strings.ContainsRune(string(encoded), '\n') {
			t.Fatalf("record retained control sequence: %q", encoded)
		}
	}
	endpoints := records[1].Context["networkURLs"].([]any)
	if len(endpoints) != 2 || len([]rune(endpoints[1].(string))) != diffLifecycleFieldLimit || strings.Contains(endpoints[0].(string), "\x1b") {
		t.Fatalf("sanitized endpoint context = %#v", records[1].Context)
	}

	failing := &diffFailingWriter{err: io.ErrClosedPipe}
	failingRuntime := logging.NewRuntime(logging.Options{Writer: failing})
	failingRuntime.Logger("diff").Info("ignored writer error", map[string]any{"safe": "value"})
	if failing.calls != 1 {
		t.Fatalf("logging writer calls = %d, want 1", failing.calls)
	}
}

func TestDiffLifecycleTextSymbolsAndConfiguredLevelFiltering(t *testing.T) {
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{
		Writer: &output,
		Now:    func() time.Time { return time.Date(2026, 9, 3, 6, 0, 0, 0, time.UTC) },
	})
	runtime.SetLevel(logging.Debug)
	logger := runtime.Logger("diff")
	logger.Info("Directory diff started", nil)
	logger.Info("Diff endpoints available", nil)
	logger.Info("Comparison workspace configured", nil)
	logger.Info("Initial comparison refresh started", nil)
	logger.Info("Comparison refresh started", nil)
	logger.Debug("Comparison refresh phase", map[string]any{"phase": "discovering"})
	logger.Info("Comparison snapshot ready", nil)
	logger.Warn("Comparison snapshot contains issues", nil)
	logger.Info("Comparison refresh cancelled", nil)
	logger.Error("Comparison refresh failed", nil)
	logger.Info("Directory diff stopping", nil)
	logger.Info("Directory diff stopped", nil)
	logger.Error("Directory diff failed", nil)

	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != 13 {
		t.Fatalf("text lifecycle line count = %d, output = %q", len(lines), output.String())
	}
	for index, line := range lines {
		if strings.ContainsAny(line, "\r\n") || !strings.Contains(line, "diff:") {
			t.Fatalf("text line %d = %q", index, line)
		}
	}
	for _, expected := range []string{"●  Directory diff started", "·  Comparison refresh phase", "✓  Comparison snapshot ready", "!  Comparison snapshot contains issues", "⊘  Comparison refresh cancelled", "✕  Comparison refresh failed", "✓  Directory diff stopped", "✕  Directory diff failed"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("text output omitted %q: %q", expected, output.String())
		}
	}

	for _, levelCase := range []struct {
		name      string
		level     logging.Level
		wantDebug bool
		wantInfo  bool
		wantWarn  bool
		wantError bool
	}{
		{name: "info", level: logging.Info, wantInfo: true, wantWarn: true, wantError: true},
		{name: "warn", level: logging.Warn, wantWarn: true, wantError: true},
		{name: "error", level: logging.Error, wantError: true},
	} {
		t.Run(levelCase.name, func(t *testing.T) {
			output.Reset()
			runtime.SetLevel(levelCase.level)
			logger.Debug("Comparison refresh phase", nil)
			logger.Info("Comparison snapshot ready", nil)
			logger.Warn("Comparison snapshot contains issues", nil)
			logger.Error("Comparison refresh failed", nil)
			got := output.String()
			if strings.Contains(got, "Comparison refresh phase") != levelCase.wantDebug || strings.Contains(got, "Comparison snapshot ready") != levelCase.wantInfo || strings.Contains(got, "Comparison snapshot contains issues") != levelCase.wantWarn || strings.Contains(got, "Comparison refresh failed") != levelCase.wantError {
				t.Fatalf("level %v output = %q", levelCase.level, got)
			}
		})
	}
}

func TestDiffLifecycleNDJSONRemainsSchemaStableAndRequestsStaySilent(t *testing.T) {
	var output bytes.Buffer
	runtime := logging.NewRuntime(logging.Options{
		Writer: &output,
		Format: logging.JSONFormat,
		Now:    func() time.Time { return time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC) },
	})
	lifecycle := newDiffLifecycle(runtime.Logger("diff"), func() time.Time { return time.Now() })
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: mustTempDirectory(t), TargetDirectory: mustTempDirectory(t)})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	lifecycle.begin(Startup{LocalURL: "http://127.0.0.1:43123", NetworkURLs: []string{}, BaselineDirectory: "/b", TargetDirectory: "/t", Port: 43123}, nil)
	lifecycle.commitStartup()
	output.Reset()
	handlers := newProtocolHandlers(workspace, "127.0.0.1", lifecycle)
	rest := handlers.REST.(*diffHTTPHandler)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/refresh", nil),
		httptest.NewRequest(http.MethodPost, "/api/refresh", nil),
	} {
		request.Host = "127.0.0.1:43123"
		request.Header.Set("Origin", "https://attacker.example")
		response := httptest.NewRecorder()
		rest.ServeHTTP(response, request)
	}
	stateResponse := httptest.NewRecorder()
	rest.ServeHTTP(stateResponse, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	if output.Len() != 0 {
		t.Fatalf("ordinary/invalid requests emitted lifecycle logs: %q", output.String())
	}
	logger := runtime.Logger("diff")
	logger.Info("Comparison refresh started", map[string]any{"networkURLs": []string{}})
	scanner := bufio.NewScanner(strings.NewReader(output.String()))
	if !scanner.Scan() {
		t.Fatal("missing NDJSON lifecycle record")
	}
	var record map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
		t.Fatalf("decode NDJSON: %v", err)
	}
	if record["timestamp"] != "2026-09-03T07:00:00.000Z" || record["level"] != "info" || record["scope"] != "diff" || record["message"] != "Comparison refresh started" {
		t.Fatalf("NDJSON envelope = %#v", record)
	}
	if _, ok := record["context"].(map[string]any); !ok {
		t.Fatalf("NDJSON context = %#v", record["context"])
	}
	if strings.ContainsRune(output.String(), '\x1b') || strings.ContainsAny(output.String(), "\r\n") && strings.Count(output.String(), "\n") != 1 {
		t.Fatalf("NDJSON control/multiline output = %q", output.String())
	}
}

func TestDiffLifecycleConcurrentAttemptsAreExactlyOnceAndStopIsLast(t *testing.T) {
	base := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	var output synchronizedBuffer
	runtime := logging.NewRuntime(logging.Options{Writer: &output, Format: logging.JSONFormat, Now: func() time.Time { return base }})
	runtime.SetLevel(logging.Debug)
	lifecycle := newDiffLifecycle(runtime.Logger("diff"), func() time.Time { return base })
	lifecycle.begin(Startup{LocalURL: "http://127.0.0.1:43123", BaselineDirectory: "/b", TargetDirectory: "/t", Port: 43123}, nil)
	lifecycle.commitStartup()

	const attemptCount = 24
	var group sync.WaitGroup
	group.Add(attemptCount)
	for index := 0; index < attemptCount; index++ {
		index := index
		go func() {
			defer group.Done()
			source := "rest"
			if index%2 == 1 {
				source = "mcp"
			}
			attempt := &diffRefreshAttempt{
				lifecycle: lifecycle,
				ordinal:   index + 1,
				source:    source,
				startedAt: base,
				phaseSeen: make(map[WorkspacePhase]bool),
			}
			run := &RefreshRun{attempt: attempt, done: make(chan struct{})}
			lifecycle.attemptStarted(attempt, run)
			lifecycle.state(run, WorkspaceState{Phase: PhaseDiscovering, Progress: &WorkspaceProgress{DiscoveredEntries: index + 1}})
			lifecycle.state(run, WorkspaceState{Phase: PhaseDiscovering, Progress: &WorkspaceProgress{DiscoveredEntries: index + 2}})
			lifecycle.state(run, WorkspaceState{Phase: PhaseComparing, Progress: &WorkspaceProgress{ComparedEntries: index + 1}})
			lifecycle.state(run, WorkspaceState{Phase: PhasePublishing, Progress: &WorkspaceProgress{ComparedEntries: index + 1}})
			if index%3 == 0 {
				attempt.markCancellation("rest", false)
				run.mu.Lock()
				run.err = context.Canceled
				run.mu.Unlock()
				lifecycle.state(run, WorkspaceState{Phase: PhaseCanceled})
				return
			}
			run.mu.Lock()
			run.snapshot = &Snapshot{summary: SnapshotSummary{ID: fmt.Sprintf("snapshot-%d", index)}}
			run.mu.Unlock()
			lifecycle.state(run, WorkspaceState{Phase: PhaseReady})
		}()
	}
	group.Wait()
	lifecycle.stopping("context-cancelled")
	lifecycle.stopped("")

	records := decodeLifecycleTestRecords(t, output.String())
	starts := lifecycleTestRecordsForMessage(records, "Comparison refresh started")
	terminals := make([]lifecycleTestRecord, 0)
	for _, record := range records {
		switch record.Message {
		case "Comparison snapshot ready", "Comparison refresh cancelled", "Comparison refresh failed":
			terminals = append(terminals, record)
		}
	}
	if len(starts) != attemptCount || len(terminals) != attemptCount {
		t.Fatalf("concurrent starts/terminals = %d/%d, records = %d", len(starts), len(terminals), len(records))
	}
	seenStart := make(map[float64]bool, attemptCount)
	seenTerminal := make(map[float64]bool, attemptCount)
	for _, record := range starts {
		ordinal, ok := record.Context["refresh"].(float64)
		if !ok || seenStart[ordinal] {
			t.Fatalf("duplicate/malformed start = %#v", record)
		}
		seenStart[ordinal] = true
	}
	for _, record := range terminals {
		ordinal, ok := record.Context["refresh"].(float64)
		if !ok || seenTerminal[ordinal] {
			t.Fatalf("duplicate/malformed terminal = %#v", record)
		}
		seenTerminal[ordinal] = true
	}
	if len(seenStart) != attemptCount || len(seenTerminal) != attemptCount {
		t.Fatalf("ordinal sets = %d/%d", len(seenStart), len(seenTerminal))
	}
	if messages := lifecycleTestMessages(records); messages[len(messages)-2] != "Directory diff stopping" || messages[len(messages)-1] != "Directory diff stopped" {
		t.Fatalf("concurrent shutdown tail = %#v", messages[len(messages)-2:])
	}

	lateAttempt := &diffRefreshAttempt{lifecycle: lifecycle, ordinal: attemptCount + 1, source: "rest", startedAt: base, phaseSeen: make(map[WorkspacePhase]bool)}
	lifecycle.attemptStarted(lateAttempt, &RefreshRun{attempt: lateAttempt, done: make(chan struct{})})
	if len(decodeLifecycleTestRecords(t, output.String())) != len(records) {
		t.Fatal("late attempt emitted after service stopping")
	}
}

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func mustTempDirectory(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

type diffFailingWriter struct {
	err   error
	calls int
}

func (writer *diffFailingWriter) Write(value []byte) (int, error) {
	writer.calls++
	return 0, writer.err
}

func waitForRefreshCoordinatorClear(t *testing.T, coordinator *refreshCoordinator) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		active := coordinator.active
		coordinator.mu.Unlock()
		if active == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("refresh coordinator remained active")
}

func countString(values []string, expected string) int {
	count := 0
	for _, value := range values {
		if value == expected {
			count++
		}
	}
	return count
}

func lifecycleTestRecordsForMessage(records []lifecycleTestRecord, message string) []lifecycleTestRecord {
	result := make([]lifecycleTestRecord, 0)
	for _, record := range records {
		if record.Message == message {
			result = append(result, record)
		}
	}
	return result
}

func decodeLifecycleTestRecords(t *testing.T, contents string) []lifecycleTestRecord {
	t.Helper()
	var records []lifecycleTestRecord
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		var record lifecycleTestRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode lifecycle record %q: %v", scanner.Text(), err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan lifecycle records: %v", err)
	}
	return records
}

func lifecycleTestMessages(records []lifecycleTestRecord) []string {
	messages := make([]string, 0, len(records))
	for _, record := range records {
		messages = append(messages, record.Message)
	}
	return messages
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func intPointer(value int) *int { return &value }
