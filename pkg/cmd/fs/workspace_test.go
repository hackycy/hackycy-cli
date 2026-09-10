package fs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenWorkspaceResolvesTheBrowseRootOnce(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "resolved-root")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("create target root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("write root file: %v", err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("create root alias: %v", err)
	}

	workspace, err := OpenWorkspace(alias)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	if workspace.RootName() != "resolved-root" {
		t.Fatalf("RootName() = %q, want resolved root name", workspace.RootName())
	}

	contents := readWorkspaceFile(t, workspace, "inside.txt")
	if contents != "inside" {
		t.Fatalf("opened contents = %q, want %q", contents, "inside")
	}
}

func TestOpenWorkspaceRejectsMissingAndNonDirectoryRoots(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := OpenWorkspace(missing); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("OpenWorkspace(missing) error = %v, want ErrWorkspaceNotFound", err)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write non-directory root: %v", err)
	}
	if _, err := OpenWorkspace(file); !errors.Is(err, ErrWorkspaceNotDirectory) {
		t.Fatalf("OpenWorkspace(file) error = %v, want ErrWorkspaceNotDirectory", err)
	}
}

func TestWorkspaceFollowsInternalLinksAndRejectsEscapingLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows reparse-point coverage requires its own privileged fixture")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("write internal file: %v", err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(root, "internal-link")); err != nil {
		t.Fatalf("create internal link: %v", err)
	}
	external := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(external, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write external file: %v", err)
	}
	if err := os.Symlink(external, filepath.Join(root, "external-link")); err != nil {
		t.Fatalf("create external link: %v", err)
	}

	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	if contents := readWorkspaceFile(t, workspace, "internal-link"); contents != "inside" {
		t.Fatalf("internal link contents = %q, want %q", contents, "inside")
	}
	path := mustWorkspacePath(t, "external-link")
	if _, err := workspace.OpenFile(path); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatalf("OpenFile(external link) error = %v, want ErrWorkspaceUnavailable", err)
	}
}

func TestWorkspaceHandleRetainsItsOriginalRootAfterPathReplacement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "browse")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create original root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "original.txt"), []byte("original"), 0o600); err != nil {
		t.Fatalf("write original file: %v", err)
	}
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() { _ = workspace.Close() })

	moved := filepath.Join(parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatalf("move original root: %v", err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create replacement root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "replacement.txt"), []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement file: %v", err)
	}

	if contents := readWorkspaceFile(t, workspace, "original.txt"); contents != "original" {
		t.Fatalf("original root contents = %q, want %q", contents, "original")
	}
	path := mustWorkspacePath(t, "replacement.txt")
	if _, err := workspace.OpenFile(path); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatalf("OpenFile(replacement file) error = %v, want ErrWorkspaceUnavailable", err)
	}
}

