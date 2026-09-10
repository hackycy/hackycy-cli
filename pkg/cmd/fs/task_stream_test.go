package fs

import (
	"context"
	"testing"
	"time"
)

func TestDownloadTaskSubscriptionPublishesInitialUpdateAndClose(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	manager := NewDownloadManager(workspace)
	events, cancel := manager.Subscribe()
	defer cancel()
	if tasks := receiveTaskSnapshot(t, events); len(tasks) != 0 {
		t.Fatalf("initial tasks = %#v", tasks)
	}
	manager.mu.Lock()
	manager.tasks["download"] = &DownloadTask{ID: "download", Status: "running"}
	manager.notifyLocked()
	manager.mu.Unlock()
	if tasks := receiveTaskSnapshot(t, events); len(tasks) != 1 || tasks[0].Status != "running" {
		t.Fatalf("updated tasks = %#v", tasks)
	}
	manager.Close()
	select {
	case _, open := <-events:
		if open {
			t.Fatal("download event stream remained open after close")
		}
	case <-time.After(time.Second):
		t.Fatal("download event stream did not close")
	}
}

func TestExtractionTaskSubscriptionPublishesInitialUpdateAndClose(t *testing.T) {
	manager := newExtractionManager(func(context.Context, WorkspacePath, ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		<-time.After(time.Second)
		return ArchiveExtractionResult{}, nil
	})
	events, cancel := manager.Subscribe()
	defer cancel()
	if tasks := receiveTaskSnapshot(t, events); len(tasks) != 0 {
		t.Fatalf("initial tasks = %#v", tasks)
	}
	if _, err := manager.Enqueue([]string{"archive.zip"}); err != nil {
		t.Fatal(err)
	}
	if tasks := receiveTaskSnapshot(t, events); len(tasks) != 1 || tasks[0].Status != "running" {
		t.Fatalf("updated tasks = %#v", tasks)
	}
	manager.Close()
	select {
	case _, open := <-events:
		if open {
			t.Fatal("extraction event stream remained open after close")
		}
	case <-time.After(time.Second):
		t.Fatal("extraction event stream did not close")
	}
}

func receiveTaskSnapshot[T any](t *testing.T, events <-chan []T) []T {
	t.Helper()
	select {
	case snapshot := <-events:
		return snapshot
	case <-time.After(time.Second):
		t.Fatal("task snapshot was not published")
		var zero []T
		return zero
	}
}
