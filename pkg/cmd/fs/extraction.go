package fs

import (
	"context"
	"sync"
	"time"
)

const (
	maxExtractionPaths   = 100
	maxExtractionPath    = 4096
	maxExtractionTasks   = 100
	maxQueuedExtractions = 100
)

type ExtractionTask struct {
	ID                string `json:"id"`
	ArchivePath       string `json:"archivePath"`
	Status            string `json:"status"`
	Progress          *int   `json:"progress,omitempty"`
	UncompressedBytes *int64 `json:"uncompressedBytes,omitempty"`
	EntryCount        *int64 `json:"entryCount,omitempty"`
	DestinationPath   string `json:"destinationPath,omitempty"`
	CreatedAt         string `json:"createdAt"`
	StartedAt         string `json:"startedAt,omitempty"`
	FinishedAt        string `json:"finishedAt,omitempty"`
	Error             string `json:"error,omitempty"`
	cancel            context.CancelFunc
	startedAt         time.Time
	retryOf           string
	cancelSource      string
	lifecycleTerminal bool
	order             uint64
}

// extractionLifecycleObserver receives only real Managed Task transitions.
// It is private so the existing task HTTP representation stays unchanged.
type extractionLifecycleObserver interface {
	extractionAccepted(extractionLifecycleTask)
	extractionStarted(extractionLifecycleTask)
	extractionCompleted(extractionLifecycleTask)
	extractionCancelled(extractionLifecycleTask)
	extractionFailed(extractionLifecycleTask, error)
}

type ArchiveTaskExecutor func(context.Context, WorkspacePath, ArchiveExtractionOptions) (ArchiveExtractionResult, error)

type ExtractionManager struct {
	extract       ArchiveTaskExecutor
	mu            sync.Mutex
	tasks         map[string]*ExtractionTask
	queue         []string
	active        bool
	nextOrder     uint64
	closed        bool
	subscriptions map[*taskSubscription[ExtractionTask]]struct{}
	now           func() time.Time
	observer      extractionLifecycleObserver
	workers       sync.WaitGroup
}

func NewExtractionManager(workspace *Workspace, options ArchiveExtractionOptions) *ExtractionManager {
	return newExtractionManager(func(ctx context.Context, path WorkspacePath, callbacks ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		callbacks.Inspector = options.Inspector
		callbacks.Capacity = options.Capacity
		return workspace.ExtractArchive(ctx, path, callbacks)
	})
}

func newExtractionManager(extract ArchiveTaskExecutor) *ExtractionManager {
	return newExtractionManagerWithLifecycle(extract, time.Now, nil)
}

func newExtractionManagerWithLifecycle(extract ArchiveTaskExecutor, now func() time.Time, observer extractionLifecycleObserver) *ExtractionManager {
	if now == nil {
		now = time.Now
	}
	return &ExtractionManager{extract: extract, tasks: make(map[string]*ExtractionTask), subscriptions: make(map[*taskSubscription[ExtractionTask]]struct{}), now: now, observer: observer}
}

// setLifecycle installs the service observer before the listener is exposed.
func (manager *ExtractionManager) setLifecycle(observer extractionLifecycleObserver, now func() time.Time) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.observer = observer
	if now != nil {
		manager.now = now
	}
}

func (manager *ExtractionManager) List() []ExtractionTask {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.listLocked()
}

func (manager *ExtractionManager) listLocked() []ExtractionTask {
	result := make([]ExtractionTask, 0, len(manager.tasks))
	for _, task := range manager.tasks {
		result = append(result, publicExtractionTask(task))
	}
	for left := 0; left < len(result); left++ {
		for right := left + 1; right < len(result); right++ {
			if result[right].order > result[left].order {
				result[left], result[right] = result[right], result[left]
			}
		}
	}
	return result
}

