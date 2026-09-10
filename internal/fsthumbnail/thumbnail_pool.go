package fsthumbnail

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const (
	thumbnailWorkerCount       = 2
	thumbnailWorkerQueue       = 128
	thumbnailWorkerTimeout     = 5 * time.Second
	thumbnailWorkerStderrLimit = 8 << 10
)

// Converter converts validated image bytes into a thumbnail.
type Converter interface {
	Convert(mimeType string, source []byte) ([]byte, error)
}

// Pool owns persistent private thumbnail worker processes.
type Pool interface {
	Converter
	Close()
}

type thumbnailWorkerPoolOptions struct {
	workerCount int
	maxQueued   int
	timeout     time.Duration
	launch      func() (*thumbnailWorkerProcess, error)
}

type thumbnailWorkerPool struct {
	mu           sync.Mutex
	workers      []*thumbnailWorkerSlot
	queue        []*thumbnailWorkerTask
	closed       bool
	nextID       uint64
	maxQueued    int
	timeout      time.Duration
	launch       func() (*thumbnailWorkerProcess, error)
	replacements sync.WaitGroup
}

type thumbnailWorkerSlot struct {
	process    *thumbnailWorkerProcess
	task       *thumbnailWorkerTask
	generation uint64
	replacing  bool
}

type thumbnailWorkerTask struct {
	id       uint64
	mimeType string
	source   []byte
	timer    *time.Timer
	done     chan thumbnailWorkerResult
}

type thumbnailWorkerResult struct {
	thumbnail []byte
	err       error
}

type thumbnailWorkerAssignment struct {
	slot       *thumbnailWorkerSlot
	process    *thumbnailWorkerProcess
	task       *thumbnailWorkerTask
	generation uint64
}

type thumbnailWorkerProcess struct {
	command  *exec.Cmd
	input    io.WriteCloser
	output   io.ReadCloser
	stderr   *thumbnailWorkerStderr
	waitOnce sync.Once
	waitErr  error
}

type thumbnailWorkerStderr struct {
	mu   sync.Mutex
	data []byte
}

func newThumbnailWorkerPool(options thumbnailWorkerPoolOptions) (*thumbnailWorkerPool, error) {
	if options.workerCount == 0 {
		options.workerCount = thumbnailWorkerCount
	}
	if options.maxQueued == 0 {
		options.maxQueued = thumbnailWorkerQueue
	}
	if options.timeout == 0 {
		options.timeout = thumbnailWorkerTimeout
	}
	if options.launch == nil {
		executable, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve thumbnail worker executable: %w", err)
		}
		options.launch = func() (*thumbnailWorkerProcess, error) {
			return startThumbnailWorkerProcess(exec.Command(executable, thumbnailWorkerArgument))
		}
	}
	return &thumbnailWorkerPool{
		workers:   make([]*thumbnailWorkerSlot, options.workerCount),
		maxQueued: options.maxQueued,
		timeout:   options.timeout,
		launch:    options.launch,
	}, nil
}

// NewPool starts the lazily populated worker pool used by the FS command.
func NewPool() (Pool, error) {
	return newThumbnailWorkerPool(thumbnailWorkerPoolOptions{})
}

func startThumbnailWorkerProcess(command *exec.Cmd) (*thumbnailWorkerProcess, error) {
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	process := &thumbnailWorkerProcess{input: input, output: output, command: command, stderr: &thumbnailWorkerStderr{}}
	command.Stderr = process.stderr
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	return process, nil
}

func (pool *thumbnailWorkerPool) convert(mimeType string, source []byte) ([]byte, error) {
	task := &thumbnailWorkerTask{
		mimeType: mimeType,
		source:   append([]byte(nil), source...),
		done:     make(chan thumbnailWorkerResult, 1),
	}
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return nil, thumbnailWorkerPoolError("THUMBNAIL_STOPPED", "Thumbnail service is stopped")
	}
	if len(pool.queue) >= pool.maxQueued {
		pool.mu.Unlock()
		return nil, thumbnailWorkerPoolError("THUMBNAIL_QUEUE_FULL", "Thumbnail queue is full")
	}
	task.id = pool.nextID + 1
	pool.nextID = task.id
	pool.queue = append(pool.queue, task)
	pool.ensureWorkersLocked()
	assignments := pool.dispatchLocked()
	pool.mu.Unlock()
	pool.startAssignments(assignments)
	result := <-task.done
	return result.thumbnail, result.err
}

