package diff

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
)

const diffLifecycleFieldLimit = 1024

// diffRefreshAttempt is the service-owned identity for one accepted refresh.
// Workspace keeps the attempt attached to its RefreshRun so callbacks retain
// their identity even when a refresh completes before the caller observes it.
type diffRefreshAttempt struct {
	lifecycle *diffLifecycle
	ordinal   int
	source    string
	startedAt time.Time

	run *RefreshRun

	mu                   sync.Mutex
	started              bool
	startEmitted         bool
	terminal             bool
	shutdown             bool
	cancelSource         string
	previousSnapshotID   string
	phaseSeen            map[WorkspacePhase]bool
	pendingEvents        []diffLifecycleEvent
	lastProgress         *WorkspaceProgress
	terminalSnapshot     *Snapshot
	terminalError        error
	terminalState        WorkspacePhase
	terminalEventEmitted bool
}

type diffLifecycleEvent struct {
	attempt *diffRefreshAttempt
	start   bool
	action  func()
}

// diffLifecycle is a small service-local sequencer. Startup records and
// refresh observations are held until the stdout startup checkpoint commits;
// this keeps an immediately-completing initial refresh from racing stdout.
type diffLifecycle struct {
	logger logging.Logger
	now    func() time.Time

	mu              sync.Mutex
	startupBegun    bool
	stdoutCommitted bool
	aborted         bool
	preStartup      []diffLifecycleEvent
	pending         []diffLifecycleEvent
	stoppingStarted bool
	terminal        bool
}

func newDiffLifecycle(logger logging.Logger, now func() time.Time) *diffLifecycle {
	if now == nil {
		now = time.Now
	}
	return &diffLifecycle{logger: logger, now: now}
}

func (lifecycle *diffLifecycle) begin(startup Startup, initial *RefreshRun) {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.startupBegun {
		return
	}
	lifecycle.startupBegun = true
	lifecycle.logger.Info("Directory diff started", map[string]any{
		"localURL": sanitizeDiffField(startup.LocalURL),
		"public":   startup.Public,
		"port":     startup.Port,
	})
	lifecycle.logger.Info("Diff endpoints available", map[string]any{
		"mcpURL":      sanitizeDiffField(startup.LocalURL + "/mcp"),
		"networkURLs": sanitizeDiffURLs(startup.NetworkURLs),
	})
	lifecycle.logger.Info("Comparison workspace configured", map[string]any{
		"baselineDirectory": sanitizeDiffField(startup.BaselineDirectory),
		"targetDirectory":   sanitizeDiffField(startup.TargetDirectory),
	})

	var initialAttempt *diffRefreshAttempt
	if initial != nil {
		initialAttempt = initial.attempt
	}
	if initialAttempt != nil {
		lifecycle.emitStartLocked(initialAttempt, true)
		initialAttempt.mu.Lock()
		initialAttempt.started = true
		initialAttempt.startEmitted = true
		queued := append([]diffLifecycleEvent(nil), initialAttempt.pendingEvents...)
		initialAttempt.pendingEvents = nil
		initialAttempt.mu.Unlock()
		lifecycle.pending = append(lifecycle.pending, queued...)
	}
	for _, event := range lifecycle.preStartup {
		if event.start && event.attempt == initialAttempt {
			continue
		}
		lifecycle.pending = append(lifecycle.pending, event)
	}
	lifecycle.preStartup = nil
}

// commitStartup records that the durable stdout checkpoint was accepted and
// releases all queued lifecycle observations in their service-local order.
func (lifecycle *diffLifecycle) commitStartup() {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	if lifecycle.aborted || lifecycle.stoppingStarted || lifecycle.terminal {
		lifecycle.mu.Unlock()
		return
	}
	lifecycle.stdoutCommitted = true
	events := append([]diffLifecycleEvent(nil), lifecycle.pending...)
	lifecycle.pending = nil
	for _, event := range events {
		if event.action != nil {
			event.action()
		}
	}
	lifecycle.mu.Unlock()
}

// abort discards observations for a startup that never produced its result.
func (lifecycle *diffLifecycle) abort() {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	lifecycle.aborted = true
	lifecycle.preStartup = nil
	lifecycle.pending = nil
	lifecycle.mu.Unlock()
}

