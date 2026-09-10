package diff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspacePublishesDirectionalStatusesForRegularFiles(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "deleted.txt", "baseline only")
	writeComparisonFile(t, baseline, "modified.txt", "before")
	writeComparisonFile(t, baseline, "unchanged.txt", "same bytes")
	writeComparisonFile(t, target, "added.txt", "target only")
	writeComparisonFile(t, target, "modified.txt", "after!")
	writeComparisonFile(t, target, "unchanged.txt", "same bytes")

	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	got := make([]struct {
		path   string
		status ComparisonStatus
	}, len(snapshot.entries))
	for index, entry := range snapshot.entries {
		got[index] = struct {
			path   string
			status ComparisonStatus
		}{path: entry.Path, status: entry.Status}
	}
	want := []struct {
		path   string
		status ComparisonStatus
	}{
		{path: "added.txt", status: StatusAdded},
		{path: "deleted.txt", status: StatusDeleted},
		{path: "modified.txt", status: StatusModified},
		{path: "unchanged.txt", status: StatusUnchanged},
	}
	if !equalEntryStatuses(got, want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
	if snapshot.Summary().Counts != (StatusCounts{Added: 1, Deleted: 1, Modified: 1, Unchanged: 1}) {
		t.Fatalf("counts = %#v", snapshot.Summary().Counts)
	}
}

func TestWorkspaceComparesStoredSymlinkTargetsWithoutFollowingLinks(t *testing.T) {
	baseline, target := comparisonRoots(t)
	if err := os.Symlink("missing-one", filepath.Join(baseline, "changed")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-two", filepath.Join(target, "changed")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop-b", filepath.Join(baseline, "loop-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop-a", filepath.Join(baseline, "loop-b")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop-b", filepath.Join(target, "loop-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop-a", filepath.Join(target, "loop-b")); err != nil {
		t.Fatal(err)
	}

	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	if len(snapshot.entries) != 3 {
		t.Fatalf("entry count = %d, want 3", len(snapshot.entries))
	}
	if got := snapshot.entries[0]; got.Path != "changed" || got.Status != StatusModified || got.Baseline.LinkTarget != "missing-one" || got.Target.LinkTarget != "missing-two" {
		t.Fatalf("changed entry = %#v", got)
	}
	for _, entry := range snapshot.entries[1:] {
		if entry.Status != StatusUnchanged || entry.Baseline == nil || entry.Target == nil || entry.Baseline.Kind != EntryKindSymlink || entry.Target.Kind != EntryKindSymlink {
			t.Fatalf("loop entry = %#v", entry)
		}
	}
}

func TestWorkspaceRejectsNonDirectoryAndEqualResolvedRoots(t *testing.T) {
	baseline, target := comparisonRoots(t)
	notDirectory := filepath.Join(target, "file.txt")
	writeComparisonFile(t, target, "file.txt", "not a directory")

	if _, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: notDirectory}); err == nil || err.Error() != "Target Directory must be a directory" {
		t.Fatalf("non-directory error = %v", err)
	}
	if _, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: baseline}); err == nil || err.Error() != "Baseline Directory and Target Directory must be different" {
		t.Fatalf("equal-root error = %v", err)
	}
}

func TestWorkspaceRetainsPublishedSnapshotWhenRootIdentityChanges(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "added.txt", "content")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	original := refreshWorkspace(t, workspace)

	retiredTarget := target + "-retired"
	if err := os.Rename(target, retiredTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	run, err := workspace.StartRefresh(context.Background())
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	if _, err := run.Wait(context.Background()); err == nil || err.Error() != "Target Directory changed after the Comparison Workspace was created" {
		t.Fatalf("changed-root refresh error = %v", err)
	}
	if snapshot := workspace.Snapshot(); snapshot == nil || snapshot.Summary().ID != original.Summary().ID {
		t.Fatalf("published snapshot = %#v, want original %#v", snapshot, original)
	}
}

func TestWorkspaceAppliesHardAndExplicitExclusionsBeforeTraversal(t *testing.T) {
	baseline, target := comparisonRoots(t)
	for _, root := range []string{baseline, target} {
		writeComparisonFile(t, root, ".git/config", "hidden")
		writeComparisonFile(t, root, "nested/.DS_Store", "hidden")
		writeComparisonFile(t, root, "nested/ignored.tmp", "hidden")
		writeComparisonFile(t, root, "visible.txt", "visible")
	}
	workspace, err := NewWorkspace(WorkspaceOptions{
		BaselineDirectory: baseline,
		TargetDirectory:   target,
		Exclusions:        []string{"**/*.tmp"},
	})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	if len(snapshot.entries) != 1 || snapshot.entries[0].Path != "visible.txt" || snapshot.entries[0].Status != StatusUnchanged {
		t.Fatalf("entries = %#v", snapshot.entries)
	}
}

