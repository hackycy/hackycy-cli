package diff

import (
	"strings"
	"testing"
)

func TestSnapshotListFiltersAndPaginatesSortedEntries(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "bravo.txt", "before")
	writeComparisonFile(t, target, "bravo.txt", "after!")
	writeComparisonFile(t, target, "alpha.txt", "added")
	writeComparisonFile(t, baseline, "charlie.txt", "same")
	writeComparisonFile(t, target, "charlie.txt", "same")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	first, err := snapshot.List(EntryQuery{Limit: 1})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got := entryPaths(first.Entries); !equalStrings(got, []string{"alpha.txt"}) || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	second, err := snapshot.List(EntryQuery{Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("List() second page error = %v", err)
	}
	if got := entryPaths(second.Entries); !equalStrings(got, []string{"bravo.txt"}) || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}
	all, err := snapshot.List(EntryQuery{IncludeUnchanged: true})
	if err != nil {
		t.Fatalf("List() all error = %v", err)
	}
	if got := entryPaths(all.Entries); !equalStrings(got, []string{"alpha.txt", "bravo.txt", "charlie.txt"}) {
		t.Fatalf("all paths = %#v", got)
	}
	filtered, err := snapshot.List(EntryQuery{Statuses: []ComparisonStatus{StatusModified}, Path: "BRAVO", Kinds: []EntryItemKind{ItemKindFile}})
	if err != nil {
		t.Fatalf("List() filtered error = %v", err)
	}
	if got := entryPaths(filtered.Entries); !equalStrings(got, []string{"bravo.txt"}) {
		t.Fatalf("filtered paths = %#v", got)
	}
}

func TestSnapshotListUsesAnchorAndPreservesCursorCompatibility(t *testing.T) {
	baseline, target := comparisonRoots(t)
	for _, name := range []string{"alpha.txt", "bravo.txt", "charlie.txt"} {
		writeComparisonFile(t, target, name, "added")
	}
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	anchored, err := snapshot.List(EntryQuery{Limit: 2, Anchor: 3})
	if err != nil {
		t.Fatalf("List() anchor error = %v", err)
	}
	if got := entryPaths(anchored.Entries); !equalStrings(got, []string{"charlie.txt"}) {
		t.Fatalf("anchor page paths = %#v", got)
	}
	if _, err := snapshot.List(EntryQuery{Anchor: 99}); err == nil || err.Error() != "Entry anchor does not match the current filters" {
		t.Fatalf("invalid anchor error = %v", err)
	}
	if _, err := snapshot.List(EntryQuery{Cursor: "not-a-cursor"}); err == nil || err.Error() != "Invalid entry cursor" {
		t.Fatalf("invalid cursor error = %v", err)
	}
	largeCursor := encodeEntryCursor("999999999999999999999999999999")
	empty, err := snapshot.List(EntryQuery{Cursor: largeCursor})
	if err != nil || len(empty.Entries) != 0 {
		t.Fatalf("large cursor page = %#v, error = %v", empty, err)
	}
	first, err := snapshot.List(EntryQuery{Limit: 1})
	if err != nil {
		t.Fatalf("List() first page error = %v", err)
	}
	changedFilter, err := snapshot.List(EntryQuery{Cursor: first.NextCursor, Path: "charlie"})
	if err != nil {
		t.Fatalf("List() filter-reused cursor error = %v", err)
	}
	if got := entryPaths(changedFilter.Entries); !equalStrings(got, []string{"charlie.txt"}) {
		t.Fatalf("filter-reused cursor paths = %#v", got)
	}
}

