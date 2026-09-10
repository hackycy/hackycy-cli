package diff

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotTextDiffRendersBoundedUnifiedPatches(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "alpha\nbefore\nomega\n")
	writeComparisonFile(t, target, "changed.txt", "alpha\nafter\nomega\n")
	writeComparisonFile(t, target, "added.txt", "new\n")
	writeComparisonFile(t, baseline, "deleted.txt", "old\n")
	writeComparisonFile(t, baseline, "no-newline.txt", "old")
	writeComparisonFile(t, target, "no-newline.txt", "new")
	writeComparisonFile(t, baseline, "whitespace.txt", "alpha \n\tbeta\n")
	writeComparisonFile(t, target, "whitespace.txt", "alpha\t\n\tbeta\n")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	changed, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "changed.txt"), nil)
	if err != nil {
		t.Fatalf("changed TextDiff() error = %v", err)
	}
	if changed.Status != TextDiffReady || changed.ComparisonStatus != StatusModified || changed.ContextLines != 3 || changed.AddedLines != 1 || changed.DeletedLines != 1 || changed.Patch != "--- baseline\n+++ target\n@@ -1,3 +1,3 @@\n alpha\n-before\n+after\n omega\n" {
		t.Fatalf("changed TextDiff() = %#v", changed)
	}
	if changed.BaselineEncoding == nil || *changed.BaselineEncoding != EncodingUTF8 || changed.TargetEncoding == nil || *changed.TargetEncoding != EncodingUTF8 {
		t.Fatalf("changed encodings = %#v", changed)
	}
	twenty := 20
	if wideContext, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "changed.txt"), &TextDiffOptions{ContextLines: &twenty}); err != nil || wideContext.ContextLines != 20 || wideContext.Status != TextDiffReady {
		t.Fatalf("wide-context TextDiff() = %#v, error = %v", wideContext, err)
	}

	added, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "added.txt"), nil)
	if err != nil || added.Patch != "--- /dev/null\n+++ target\n@@ -0,0 +1,1 @@\n+new\n" || added.AddedLines != 1 || added.DeletedLines != 0 {
		t.Fatalf("added TextDiff() = %#v, error = %v", added, err)
	}
	deleted, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "deleted.txt"), nil)
	if err != nil || deleted.Patch != "--- baseline\n+++ /dev/null\n@@ -1,1 +0,0 @@\n-old\n" || deleted.AddedLines != 0 || deleted.DeletedLines != 1 {
		t.Fatalf("deleted TextDiff() = %#v, error = %v", deleted, err)
	}

	zero := 0
	noNewline, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "no-newline.txt"), &TextDiffOptions{ContextLines: &zero})
	if err != nil || noNewline.ContextLines != 0 || noNewline.Patch != "--- baseline\n+++ target\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n" {
		t.Fatalf("no-newline TextDiff() = %#v, error = %v", noNewline, err)
	}
	whitespace, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "whitespace.txt"), nil)
	if err != nil || whitespace.Patch != "--- baseline\n+++ target\n@@ -1,2 +1,2 @@\n-alpha \n+alpha\t\n \tbeta\n" {
		t.Fatalf("whitespace TextDiff() = %#v, error = %v", whitespace, err)
	}
}

func TestTextDiffAlgorithmStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := calculateTextDiff(ctx, []textDiffLine{{text: "before\n"}}, []textDiffLine{{text: "after\n"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("calculateTextDiff() error = %v", err)
	}
}

func TestSnapshotTextDiffClassifiesSourcesAndValidatesInputs(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "encoding.txt", "hello\n")
	writeComparisonBytes(t, target, "encoding.txt", []byte{0xff, 0xfe, 'h', 0, 'e', 0, 'l', 0, 'l', 0, 'o', 0, '\n', 0})
	writeComparisonBytes(t, baseline, "binary.bin", []byte{0xc3, 0x28})
	writeComparisonBytes(t, target, "binary.bin", []byte{0xc3, 0x29})
	writeComparisonFile(t, baseline, "kind-change", "text\n")
	if err := os.Symlink("missing-target", filepath.Join(target, "kind-change")); err != nil {
		t.Fatal(err)
	}
	writeComparisonFile(t, target, "guarded.txt", strings.Repeat("x\n", 50_000))
	writeComparisonFile(t, baseline, "unchanged.txt", "same\n")
	writeComparisonFile(t, target, "unchanged.txt", "same\n")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	encodingOnly, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "encoding.txt"), nil)
	if err != nil || encodingOnly.Status != TextDiffNoTextualChanges || encodingOnly.Reason != TextDiffEncodingOrBOMOnly || encodingOnly.BaselineEncoding == nil || *encodingOnly.BaselineEncoding != EncodingUTF8 || encodingOnly.TargetEncoding == nil || *encodingOnly.TargetEncoding != EncodingUTF16LE {
		t.Fatalf("encoding-only TextDiff() = %#v, error = %v", encodingOnly, err)
	}
	binary, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "binary.bin"), nil)
	if err != nil || binary.Status != TextDiffUnavailable || binary.Reason != TextDiffNonText {
		t.Fatalf("binary TextDiff() = %#v, error = %v", binary, err)
	}
	mixed, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "kind-change"), nil)
	if err != nil || mixed.Status != TextDiffUnavailable || mixed.Reason != TextDiffMixedEntryKinds {
		t.Fatalf("mixed TextDiff() = %#v, error = %v", mixed, err)
	}
	guarded, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "guarded.txt"), nil)
	if err != nil || guarded.Status != TextDiffUnavailable || guarded.Reason != TextDiffSourceTooLarge || guarded.TargetSize == nil || *guarded.TargetSize != 100_000 || guarded.TargetLineCount == nil || *guarded.TargetLineCount != 50_001 {
		t.Fatalf("guarded TextDiff() = %#v, error = %v", guarded, err)
	}

	if _, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "unchanged.txt"), nil); !errors.Is(err, errComparisonEntryNotFound) {
		t.Fatalf("unchanged TextDiff() error = %v", err)
	}
	if _, err := snapshot.TextDiff(context.Background(), 99_999, nil); !errors.Is(err, errComparisonEntryNotFound) {
		t.Fatalf("unknown TextDiff() error = %v", err)
	}
	invalidContext := 21
	if _, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "encoding.txt"), &TextDiffOptions{ContextLines: &invalidContext}); err == nil || err.Error() != "contextLines must be an integer between 0 and 20" {
		t.Fatalf("invalid-context TextDiff() error = %v", err)
	}
}

func TestSnapshotTextDiffBoundsOutputWorkAndConcurrentCalls(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "large-change.txt", strings.Repeat("x", 300*1024)+"\n")
	writeComparisonFile(t, target, "complex-change.txt", strings.Repeat("0123456789abcdef\n", 6_000))
	writeComparisonFile(t, target, "stale.txt", "before")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	large, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "large-change.txt"), nil)
	if err != nil || large.Status != TextDiffUnavailable || large.Reason != TextDiffOutputTooLarge || large.AddedLines != 1 || large.DeletedLines != 0 || large.OutputBytes <= 256*1024 {
		t.Fatalf("large TextDiff() = %#v, error = %v", large, err)
	}
	complex, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "complex-change.txt"), nil)
	if err != nil || complex.Status != TextDiffUnavailable || complex.Reason != TextDiffComplexityLimit {
		t.Fatalf("complex TextDiff() = %#v, error = %v", complex, err)
	}

	staleID := snapshotEntryID(t, snapshot, "stale.txt")
	if err := os.WriteFile(filepath.Join(target, "stale.txt"), []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err := snapshot.TextDiff(context.Background(), staleID, nil)
	if err != nil || stale.Status != TextDiffUnavailable || stale.Reason != TextDiffStale {
		t.Fatalf("stale TextDiff() = %#v, error = %v", stale, err)
	}

	snapshot.textDiffSlots <- struct{}{}
	snapshot.textDiffSlots <- struct{}{}
	busy, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "large-change.txt"), nil)
	if err != nil || busy.Status != TextDiffUnavailable || busy.Reason != TextDiffServerBusy {
		t.Fatalf("busy TextDiff() = %#v, error = %v", busy, err)
	}
	<-snapshot.textDiffSlots
	<-snapshot.textDiffSlots
}