func TestWorkspaceUsesTargetGitIgnoreRulesForBothRoots(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, ".gitignore", "baseline-only.txt\n")
	writeComparisonFile(t, target, ".gitignore", "*.tmp\nbaseline-only/\n")
	writeComparisonFile(t, baseline, "nested/.gitignore", "!keep.tmp\n")
	writeComparisonFile(t, target, "nested/.gitignore", "!keep.tmp\n")
	writeComparisonFile(t, baseline, "nested/drop.tmp", "hidden baseline")
	writeComparisonFile(t, target, "nested/drop.tmp", "hidden target")
	writeComparisonFile(t, baseline, "nested/keep.tmp", "before")
	writeComparisonFile(t, target, "nested/keep.tmp", "after!")
	writeComparisonFile(t, baseline, "baseline-only/secret.txt", "hidden baseline only")
	writeComparisonFile(t, baseline, "baseline-only.txt", "visible because only Target rules apply")

	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	got := make([]string, len(snapshot.entries))
	for index, entry := range snapshot.entries {
		got[index] = entry.Path
	}
	want := []string{".gitignore", "baseline-only.txt", "nested/.gitignore", "nested/keep.tmp"}
	if !equalStrings(got, want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
	if got := snapshot.entries[3].Status; got != StatusModified {
		t.Fatalf("nested keep status = %q, want modified", got)
	}
}

func TestWorkspaceNoGitIgnoreRetainsRulesAsComparedFiles(t *testing.T) {
	baseline, target := comparisonRoots(t)
	for _, root := range []string{baseline, target} {
		writeComparisonFile(t, root, ".gitignore", "ignored.txt\n")
		writeComparisonFile(t, root, "ignored.txt", "still compared")
	}
	workspace, err := NewWorkspace(WorkspaceOptions{
		BaselineDirectory: baseline,
		TargetDirectory:   target,
		NoGitIgnore:       true,
	})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	got := make([]string, len(snapshot.entries))
	for index, entry := range snapshot.entries {
		got[index] = entry.Path
	}
	if !equalStrings(got, []string{".gitignore", "ignored.txt"}) {
		t.Fatalf("entries = %#v", got)
	}
}

func TestWorkspaceRetriesChangedPairedFilesBeforePublishing(t *testing.T) {
	baseline, target := comparisonRoots(t)
	targetPath := filepath.Join(target, "changing.txt")
	writeComparisonFile(t, baseline, "changing.txt", "before")
	writeComparisonFile(t, target, "changing.txt", "before")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	changed := false
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if !changed && state.Phase == PhaseComparing {
			changed = true
			if err := os.WriteFile(targetPath, []byte("after!"), 0o644); err != nil {
				t.Errorf("replace target: %v", err)
			}
		}
	})
	defer unsubscribe()

	snapshot := refreshWorkspace(t, workspace)
	if !changed {
		t.Fatal("comparison phase was not observed")
	}
	entry := snapshot.entries[0]
	if entry.Status != StatusModified || entry.Baseline == nil || entry.Target == nil || entry.Baseline.Size != 6 || entry.Target.Size != 6 {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestWorkspacePublishesPairedDisappearanceAsIssue(t *testing.T) {
	baseline, target := comparisonRoots(t)
	targetPath := filepath.Join(target, "disappearing.txt")
	writeComparisonFile(t, baseline, "disappearing.txt", "same")
	writeComparisonFile(t, target, "disappearing.txt", "same")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	removed := false
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if !removed && state.Phase == PhaseComparing {
			removed = true
			if err := os.Remove(targetPath); err != nil {
				t.Errorf("remove target: %v", err)
			}
		}
	})
	defer unsubscribe()

	snapshot := refreshWorkspace(t, workspace)
	entry := snapshot.entries[0]
	if !removed || entry.Status != StatusIssue || !strings.Contains(entry.Message, "Comparison could not be completed") {
		t.Fatalf("entry = %#v, removed = %t", entry, removed)
	}
}

