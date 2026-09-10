//go:build darwin || linux

package fs

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestWorkspaceListsSpecialEntriesAsUnavailable(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "stream")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("create FIFO: %v", err)
	}
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	entries, err := workspace.List(mustWorkspacePath(t, ""))
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "stream" || entries[0].Kind != EntryKindUnavailable {
		t.Fatalf("entries = %#v, want unavailable FIFO", entries)
	}
}