func (lifecycle *diffLifecycle) attemptStarted(attempt *diffRefreshAttempt, run *RefreshRun) {
	if lifecycle == nil || attempt == nil {
		return
	}
	attempt.mu.Lock()
	attempt.run = run
	attempt.mu.Unlock()
	lifecycle.mu.Lock()
	if lifecycle.aborted || lifecycle.stoppingStarted || lifecycle.terminal {
		lifecycle.mu.Unlock()
		return
	}
	attempt.mu.Lock()
	queued := append([]diffLifecycleEvent(nil), attempt.pendingEvents...)
	attempt.pendingEvents = nil
	emitStart := !attempt.startEmitted
	attempt.startEmitted = true
	attempt.started = true
	attempt.mu.Unlock()
	events := queued
	if emitStart {
		event := diffLifecycleEvent{
			attempt: attempt,
			start:   true,
			action: func() {
				lifecycle.emitStartDirect(attempt, false)
			},
		}
		events = append([]diffLifecycleEvent{event}, events...)
	}
	if !lifecycle.startupBegun {
		lifecycle.preStartup = append(lifecycle.preStartup, events...)
		lifecycle.mu.Unlock()
		return
	}
	if !lifecycle.stdoutCommitted {
		lifecycle.pending = append(lifecycle.pending, events...)
		lifecycle.mu.Unlock()
		return
	}
	for _, event := range events {
		if event.action != nil {
			event.action()
		}
	}
	lifecycle.mu.Unlock()
}

func (lifecycle *diffLifecycle) emitStartLocked(attempt *diffRefreshAttempt, initial bool) {
	lifecycle.emitStartDirect(attempt, initial)
}

// emitStartDirect is called while the lifecycle mutex is already held.
func (lifecycle *diffLifecycle) emitStartDirect(attempt *diffRefreshAttempt, initial bool) {
	if lifecycle == nil || attempt == nil {
		return
	}
	message := "Comparison refresh started"
	if initial {
		message = "Initial comparison refresh started"
	}
	lifecycle.logger.Info(message, map[string]any{
		"refresh": attempt.ordinal,
		"source":  sanitizeDiffField(attempt.source),
	})
}

func (lifecycle *diffLifecycle) state(run *RefreshRun, state WorkspaceState) {
	if lifecycle == nil || run == nil || run.attempt == nil {
		return
	}
	attempt := run.attempt
	var events []diffLifecycleEvent
	attempt.mu.Lock()
	if state.Progress != nil {
		progress := cloneWorkspaceState(WorkspaceState{Progress: state.Progress}).Progress
		attempt.lastProgress = progress
	}
	if attempt.terminal {
		attempt.mu.Unlock()
		return
	}
	if state.Phase == PhaseDiscovering || state.Phase == PhaseComparing || state.Phase == PhasePublishing {
		if !attempt.phaseSeen[state.Phase] {
			attempt.phaseSeen[state.Phase] = true
			progress := cloneWorkspaceState(WorkspaceState{Progress: state.Progress}).Progress
			events = append(events, diffLifecycleEvent{
				attempt: attempt,
				action: func() {
					fields := map[string]any{
						"refresh": attempt.ordinal,
						"source":  sanitizeDiffField(attempt.source),
						"phase":   sanitizeDiffField(string(state.Phase)),
					}
					addDiffProgressFields(fields, progress)
					lifecycle.logger.Debug("Comparison refresh phase", fields)
				},
			})
		}
	}
	if state.Phase == PhaseReady || state.Phase == PhaseCanceled || state.Phase == PhaseError {
		attempt.terminal = true
		attempt.terminalState = state.Phase
		attempt.terminalSnapshot = run.snapshotValue()
		attempt.terminalError = run.errorValue()
		attempt.terminalEventEmitted = true
		if attempt.shutdown && state.Phase != PhaseReady {
			attempt.mu.Unlock()
			lifecycle.schedule(events)
			return
		}
		// A refresh that reached Ready before shutdown remains a successful
		// observation. Only cancellation/error caused by shutdown is folded
		// into the service stopping record.
		terminalEvent := lifecycle.makeTerminalEvent(attempt, state, attempt.shutdown && state.Phase != PhaseReady)
		events = append(events, terminalEvent)
	}
	if !attempt.started {
		attempt.pendingEvents = append(attempt.pendingEvents, events...)
		attempt.mu.Unlock()
		return
	}
	attempt.mu.Unlock()
	lifecycle.schedule(events)
}