func TestSnapshotLookupRejectsReplacedIDAndReturnsCopies(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "added.txt", "content")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	if workspace.Snapshot(snapshot.Summary().ID) != snapshot || workspace.Snapshot("unknown") != nil {
		t.Fatalf("snapshot ID lookup did not preserve current identity")
	}
	page, err := snapshot.List(EntryQuery{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	page.Entries[0].Path = "tampered.txt"
	page.Entries[0].Target.Size = 999
	again, err := snapshot.List(EntryQuery{})
	if err != nil {
		t.Fatalf("List() second error = %v", err)
	}
	if again.Entries[0].Path != "added.txt" || again.Entries[0].Target.Size != int64(len("content")) {
		t.Fatalf("snapshot leaked mutable entry: %#v", again.Entries[0])
	}
	summary := snapshot.Summary()
	summary.Counts.Added = 999
	if snapshot.Summary().Counts.Added != 1 {
		t.Fatalf("snapshot leaked mutable summary: %#v", snapshot.Summary())
	}
	if strings.TrimSpace(snapshot.Summary().ID) == "" {
		t.Fatal("snapshot ID is empty")
	}
}

func TestSnapshotTreeAndSearchUseSnapshotLocalDirectoryIndexes(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "src/added.ts", "added")
	writeComparisonFile(t, baseline, "src/lib/changed.ts", "before")
	writeComparisonFile(t, target, "src/lib/changed.ts", "after!")
	writeComparisonFile(t, baseline, "README.md", "same")
	writeComparisonFile(t, target, "README.md", "same")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	root := snapshot.Tree("")
	if len(root.Children) != 2 {
		t.Fatalf("root children = %#v", root.Children)
	}
	if directory := root.Children[0]; directory.Kind != TreeKindDirectory || directory.Path != "src" || directory.Counts != (StatusCounts{Added: 1, Modified: 1}) || directory.Issues != 0 {
		t.Fatalf("root directory = %#v", directory)
	}
	if entry := root.Children[1]; entry.Kind != TreeKindFile || entry.Path != "README.md" || entry.Status != StatusUnchanged {
		t.Fatalf("root entry = %#v", entry)
	}
	src := snapshot.Tree("src")
	if len(src.Children) != 2 || src.Children[0].Kind != TreeKindDirectory || src.Children[0].Path != "src/lib" || src.Children[1].Kind != TreeKindFile || src.Children[1].Path != "src/added.ts" {
		t.Fatalf("src children = %#v", src.Children)
	}
	if unknown := snapshot.Tree("missing"); len(unknown.Children) != 0 {
		t.Fatalf("unknown tree = %#v", unknown)
	}

	search := snapshot.Search("SRC", nil, 200)
	if got := treePaths(search.Results); !equalStrings(got, []string{"src", "src/added.ts", "src/lib", "src/lib/changed.ts"}) || search.Truncated {
		t.Fatalf("search = %#v", search)
	}
	modifiedOnly := snapshot.Search("", []ComparisonStatus{StatusModified}, 200)
	if got := treePaths(modifiedOnly.Results); !equalStrings(got, []string{"src", "src/lib", "src/lib/changed.ts"}) {
		t.Fatalf("modified search = %#v", modifiedOnly)
	}
	emptyStatuses := snapshot.Search("", []ComparisonStatus{}, 200)
	if len(emptyStatuses.Results) != 0 {
		t.Fatalf("empty-status search = %#v", emptyStatuses)
	}

	root.Children[0].Counts.Added = 999
	if snapshot.Tree("").Children[0].Counts.Added != 1 {
		t.Fatal("tree response mutated the snapshot index")
	}
}

func TestSnapshotUsesJavaScriptUTF16PathOrdering(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "\ue000.txt", "private use")
	writeComparisonFile(t, target, "\U0001f600.txt", "emoji")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	page, err := refreshWorkspace(t, workspace).List(EntryQuery{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got := entryPaths(page.Entries); !equalStrings(got, []string{"\U0001f600.txt", "\ue000.txt"}) {
		t.Fatalf("ordered paths = %#v", got)
	}
}

func entryPaths(entries []Entry) []string {
	paths := make([]string, len(entries))
	for index, entry := range entries {
		paths[index] = entry.Path
	}
	return paths
}

func treePaths(nodes []TreeNode) []string {
	paths := make([]string, len(nodes))
	for index, node := range nodes {
		paths[index] = node.Path
	}
	return paths
}
