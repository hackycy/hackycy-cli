package fsthumbnail

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

const thumbnailPoolWorkerEnvironment = "YCY_TEST_THUMBNAIL_POOL_WORKER"

func TestThumbnailWorkerPoolHelperProcess(t *testing.T) {
	if os.Getenv(thumbnailPoolWorkerEnvironment) != "1" {
		return
	}
	for {
		request, err := readThumbnailWorkerRequest(os.Stdin)
		if err == io.EOF {
			os.Exit(0)
		}
		if err != nil {
			os.Exit(2)
		}
		switch request.mimeType {
		case "application/x-thumbnail-test-stall":
			<-time.After(time.Hour)
		case "application/x-thumbnail-test-crash":
			os.Exit(2)
		case "application/x-thumbnail-test-pid":
			if err := writeThumbnailWorkerResponse(os.Stdout, thumbnailWorkerResponse{id: request.id, ok: true, payload: []byte(strconv.Itoa(os.Getpid()))}); err != nil {
				os.Exit(2)
			}
		default:
			thumbnail, convertErr := convertThumbnail(request.mimeType, request.source)
			response := thumbnailWorkerResponse{id: request.id, ok: convertErr == nil, payload: thumbnail}
			if convertErr != nil {
				response.payload = []byte(convertErr.Error())
			}
			if err := writeThumbnailWorkerResponse(os.Stdout, response); err != nil {
				os.Exit(2)
			}
		}
	}
}

func TestThumbnailWorkerPoolClosesBeforeAnyWorkerStarts(t *testing.T) {
	pool := newThumbnailTestPool(t, thumbnailWorkerPoolOptions{})
	pool.close()
	pool.close()
}

func TestThumbnailWorkerPoolStartsTwoPersistentWorkers(t *testing.T) {
	pool := newThumbnailTestPool(t, thumbnailWorkerPoolOptions{})
	first, err := pool.convert("application/x-thumbnail-test-pid", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.convert("application/x-thumbnail-test-pid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("persistent worker PIDs = %q, %q", first, second)
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.workers) != thumbnailWorkerCount || pool.workers[0].process == nil || pool.workers[1].process == nil {
		t.Fatalf("workers = %#v", pool.workers)
	}
}

func TestThumbnailWorkerPoolTimeoutKillsWaitsAndReplaces(t *testing.T) {
	pool := newThumbnailTestPool(t, thumbnailWorkerPoolOptions{workerCount: 1, timeout: 40 * time.Millisecond})
	before, err := pool.convert("application/x-thumbnail-test-pid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.convert("application/x-thumbnail-test-stall", nil); !thumbnailErrorIs(err, "THUMBNAIL_TIMEOUT") {
		var thumbnail *Error
		if errors.As(err, &thumbnail) {
			t.Fatalf("stall error = %v (%v)", err, thumbnail.Cause)
		}
		t.Fatalf("stall error = %v", err)
	}
	after, err := pool.convert("application/x-thumbnail-test-pid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatalf("replacement PID = %q, want a process different from %q", after, before)
	}
}

func TestThumbnailWorkerPoolReplacesCrashedWorker(t *testing.T) {
	pool := newThumbnailTestPool(t, thumbnailWorkerPoolOptions{workerCount: 1})
	before, err := pool.convert("application/x-thumbnail-test-pid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.convert("application/x-thumbnail-test-crash", nil); !thumbnailErrorIs(err, "THUMBNAIL_WORKER_FAILED") {
		t.Fatalf("crash error = %v", err)
	}
	after, err := pool.convert("application/x-thumbnail-test-pid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatalf("replacement PID = %q, want a process different from %q", after, before)
	}
}

func TestThumbnailWorkerPoolBoundsQueueAndClosesWorkers(t *testing.T) {
	pool := newThumbnailTestPool(t, thumbnailWorkerPoolOptions{workerCount: 1, timeout: time.Minute})
	results := make(chan error, thumbnailWorkerQueue+1)
	go func() {
		_, err := pool.convert("application/x-thumbnail-test-stall", nil)
		results <- err
	}()
	waitForThumbnailPool(t, pool, func() bool { return pool.workers[0] != nil && pool.workers[0].task != nil })
	for range thumbnailWorkerQueue {
		go func() {
			_, err := pool.convert("application/x-thumbnail-test-pid", nil)
			results <- err
		}()
	}
	waitForThumbnailPool(t, pool, func() bool { return len(pool.queue) == thumbnailWorkerQueue })
	if _, err := pool.convert("application/x-thumbnail-test-pid", nil); !thumbnailErrorIs(err, "THUMBNAIL_QUEUE_FULL") {
		t.Fatalf("full queue error = %v", err)
	}
	pool.close()
	for range thumbnailWorkerQueue + 1 {
		if err := <-results; !thumbnailErrorIs(err, "THUMBNAIL_STOPPED") {
			t.Fatalf("close error = %v", err)
		}
	}
}

func TestThumbnailWorkerPoolRejectsQueueWhenEveryWorkerFails(t *testing.T) {
	pool, err := newThumbnailWorkerPool(thumbnailWorkerPoolOptions{
		workerCount: 2,
		launch: func() (*thumbnailWorkerProcess, error) {
			return nil, errors.New("unavailable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.close()
	if _, err := pool.convert("application/x-thumbnail-test-pid", nil); !thumbnailErrorIs(err, "THUMBNAIL_WORKER_FAILED") {
		t.Fatalf("all-worker failure = %v", err)
	}
}

func newThumbnailTestPool(t *testing.T, options thumbnailWorkerPoolOptions) *thumbnailWorkerPool {
	t.Helper()
	options.launch = func() (*thumbnailWorkerProcess, error) {
		command := exec.Command(os.Args[0], "-test.run=^TestThumbnailWorkerPoolHelperProcess$")
		command.Env = append(os.Environ(), thumbnailPoolWorkerEnvironment+"=1")
		return startThumbnailWorkerProcess(command)
	}
	pool, err := newThumbnailWorkerPool(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.close)
	return pool
}

func waitForThumbnailPool(t *testing.T, pool *thumbnailWorkerPool, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ready := func() bool {
			pool.mu.Lock()
			defer pool.mu.Unlock()
			return predicate()
		}()
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("thumbnail worker pool did not reach the expected state")
}