func (manager *ExtractionManager) Subscribe() (<-chan []ExtractionTask, func()) {
	manager.mu.Lock()
	subscription := newTaskSubscription(manager.listLocked())
	manager.subscriptions[subscription] = struct{}{}
	manager.mu.Unlock()
	return subscription.output, func() {
		manager.mu.Lock()
		delete(manager.subscriptions, subscription)
		manager.mu.Unlock()
		subscription.close()
	}
}

func (manager *ExtractionManager) notifyLocked() {
	snapshot := manager.listLocked()
	for subscription := range manager.subscriptions {
		subscription.publish(snapshot)
	}
}

func (manager *ExtractionManager) closeSubscriptionsLocked() {
	for subscription := range manager.subscriptions {
		delete(manager.subscriptions, subscription)
		subscription.close()
	}
}

func (manager *ExtractionManager) Enqueue(paths []string) ([]ExtractionTask, error) {
	return manager.enqueue(paths, "")
}

func (manager *ExtractionManager) enqueue(paths []string, retryOf string) ([]ExtractionTask, error) {
	parsed, err := validateExtractionPaths(paths)
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, &ServiceError{Code: "EXTRACTION_SERVICE_STOPPED", Message: "Extraction service is stopped"}
	}
	if len(manager.queue)+len(parsed) > maxQueuedExtractions {
		return nil, &ServiceError{Code: "EXTRACTION_QUEUE_FULL", Message: "Extraction queue is full"}
	}
	manager.pruneTerminalLocked(len(parsed))
	if len(manager.tasks)+len(parsed) > maxExtractionTasks {
		return nil, &ServiceError{Code: "EXTRACTION_QUEUE_FULL", Message: "Extraction task history is full while tasks are active"}
	}
	created := make([]ExtractionTask, 0, len(parsed))
	for _, path := range parsed {
		id, err := newTaskID()
		if err != nil {
			return nil, err
		}
		task := &ExtractionTask{ID: id, ArchivePath: path.String(), Status: "queued", CreatedAt: formatTaskTime(manager.now()), retryOf: retryOf, order: manager.nextOrder}
		manager.nextOrder++
		manager.tasks[id] = task
		manager.queue = append(manager.queue, id)
		manager.emitExtractionAcceptedLocked(task)
		created = append(created, publicExtractionTask(task))
	}
	manager.pumpLocked()
	manager.notifyLocked()
	return created, nil
}

func (manager *ExtractionManager) Cancel(id string) (ExtractionTask, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	task, found := manager.tasks[id]
	if !found {
		return ExtractionTask{}, &ServiceError{Code: "EXTRACTION_NOT_FOUND", Message: "Extraction task was not found"}
	}
	if task.Status == "queued" {
		task.Status, task.FinishedAt = "cancelled", formatTaskTime(manager.now())
		task.cancelSource = "client"
		manager.removeQueuedLocked(id)
		manager.emitExtractionTerminalLocked(task, nil)
		manager.pruneTerminalLocked(0)
	} else if task.Status == "running" {
		task.Status, task.FinishedAt = "cancelled", formatTaskTime(manager.now())
		task.cancelSource = "client"
		if task.cancel != nil {
			task.cancel()
		}
		manager.emitExtractionTerminalLocked(task, nil)
		manager.pruneTerminalLocked(0)
	}
	manager.notifyLocked()
	return publicExtractionTask(task), nil
}

func (manager *ExtractionManager) Retry(id string) (ExtractionTask, error) {
	manager.mu.Lock()
	task, found := manager.tasks[id]
	if !found {
		manager.mu.Unlock()
		return ExtractionTask{}, &ServiceError{Code: "EXTRACTION_NOT_FOUND", Message: "Extraction task was not found"}
	}
	if task.Status != "error" && task.Status != "cancelled" {
		manager.mu.Unlock()
		return ExtractionTask{}, &ServiceError{Code: "EXTRACTION_ACTIVE", Message: "Only failed or cancelled extractions can be retried"}
	}
	path, oldID := task.ArchivePath, task.ID
	manager.mu.Unlock()
	created, err := manager.enqueue([]string{path}, oldID)
	if err != nil {
		return ExtractionTask{}, err
	}
	return created[0], nil
}