// Convert dispatches one conversion to a private worker.
func (pool *thumbnailWorkerPool) Convert(mimeType string, source []byte) ([]byte, error) {
	return pool.convert(mimeType, source)
}

func (pool *thumbnailWorkerPool) close() {
	pool.mu.Lock()
	if pool.closed {
		pool.mu.Unlock()
		return
	}
	pool.closed = true
	for _, task := range pool.queue {
		pool.finishTaskLocked(task, thumbnailWorkerResult{err: thumbnailWorkerPoolError("THUMBNAIL_STOPPED", "Thumbnail service is stopped")})
	}
	pool.queue = nil
	processes := make([]*thumbnailWorkerProcess, 0, len(pool.workers))
	for _, slot := range pool.workers {
		if slot == nil {
			continue
		}
		if slot.task != nil {
			pool.finishTaskLocked(slot.task, thumbnailWorkerResult{err: thumbnailWorkerPoolError("THUMBNAIL_STOPPED", "Thumbnail service is stopped")})
			slot.task = nil
		}
		if slot.process != nil {
			processes = append(processes, slot.process)
			slot.process = nil
			slot.replacing = true
		}
	}
	pool.mu.Unlock()
	for _, process := range processes {
		_ = process.terminateAndWait()
	}
	pool.replacements.Wait()
	pool.mu.Lock()
	for _, slot := range pool.workers {
		if slot == nil {
			continue
		}
		slot.replacing = false
	}
	pool.mu.Unlock()
}

// Close stops all private workers and unblocks queued conversion requests.
func (pool *thumbnailWorkerPool) Close() {
	pool.close()
}

func (pool *thumbnailWorkerPool) ensureWorkersLocked() {
	for index, slot := range pool.workers {
		if slot == nil {
			slot = &thumbnailWorkerSlot{}
			pool.workers[index] = slot
		}
		if slot.process != nil || slot.replacing {
			continue
		}
		process, err := pool.launch()
		if err == nil {
			slot.process = process
		}
	}
	if !pool.hasRunnableWorkerLocked() && !pool.hasReplacementLocked() {
		pool.rejectQueuedLocked(thumbnailWorkerPoolError("THUMBNAIL_WORKER_FAILED", "Thumbnail worker failed"))
	}
}

func (pool *thumbnailWorkerPool) dispatchLocked() []thumbnailWorkerAssignment {
	if pool.closed {
		return nil
	}
	assignments := make([]thumbnailWorkerAssignment, 0, len(pool.workers))
	for _, slot := range pool.workers {
		if slot == nil || slot.process == nil || slot.replacing || slot.task != nil || len(pool.queue) == 0 {
			continue
		}
		task := pool.queue[0]
		pool.queue = pool.queue[1:]
		slot.task = task
		slot.generation++
		generation := slot.generation
		process := slot.process
		task.timer = time.AfterFunc(pool.timeout, func() {
			pool.timeoutWorker(slot, process, task, generation)
		})
		assignments = append(assignments, thumbnailWorkerAssignment{slot: slot, process: process, task: task, generation: generation})
	}
	return assignments
}

func (pool *thumbnailWorkerPool) startAssignments(assignments []thumbnailWorkerAssignment) {
	for _, assignment := range assignments {
		go pool.runAssignment(assignment)
	}
}

func (pool *thumbnailWorkerPool) runAssignment(assignment thumbnailWorkerAssignment) {
	if err := writeThumbnailWorkerRequest(assignment.process.input, thumbnailWorkerRequest{
		id:       assignment.task.id,
		mimeType: assignment.task.mimeType,
		source:   assignment.task.source,
	}); err != nil {
		pool.failWorker(assignment, err)
		return
	}
	response, err := readThumbnailWorkerResponse(assignment.process.output)
	if err != nil || response.id != assignment.task.id {
		if err == nil {
			err = fmt.Errorf("thumbnail worker: response ID mismatch")
		}
		pool.failWorker(assignment, err)
		return
	}
	if !response.ok {
		pool.completeAssignment(assignment, thumbnailWorkerResult{err: thumbnailWorkerPoolError("THUMBNAIL_INVALID", string(response.payload))})
		return
	}
	pool.completeAssignment(assignment, thumbnailWorkerResult{thumbnail: response.payload})
}