func (lifecycle *diffLifecycle) makeTerminalEvent(attempt *diffRefreshAttempt, state WorkspaceState, suppressed bool) diffLifecycleEvent {
	return diffLifecycleEvent{
		attempt: attempt,
		action: func() {
			attempt.mu.Lock()
			progress := attempt.lastProgress
			snapshot := attempt.terminalSnapshot
			err := attempt.terminalError
			cancelSource := attempt.cancelSource
			previousSnapshotID := attempt.previousSnapshotID
			attempt.mu.Unlock()
			if suppressed {
				return
			}
			fields := map[string]any{
				"refresh": attempt.ordinal,
				"source":  sanitizeDiffField(attempt.source),
			}
			duration := lifecycle.durationMilliseconds(attempt.startedAt)
			fields["durationMs"] = duration
			switch {
			case state.Phase == PhaseReady && snapshot != nil:
				summary := snapshot.Summary()
				fields["snapshotID"] = sanitizeDiffField(summary.ID)
				fields["added"] = summary.Counts.Added
				fields["deleted"] = summary.Counts.Deleted
				fields["modified"] = summary.Counts.Modified
				fields["unchanged"] = summary.Counts.Unchanged
				fields["issues"] = summary.Issues
				addDiffProgressFields(fields, progress)
				lifecycle.logger.Info("Comparison snapshot ready", fields)
				if summary.Issues > 0 {
					lifecycle.logger.Warn("Comparison snapshot contains issues", map[string]any{
						"refresh":    attempt.ordinal,
						"snapshotID": sanitizeDiffField(summary.ID),
						"issues":     summary.Issues,
					})
				}
			case state.Phase == PhaseCanceled || errors.Is(err, context.Canceled):
				if cancelSource == "" {
					cancelSource = "context"
				}
				fields["cancelSource"] = sanitizeDiffField(cancelSource)
				addDiffProgressFields(fields, progress)
				addPreviousSnapshotFields(fields, previousSnapshotID)
				lifecycle.logger.Info("Comparison refresh cancelled", fields)
			default:
				fields["error"] = safeDiffError(err, state.Error)
				addDiffProgressFields(fields, progress)
				addPreviousSnapshotFields(fields, previousSnapshotID)
				lifecycle.logger.Error("Comparison refresh failed", fields)
			}
		},
	}
}

