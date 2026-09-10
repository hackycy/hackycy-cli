package diff

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

var ErrRefreshStopped = errors.New("the comparison service is stopping")

type refreshCoordinator struct {
	workspace   *Workspace
	lifecycle   *diffLifecycle
	mu          sync.Mutex
	active      *RefreshRun
	lastStarted *RefreshRun
	nextOrdinal int
	accepting   bool
}

func newRefreshCoordinator(workspace *Workspace, lifecycles ...*diffLifecycle) *refreshCoordinator {
	var lifecycle *diffLifecycle
	if len(lifecycles) > 0 {
		lifecycle = lifecycles[0]
	}
	return &refreshCoordinator{workspace: workspace, lifecycle: lifecycle, accepting: true}
}

func (handler *diffHTTPHandler) serveRefresh(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost && request.Method != http.MethodDelete {
		writeHTTPError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST or DELETE")
		return
	}
	if !isHTTPRefreshOriginAllowed(request) {
		writeHTTPError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Refresh requests must be same-origin")
		return
	}
	if request.Method == http.MethodDelete {
		handler.cancelHTTPRefresh()
		writeHTTPNoContent(writer)
		return
	}

	if err := handler.startHTTPRefresh(); err != nil {
		if errors.Is(err, ErrRefreshActive) {
			writeHTTPError(writer, http.StatusConflict, "REFRESH_ACTIVE", "A refresh is already active")
			return
		}
		writeHTTPError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Refresh could not be started")
		return
	}
	writeHTTPJSON(writer, http.StatusAccepted, struct {
		Accepted bool `json:"accepted"`
	}{Accepted: true})
}

func isHTTPRefreshOriginAllowed(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+request.Host
}

func (handler *diffHTTPHandler) startHTTPRefresh() error {
	return handler.refresh.StartSource("rest")
}

func (handler *diffHTTPHandler) cancelHTTPRefresh() {
	handler.refresh.Cancel()
}

func (coordinator *refreshCoordinator) Start() error {
	return coordinator.StartSource("rest")
}

func (coordinator *refreshCoordinator) StartInitial() error {
	return coordinator.StartSource("initial")
}

func (coordinator *refreshCoordinator) StartSource(source string) error {
	coordinator.mu.Lock()
	if !coordinator.accepting {
		coordinator.mu.Unlock()
		return ErrRefreshStopped
	}
	if coordinator.active != nil {
		coordinator.mu.Unlock()
		return ErrRefreshActive
	}
	if source == "" {
		source = "rest"
	}
	source = sanitizeDiffField(source)
	if source == "" {
		source = "rest"
	}
	attempt := &diffRefreshAttempt{
		lifecycle:          coordinator.lifecycle,
		ordinal:            coordinator.nextOrdinal + 1,
		source:             source,
		startedAt:          coordinator.lifecycleNow(),
		phaseSeen:          make(map[WorkspacePhase]bool),
		previousSnapshotID: coordinator.workspace.publishedSnapshotID(),
	}
	run, err := coordinator.workspace.startRefresh(context.Background(), attempt)
	if err != nil {
		coordinator.mu.Unlock()
		return err
	}
	coordinator.nextOrdinal = attempt.ordinal
	coordinator.active = run
	coordinator.lastStarted = run
	go coordinator.clear(run)
	coordinator.mu.Unlock()
	if coordinator.lifecycle != nil {
		coordinator.lifecycle.attemptStarted(attempt, run)
	}
	return nil
}

func (coordinator *refreshCoordinator) Cancel() {
	coordinator.CancelSource("rest")
}

func (coordinator *refreshCoordinator) CancelSource(source string) {
	coordinator.mu.Lock()
	run := coordinator.active
	coordinator.mu.Unlock()
	if run != nil {
		if run.attempt != nil {
			run.attempt.markCancellation(source, false)
		}
		run.Cancel()
	}
}

func (coordinator *refreshCoordinator) CancelAndWait() {
	coordinator.StopAndCancel()
	coordinator.waitActive()
}

func (coordinator *refreshCoordinator) StopAndCancel() {
	coordinator.mu.Lock()
	coordinator.accepting = false
	run := coordinator.active
	coordinator.mu.Unlock()
	if run != nil {
		if run.attempt != nil {
			run.attempt.markCancellation("shutdown", true)
		}
		run.Cancel()
	}
}

func (coordinator *refreshCoordinator) waitActive() {
	coordinator.mu.Lock()
	run := coordinator.active
	coordinator.mu.Unlock()
	if run == nil {
		return
	}
	run.Cancel()
	_, _ = run.Wait(context.Background())
}

func (coordinator *refreshCoordinator) lifecycleNow() time.Time {
	if coordinator.lifecycle == nil || coordinator.lifecycle.now == nil {
		return time.Now()
	}
	return coordinator.lifecycle.now()
}

func (coordinator *refreshCoordinator) clear(run *RefreshRun) {
	_, _ = run.Wait(context.Background())
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.active == run {
		coordinator.active = nil
	}
}
