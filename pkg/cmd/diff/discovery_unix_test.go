//go:build darwin || linux

package diff

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWorkspacePublishesUnsupportedFilesystemKindAsIssue(t *testing.T) {
	baseline, target := comparisonRoots(t)
	fifo := filepath.Join(target, "service.pipe")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	if len(snapshot.entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(snapshot.entries))
	}
	issue := snapshot.entries[0]
	if issue.Path != "service.pipe" || issue.Status != StatusIssue || !strings.Contains(issue.Message, "unsupported filesystem kind") || snapshot.Summary().Issues != 1 {
		t.Fatalf("issue = %#v, summary = %#v", issue, snapshot.Summary())
	}
}