func (lifecycle *diffLifecycle) schedule(events []diffLifecycleEvent) {
	if lifecycle == nil || len(events) == 0 {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.stoppingStarted || lifecycle.terminal {
		return
	}
	if !lifecycle.startupBegun {
		lifecycle.preStartup = append(lifecycle.preStartup, events...)
		return
	}
	if !lifecycle.stdoutCommitted {
		lifecycle.pending = append(lifecycle.pending, events...)
		return
	}
	for _, event := range events {
		if event.action != nil {
			event.action()
		}
	}
}

func (lifecycle *diffLifecycle) stopping(reason string) {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.stoppingStarted || lifecycle.terminal || !lifecycle.startupBegun {
		return
	}
	lifecycle.stoppingStarted = true
	fields := map[string]any{"reason": sanitizeDiffField(reason)}
	lifecycle.emitOrQueueLocked(diffLifecycleEvent{action: func() {
		lifecycle.logger.Info("Directory diff stopping", fields)
	}})
}

func (lifecycle *diffLifecycle) stopped(reason string) {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.terminal || !lifecycle.startupBegun {
		return
	}
	lifecycle.terminal = true
	fields := map[string]any{}
	if reason != "" {
		fields["reason"] = sanitizeDiffField(reason)
	}
	lifecycle.emitOrQueueLocked(diffLifecycleEvent{action: func() {
		lifecycle.logger.Info("Directory diff stopped", fields)
	}})
}

func (lifecycle *diffLifecycle) failed(stage string, err error) {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.terminal || !lifecycle.startupBegun {
		return
	}
	lifecycle.terminal = true
	fields := map[string]any{
		"stage": sanitizeDiffField(stage),
		"error": safeDiffError(err, ""),
	}
	lifecycle.emitOrQueueLocked(diffLifecycleEvent{action: func() {
		lifecycle.logger.Error("Directory diff failed", fields)
	}})
}

// startupOutputFailureStarted closes the lifecycle gate before the service is
// torn down after a failed stdout startup checkpoint. Late refresh callbacks
// are discarded instead of being emitted after the explicit stopping record.
func (lifecycle *diffLifecycle) startupOutputFailureStarted() {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.terminal {
		lifecycle.aborted = true
		lifecycle.preStartup = nil
		lifecycle.pending = nil
		return
	}
	lifecycle.aborted = true
	lifecycle.preStartup = nil
	lifecycle.pending = nil
	lifecycle.startupBegun = true
	lifecycle.stdoutCommitted = true
	lifecycle.stoppingStarted = true
	lifecycle.logger.Info("Directory diff stopping", map[string]any{
		"reason": "startup-output-failed",
	})
}

// startupOutputFailureFinished emits the terminal startup-failure record after
// the server has closed. It intentionally bypasses the aborted gate because
// the two records are the only lifecycle evidence for this failure path.
func (lifecycle *diffLifecycle) startupOutputFailureFinished() {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.terminal {
		return
	}
	lifecycle.terminal = true
	lifecycle.logger.Info("Directory diff stopped", map[string]any{
		"reason": "startup-output-failed",
	})
}

func (lifecycle *diffLifecycle) emitOrQueueLocked(event diffLifecycleEvent) {
	if lifecycle.stdoutCommitted {
		if event.action != nil {
			event.action()
		}
		return
	}
	lifecycle.pending = append(lifecycle.pending, event)
}

func (lifecycle *diffLifecycle) durationMilliseconds(start time.Time) int64 {
	duration := lifecycle.now().Sub(start)
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}

func (attempt *diffRefreshAttempt) markCancellation(source string, shutdown bool) {
	if attempt == nil {
		return
	}
	attempt.mu.Lock()
	if source != "" && attempt.cancelSource == "" {
		attempt.cancelSource = source
	}
	if shutdown {
		attempt.shutdown = true
	}
	attempt.mu.Unlock()
}

func addDiffProgressFields(fields map[string]any, progress *WorkspaceProgress) {
	if progress == nil {
		return
	}
	fields["discoveredEntries"] = progress.DiscoveredEntries
	fields["comparedEntries"] = progress.ComparedEntries
	if progress.TotalEntries != nil {
		fields["totalEntries"] = *progress.TotalEntries
	}
	fields["comparedBytes"] = progress.ComparedBytes
	if progress.TotalBytes != nil {
		fields["totalBytes"] = *progress.TotalBytes
	}
	fields["issues"] = progress.Issues
}

func addPreviousSnapshotFields(fields map[string]any, previousSnapshotID string) {
	fields["hasPreviousSnapshot"] = previousSnapshotID != ""
	if previousSnapshotID != "" {
		fields["previousSnapshotID"] = sanitizeDiffField(previousSnapshotID)
	}
}

func sanitizeDiffURLs(urls []string) []string {
	result := make([]string, 0, len(urls))
	for _, value := range urls {
		result = append(result, sanitizeDiffField(value))
	}
	if result == nil {
		return []string{}
	}
	return result
}

func sanitizeDiffField(value string) string {
	value = logging.RedactDiagnostic(value)
	if len([]rune(value)) <= diffLifecycleFieldLimit {
		return value
	}
	runes := []rune(value)
	return string(runes[:diffLifecycleFieldLimit])
}

func safeDiffError(err error, fallback string) string {
	if err != nil {
		return sanitizeDiffField(err.Error())
	}
	return sanitizeDiffField(fallback)
}

func cloneDiffAttemptProgress(progress *WorkspaceProgress) *WorkspaceProgress {
	if progress == nil {
		return nil
	}
	copy := *progress
	if progress.TotalEntries != nil {
		value := *progress.TotalEntries
		copy.TotalEntries = &value
	}
	if progress.TotalBytes != nil {
		value := *progress.TotalBytes
		copy.TotalBytes = &value
	}
	return &copy
}