func TestWorkspaceListsContainedEntriesAndHidesTemporaryNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows reparse-point coverage requires its own privileged fixture")
	}
	root := t.TempDir()
	for _, directory := range []string{"docs", "zebra"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}
	for name, contents := range map[string]string{
		"Alpha.txt": "upper",
		"zeta.txt":  "zeta",
		".download-550e8400-e29b-41d4-a716-446655440000.tmp":      "temporary",
		".extract-550e8400-e29b-41d4-a716-446655440000.tmp.outer": "temporary",
		".edit-550e8400-e29b-41d4-a716-446655440000.tmp":          "temporary",
		".upload-550e8400-e29b-41d4-a716-446655440000.tmp":        "temporary",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Symlink("docs", filepath.Join(root, "internal-docs")); err != nil {
		t.Fatalf("create internal link: %v", err)
	}
	if err := os.Symlink("missing", filepath.Join(root, "broken")); err != nil {
		t.Fatalf("create broken link: %v", err)
	}
	external := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	if err := os.Symlink(external, filepath.Join(root, "external")); err != nil {
		t.Fatalf("create escaping link: %v", err)
	}

	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	entries, err := workspace.List(mustWorkspacePath(t, ""))
	if err != nil {
		t.Fatalf("List(root) error = %v", err)
	}
	got := make([]struct {
		name      string
		kind      EntryKind
		isSymlink bool
	}, len(entries))
	for index, entry := range entries {
		got[index] = struct {
			name      string
			kind      EntryKind
			isSymlink bool
		}{name: entry.Name, kind: entry.Kind, isSymlink: entry.IsSymlink}
		if entry.Path.String() != entry.Name {
			t.Fatalf("entry path = %q, want %q", entry.Path.String(), entry.Name)
		}
	}
	want := []struct {
		name      string
		kind      EntryKind
		isSymlink bool
	}{
		{name: "docs", kind: EntryKindDirectory},
		{name: "internal-docs", kind: EntryKindDirectory, isSymlink: true},
		{name: "zebra", kind: EntryKindDirectory},
		{name: "Alpha.txt", kind: EntryKindFile},
		{name: "zeta.txt", kind: EntryKindFile},
		{name: "broken", kind: EntryKindUnavailable, isSymlink: true},
		{name: "external", kind: EntryKindUnavailable, isSymlink: true},
	}
	if len(got) != len(want) {
		t.Fatalf("entry count = %d, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("entry %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestSortWorkspaceEntriesOrdersKindsAndCaseTies(t *testing.T) {
	entries := []Entry{
		{Name: "external", Kind: EntryKindUnavailable},
		{Name: "alpha.txt", Kind: EntryKindFile},
		{Name: "zebra", Kind: EntryKindDirectory},
		{Name: "Alpha.txt", Kind: EntryKindFile},
		{Name: "docs", Kind: EntryKindDirectory},
		{Name: "broken", Kind: EntryKindUnavailable},
	}
	sortWorkspaceEntries(entries)
	got := make([]string, len(entries))
	for index, entry := range entries {
		got[index] = string(entry.Kind) + ":" + entry.Name
	}
	want := []string{
		"directory:docs",
		"directory:zebra",
		"file:Alpha.txt",
		"file:alpha.txt",
		"unavailable:broken",
		"unavailable:external",
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("sorted entries = %#v, want %#v", got, want)
		}
	}
}

func TestWorkspaceEntryNameValidationFailsClosedForInvalidUTF8(t *testing.T) {
	name := string([]byte{'b', 'a', 'd', 0xff})
	if err := validateWorkspaceEntryName(name); !errors.Is(err, ErrWorkspaceUnavailable) {
		t.Fatalf("validateWorkspaceEntryName() error = %v, want ErrWorkspaceUnavailable", err)
	}
}

func TestWorkspaceOpenedFileKeepsIdentityAndBytesAfterReplacement(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	filePath := filepath.Join(root, "report.txt")
	if err := os.WriteFile(filePath, []byte("original"), 0o600); err != nil {
		t.Fatalf("write original file: %v", err)
	}
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	opened, err := workspace.OpenFile(mustWorkspacePath(t, "report.txt"))
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	identity := opened.Identity()
	if identity.Name != "report.txt" || identity.Path.String() != "report.txt" || identity.Size != int64(len("original")) || identity.ModifiedAt.IsZero() {
		t.Fatalf("Identity() = %#v", identity)
	}

	candidate := filepath.Join(root, "candidate")
	if err := os.WriteFile(candidate, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement candidate: %v", err)
	}
	if err := workspace.root.Rename("candidate", "report.txt"); err != nil {
		t.Fatalf("replace file: %v", err)
	}
	contents, err := io.ReadAll(opened)
	if err != nil {
		t.Fatalf("read opened file: %v", err)
	}
	if string(contents) != "original" {
		t.Fatalf("opened bytes = %q, want original bytes", contents)
	}
	if _, err := workspace.OpenFile(mustWorkspacePath(t, "docs")); !errors.Is(err, ErrWorkspacePathNotFile) {
		t.Fatalf("OpenFile(directory) error = %v, want ErrWorkspacePathNotFile", err)
	}
}

func readWorkspaceFile(t *testing.T, workspace *Workspace, value string) string {
	t.Helper()
	file, err := workspace.OpenFile(mustWorkspacePath(t, value))
	if err != nil {
		t.Fatalf("Open(%q) error = %v", value, err)
	}
	t.Cleanup(func() { _ = file.Close() })
	contents, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read %q: %v", value, err)
	}
	return string(contents)
}

func mustWorkspacePath(t *testing.T, value string) WorkspacePath {
	t.Helper()
	path, err := ParseWorkspacePath(value)
	if err != nil {
		t.Fatalf("ParseWorkspacePath(%q) error = %v", value, err)
	}
	return path
}