func (pool *thumbnailWorkerPool) completeAssignment(assignment thumbnailWorkerAssignment, result thumbnailWorkerResult) {
	pool.mu.Lock()
	if pool.closed || assignment.slot.process != assignment.process || assignment.slot.task != assignment.task || assignment.slot.generation != assignment.generation {
		pool.mu.Unlock()
		return
	}
	pool.finishTaskLocked(assignment.task, result)
	assignment.slot.task = nil
	assignments := pool.dispatchLocked()
	pool.mu.Unlock()
	pool.startAssignments(assignments)
}

func (pool *thumbnailWorkerPool) timeoutWorker(slot *thumbnailWorkerSlot, process *thumbnailWorkerProcess, task *thumbnailWorkerTask, generation uint64) {
	pipeline := thumbnailWorkerAssignment{slot: slot, process: process, task: task, generation: generation}
	pool.detachFailedWorker(pipeline, thumbnailWorkerPoolError("THUMBNAIL_TIMEOUT", "Thumbnail conversion timed out"))
}

func (pool *thumbnailWorkerPool) failWorker(assignment thumbnailWorkerAssignment, cause error) {
	pool.detachFailedWorker(assignment, &Error{Code: "THUMBNAIL_WORKER_FAILED", Message: "Thumbnail worker failed", Cause: cause})
}

func (pool *thumbnailWorkerPool) detachFailedWorker(assignment thumbnailWorkerAssignment, result error) {
	pool.mu.Lock()
	if pool.closed || assignment.slot.process != assignment.process || assignment.slot.task != assignment.task || assignment.slot.generation != assignment.generation {
		pool.mu.Unlock()
		return
	}
	pool.finishTaskLocked(assignment.task, thumbnailWorkerResult{err: result})
	assignment.slot.task = nil
	assignment.slot.process = nil
	assignment.slot.replacing = true
	assignments := pool.dispatchLocked()
	pool.replacements.Add(1)
	pool.mu.Unlock()
	pool.startAssignments(assignments)
	go pool.replaceWorker(assignment.slot, assignment.process)
}

func (pool *thumbnailWorkerPool) replaceWorker(slot *thumbnailWorkerSlot, process *thumbnailWorkerProcess) {
	defer pool.replacements.Done()
	_ = process.terminateAndWait()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed {
		slot.replacing = false
		return
	}
	replacement, err := pool.launch()
	slot.replacing = false
	if err == nil {
		slot.process = replacement
	}
	if !pool.hasRunnableWorkerLocked() && !pool.hasReplacementLocked() {
		pool.rejectQueuedLocked(thumbnailWorkerPoolError("THUMBNAIL_WORKER_FAILED", "Thumbnail worker failed"))
		return
	}
	assignments := pool.dispatchLocked()
	go pool.startAssignments(assignments)
}

func (pool *thumbnailWorkerPool) finishTaskLocked(task *thumbnailWorkerTask, result thumbnailWorkerResult) {
	if task.timer != nil {
		task.timer.Stop()
	}
	task.done <- result
}

func (pool *thumbnailWorkerPool) rejectQueuedLocked(err error) {
	for _, task := range pool.queue {
		pool.finishTaskLocked(task, thumbnailWorkerResult{err: err})
	}
	pool.queue = nil
}

func (pool *thumbnailWorkerPool) hasRunnableWorkerLocked() bool {
	for _, slot := range pool.workers {
		if slot != nil && slot.process != nil && !slot.replacing {
			return true
		}
	}
	return false
}

func (pool *thumbnailWorkerPool) hasReplacementLocked() bool {
	for _, slot := range pool.workers {
		if slot != nil && slot.replacing {
			return true
		}
	}
	return false
}

func thumbnailWorkerPoolError(code, message string) error {
	return &Error{Code: code, Message: message}
}

func (process *thumbnailWorkerProcess) terminateAndWait() error {
	process.waitOnce.Do(func() {
		_ = process.input.Close()
		if process.command.Process != nil {
			_ = process.command.Process.Kill()
		}
		process.waitErr = process.command.Wait()
		_ = process.output.Close()
	})
	return process.waitErr
}

func (writer *thumbnailWorkerStderr) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(data) >= thumbnailWorkerStderrLimit {
		writer.data = append(writer.data[:0], data[len(data)-thumbnailWorkerStderrLimit:]...)
		return len(data), nil
	}
	if excess := len(writer.data) + len(data) - thumbnailWorkerStderrLimit; excess > 0 {
		copy(writer.data, writer.data[excess:])
		writer.data = writer.data[:len(writer.data)-excess]
	}
	writer.data = append(writer.data, data...)
	return len(data), nil
}