func (manager *ExtractionManager) ClearTerminal() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for id, task := range manager.tasks {
		if terminalExtractionStatus(task.Status) {
			delete(manager.tasks, id)
		}
	}
	manager.notifyLocked()
}

func (manager *ExtractionManager) Close() {
	_, _ = manager.closeWithStats()
}

func (manager *ExtractionManager) closeWithStats() (queued, active int) {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return 0, 0
	}
	manager.closed = true
	manager.observer = nil
	for _, id := range manager.queue {
		if task := manager.tasks[id]; task != nil && task.Status == "queued" {
			queued++
			task.Status, task.FinishedAt = "cancelled", formatTaskTime(manager.now())
			task.cancelSource = "shutdown"
		}
	}
	manager.queue = nil
	for _, task := range manager.tasks {
		if task.Status == "running" {
			active++
			task.Status, task.FinishedAt = "cancelled", formatTaskTime(manager.now())
			task.cancelSource = "shutdown"
			if task.cancel != nil {
				task.cancel()
			}
		}
	}
	manager.pruneTerminalLocked(0)
	manager.notifyLocked()
	manager.closeSubscriptionsLocked()
	manager.mu.Unlock()
	manager.workers.Wait()
	return queued, active
}

func (manager *ExtractionManager) pumpLocked() {
	if manager.active || manager.closed {
		return
	}
	for len(manager.queue) > 0 {
		id := manager.queue[0]
		manager.queue = manager.queue[1:]
		task := manager.tasks[id]
		if task == nil || task.Status != "queued" {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		task.cancel = cancel
		task.startedAt = manager.now()
		task.Status, task.StartedAt = "running", formatTaskTime(task.startedAt)
		manager.emitExtractionStartedLocked(task)
		manager.active = true
		manager.workers.Add(1)
		go manager.run(ctx, task)
		return
	}
}

func (manager *ExtractionManager) run(ctx context.Context, task *ExtractionTask) {
	defer manager.workers.Done()
	path, err := ParseWorkspacePath(task.ArchivePath)
	var result ArchiveExtractionResult
	if err == nil {
		result, err = manager.extract(ctx, path, ArchiveExtractionOptions{
			Progress:  func(value int) { manager.updateProgress(task, value) },
			OnInspect: func(inspection ArchiveInspection) { manager.updateInspection(task, inspection) },
		})
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.active = false
	task.cancel = nil
	if task.Status == "cancelled" || ctx.Err() != nil {
		task.Status = "cancelled"
		if task.cancelSource == "" {
			task.cancelSource = "client"
		}
	} else if err != nil {
		task.Status, task.Error = "error", err.Error()
	} else {
		progress := 100
		task.Status, task.Progress, task.DestinationPath = "done", &progress, result.Destination.String()
		bytes, entries := result.Inspection.UncompressedBytes, result.Inspection.EntryCount
		task.UncompressedBytes, task.EntryCount = &bytes, &entries
	}
	task.FinishedAt = formatTaskTime(manager.now())
	manager.emitExtractionTerminalLocked(task, err)
	manager.pruneTerminalLocked(0)
	manager.pumpLocked()
	manager.notifyLocked()
}

func (manager *ExtractionManager) lifecycleSnapshot() fsShutdownSnapshot {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	snapshot := fsShutdownSnapshot{}
	for _, task := range manager.tasks {
		switch task.Status {
		case "queued":
			snapshot.QueuedExtractions++
		case "running":
			snapshot.ActiveExtractions++
		}
	}
	return snapshot
}

func (manager *ExtractionManager) extractionLifecycleSnapshotLocked(task *ExtractionTask) extractionLifecycleTask {
	if task == nil {
		return extractionLifecycleTask{}
	}
	return extractionLifecycleTask{
		ID:                task.ID,
		ArchivePath:       task.ArchivePath,
		DestinationPath:   task.DestinationPath,
		Progress:          task.Progress,
		UncompressedBytes: task.UncompressedBytes,
		EntryCount:        task.EntryCount,
		StartedAt:         task.startedAt,
		RetryOf:           task.retryOf,
	}
}

func (manager *ExtractionManager) emitExtractionAcceptedLocked(task *ExtractionTask) {
	if manager.observer != nil {
		manager.observer.extractionAccepted(manager.extractionLifecycleSnapshotLocked(task))
	}
}

func (manager *ExtractionManager) emitExtractionStartedLocked(task *ExtractionTask) {
	if manager.observer != nil {
		manager.observer.extractionStarted(manager.extractionLifecycleSnapshotLocked(task))
	}
}

func (manager *ExtractionManager) emitExtractionTerminalLocked(task *ExtractionTask, err error) {
	if task == nil || task.lifecycleTerminal {
		return
	}
	task.lifecycleTerminal = true
	if manager.observer == nil {
		return
	}
	snapshot := manager.extractionLifecycleSnapshotLocked(task)
	switch task.Status {
	case "done":
		manager.observer.extractionCompleted(snapshot)
	case "cancelled":
		manager.observer.extractionCancelled(snapshot)
	case "error":
		manager.observer.extractionFailed(snapshot, err)
	}
}

func (manager *ExtractionManager) updateProgress(task *ExtractionTask, value int) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if task.Status != "running" {
		return
	}
	value = min(value, 100)
	task.Progress = &value
	manager.notifyLocked()
}

func (manager *ExtractionManager) updateInspection(task *ExtractionTask, inspection ArchiveInspection) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if task.Status != "running" {
		return
	}
	bytes, entries := inspection.UncompressedBytes, inspection.EntryCount
	progress := 0
	task.UncompressedBytes, task.EntryCount, task.Progress = &bytes, &entries, &progress
	manager.notifyLocked()
}

