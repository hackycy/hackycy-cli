package fs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExtractionManagerValidatesPathsAndRunsOneTaskAtATime(t *testing.T) {
	for _, paths := range [][]string{nil, {}, {""}, {strings.Repeat("a", maxExtractionPath+1)}} {
		if _, err := newExtractionManager(nil).Enqueue(paths); !serviceErrorIs(err, "INVALID_EXTRACTION") {
			t.Fatalf("Enqueue(%q) error = %v", paths, err)
		}
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	manager := newExtractionManager(func(ctx context.Context, path WorkspacePath, options ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		started <- path.String()
		options.OnInspect(ArchiveInspection{UncompressedBytes: 3, EntryCount: 1})
		options.Progress(42)
		select {
		case <-release:
			return ArchiveExtractionResult{Inspection: ArchiveInspection{UncompressedBytes: 3, EntryCount: 1}, Destination: mustWorkspacePath(t, path.String()+".out")}, nil
		case <-ctx.Done():
			return ArchiveExtractionResult{}, ctx.Err()
		}
	})
	created, err := manager.Enqueue([]string{"first.zip", "second.zip"})
	if err != nil || len(created) != 2 || created[0].Status != "queued" || created[1].Status != "queued" {
		t.Fatalf("Enqueue() = %#v, %v", created, err)
	}
	if got := <-started; got != "first.zip" {
		t.Fatalf("first started task = %q", got)
	}
	tasks := waitExtractionProgress(t, manager, created[0].ID)
	if tasks[0].ArchivePath != "second.zip" || tasks[0].Status != "queued" || tasks[1].Status != "running" || tasks[1].Progress == nil || *tasks[1].Progress != 42 || tasks[1].UncompressedBytes == nil || *tasks[1].UncompressedBytes != 3 {
		t.Fatalf("tasks while active = %#v", tasks)
	}
	close(release)
	if got := <-started; got != "second.zip" {
		t.Fatalf("second started task = %q", got)
	}
	if task := waitExtraction(t, manager, created[0].ID); task.Status != "done" || task.DestinationPath != "first.zip.out" || task.Progress == nil || *task.Progress != 100 {
		t.Fatalf("first task = %#v", task)
	}
	if task := waitExtraction(t, manager, created[1].ID); task.Status != "done" || task.DestinationPath != "second.zip.out" {
		t.Fatalf("second task = %#v", task)
	}
}

func waitExtractionProgress(t *testing.T, manager *ExtractionManager, id string) []ExtractionTask {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tasks := manager.List()
		for _, task := range tasks {
			if task.ID == id && task.Progress != nil {
				return tasks
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for extraction progress: %s", fmt.Sprint(manager.List()))
	return nil
}

func TestExtractionManagerCancelsRetriesPrunesAndCloses(t *testing.T) {
	started := make(chan string, 3)
	manager := newExtractionManager(func(ctx context.Context, path WorkspacePath, options ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		started <- path.String()
		if path.String() == "broken.zip" {
			return ArchiveExtractionResult{}, errors.New("broken archive")
		}
		<-ctx.Done()
		return ArchiveExtractionResult{}, ctx.Err()
	})
	created, err := manager.Enqueue([]string{"active.zip", "queued.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "active.zip" {
		t.Fatalf("active task = %q", got)
	}
	if cancelled, err := manager.Cancel(created[1].ID); err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("Cancel(queued) = %#v, %v", cancelled, err)
	}
	if cancelled, err := manager.Cancel(created[0].ID); err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("Cancel(active) = %#v, %v", cancelled, err)
	}
	if task := waitExtraction(t, manager, created[0].ID); task.Status != "cancelled" {
		t.Fatalf("active task = %#v", task)
	}
	broken, err := manager.Enqueue([]string{"broken.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-started; got != "broken.zip" {
		t.Fatalf("broken task = %q", got)
	}
	if task := waitExtraction(t, manager, broken[0].ID); task.Status != "error" || task.Error != "broken archive" {
		t.Fatalf("broken task = %#v", task)
	}
	retried, err := manager.Retry(broken[0].ID)
	if err != nil || retried.ID == broken[0].ID {
		t.Fatalf("Retry() = %#v, %v", retried, err)
	}
	if got := <-started; got != "broken.zip" {
		t.Fatalf("retried task = %q", got)
	}
	if task := waitExtraction(t, manager, retried.ID); task.Status != "error" {
		t.Fatalf("retried task = %#v", task)
	}
	manager.ClearTerminal()
	if len(manager.List()) != 0 {
		t.Fatalf("ClearTerminal() left %#v", manager.List())
	}
	manager.Close()
	if _, err := manager.Enqueue([]string{"later.zip"}); !serviceErrorIs(err, "EXTRACTION_SERVICE_STOPPED") {
		t.Fatalf("Enqueue after Close() error = %v", err)
	}
}

func waitExtraction(t *testing.T, manager *ExtractionManager, id string) ExtractionTask {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, task := range manager.List() {
			if task.ID == id && terminalExtractionStatus(task.Status) {
				return task
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for extraction task %s: %s", id, fmt.Sprint(manager.List()))
	return ExtractionTask{}
}