func TestWorkspaceRejectsOneSidedMutationWithoutReplacingSnapshot(t *testing.T) {
	baseline, target := comparisonRoots(t)
	targetPath := filepath.Join(target, "added.txt")
	writeComparisonFile(t, target, "added.txt", "before")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	original := refreshWorkspace(t, workspace)
	changed := false
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if !changed && state.Phase == PhaseComparing {
			changed = true
			if err := os.WriteFile(targetPath, []byte("changed-size"), 0o644); err != nil {
				t.Errorf("replace target: %v", err)
			}
		}
	})
	defer unsubscribe()
	run, err := workspace.StartRefresh(context.Background())
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	if _, err := run.Wait(context.Background()); err == nil || err.Error() != "Comparison Entry changed before snapshot publication" {
		t.Fatalf("refresh error = %v", err)
	}
	if snapshot := workspace.Snapshot(); snapshot == nil || snapshot.Summary().ID != original.Summary().ID {
		t.Fatalf("snapshot = %#v, want original %#v", snapshot, original)
	}
}

func TestWorkspaceCancellationRetainsPriorSnapshot(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "value.txt", "before")
	writeComparisonFile(t, target, "value.txt", "after!")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	original := refreshWorkspace(t, workspace)
	for index := 0; index < 128; index++ {
		writeComparisonFile(t, target, filepath.Join("many", fmt.Sprintf("%04d.txt", index)), "content")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := false
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if !canceled && state.Phase == PhaseComparing {
			canceled = true
			cancel()
		}
	})
	defer unsubscribe()
	run, err := workspace.StartRefresh(ctx)
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	if _, err := run.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh error = %v", err)
	}
	if !canceled {
		t.Fatal("comparison phase was not canceled")
	}
	if state := workspace.State(); state.Phase != PhaseCanceled || state.SnapshotID != original.Summary().ID {
		t.Fatalf("state = %#v", state)
	}
	if snapshot := workspace.Snapshot(); snapshot == nil || snapshot.Summary().ID != original.Summary().ID {
		t.Fatalf("snapshot = %#v, want original %#v", snapshot, original)
	}
}

func TestWorkspaceUsesBytesRatherThanTimestampsForFileStatus(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "equal.txt", "same bytes")
	writeComparisonFile(t, target, "equal.txt", "same bytes")
	writeComparisonFile(t, baseline, "different.txt", "same size A")
	writeComparisonFile(t, target, "different.txt", "same size B")
	timestamp := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(target, "equal.txt"), time.Now(), timestamp); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(baseline, "different.txt"), filepath.Join(target, "different.txt")} {
		if err := os.Chtimes(path, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	got := make([]struct {
		path   string
		status ComparisonStatus
	}, len(snapshot.entries))
	for index, entry := range snapshot.entries {
		got[index] = struct {
			path   string
			status ComparisonStatus
		}{path: entry.Path, status: entry.Status}
	}
	want := []struct {
		path   string
		status ComparisonStatus
	}{
		{path: "different.txt", status: StatusModified},
		{path: "equal.txt", status: StatusUnchanged},
	}
	if !equalEntryStatuses(got, want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
}

func TestWorkspaceAcceptsNestedComparisonRoots(t *testing.T) {
	root := t.TempDir()
	baseline := filepath.Join(root, "baseline")
	target := filepath.Join(baseline, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeComparisonFile(t, baseline, "base.txt", "base")
	writeComparisonFile(t, target, "target.txt", "target")

	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	got := make([]struct {
		path   string
		status ComparisonStatus
	}, len(snapshot.entries))
	for index, entry := range snapshot.entries {
		got[index] = struct {
			path   string
			status ComparisonStatus
		}{path: entry.Path, status: entry.Status}
	}
	want := []struct {
		path   string
		status ComparisonStatus
	}{
		{path: "base.txt", status: StatusDeleted},
		{path: "target.txt", status: StatusAdded},
		{path: "target/target.txt", status: StatusDeleted},
	}
	if !equalEntryStatuses(got, want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
}

func comparisonRoots(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	baseline := filepath.Join(root, "baseline")
	target := filepath.Join(root, "target")
	for _, directory := range []string{baseline, target} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return baseline, target
}

func writeComparisonFile(t *testing.T, root, relativePath, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func refreshWorkspace(t *testing.T, workspace *Workspace) *Snapshot {
	t.Helper()
	run, err := workspace.StartRefresh(context.Background())
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	snapshot, err := run.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	return snapshot
}

func equalEntryStatuses(left, right []struct {
	path   string
	status ComparisonStatus
}) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