func (manager *ExtractionManager) removeQueuedLocked(id string) {
	for index, queued := range manager.queue {
		if queued == id {
			manager.queue = append(manager.queue[:index], manager.queue[index+1:]...)
			return
		}
	}
}

func (manager *ExtractionManager) pruneTerminalLocked(required int) {
	for len(manager.tasks)+required > maxExtractionTasks {
		var oldest *ExtractionTask
		for _, task := range manager.tasks {
			if terminalExtractionStatus(task.Status) && (oldest == nil || task.order < oldest.order) {
				oldest = task
			}
		}
		if oldest == nil {
			return
		}
		delete(manager.tasks, oldest.ID)
	}
}

func validateExtractionPaths(paths []string) ([]WorkspacePath, error) {
	if len(paths) == 0 || len(paths) > maxExtractionPaths {
		return nil, &ServiceError{Code: "INVALID_EXTRACTION", Message: "Extraction requests must contain between 1 and 100 paths"}
	}
	result := make([]WorkspacePath, 0, len(paths))
	for _, raw := range paths {
		if raw == "" || len(raw) > maxExtractionPath {
			return nil, &ServiceError{Code: "INVALID_EXTRACTION", Message: "Archive path is invalid"}
		}
		path, err := ParseWorkspacePath(raw)
		if err != nil {
			return nil, &ServiceError{Code: "INVALID_EXTRACTION", Message: "Archive path is invalid"}
		}
		result = append(result, path)
	}
	return result, nil
}

func terminalExtractionStatus(status string) bool {
	return status == "done" || status == "error" || status == "cancelled"
}

func publicExtractionTask(task *ExtractionTask) ExtractionTask {
	result := *task
	result.cancel = nil
	result.startedAt = time.Time{}
	result.retryOf = ""
	result.cancelSource = ""
	result.lifecycleTerminal = false
	return result
}
