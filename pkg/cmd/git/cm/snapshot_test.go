package cm

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestCaptureSnapshotCapturesStagedWorktreeAndUntrackedScopes(t *testing.T) {
	root := t.TempDir()
	writeSnapshotFile(t, root, "new.txt", "first\nsecond\n")
	writeSnapshotFile(t, root, ".env", "API_KEY=never-send-this\n")
	runner := newSnapshotRunner(root, "M  src/staged.go\x00 M src/worktree.go\x00?? new.txt\x00?? .env\x00")
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--no-ext-diff", "--find-renames", "--unified=0"})] = GitOutput{Stdout: []byte("diff --git a/src/staged.go b/src/staged.go\n--- a/src/staged.go\n+++ b/src/staged.go\n@@ -1 +1 @@ func staged()\n-old\n+new\n")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--numstat", "-z", "--find-renames"})] = GitOutput{Stdout: []byte("1\t1\tsrc/staged.go\x00")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--no-ext-diff", "--find-renames", "--unified=0"})] = GitOutput{Stdout: []byte("diff --git a/src/worktree.go b/src/worktree.go\n--- a/src/worktree.go\n+++ b/src/worktree.go\n@@ -2,1 +2,2 @@ func worktree()\n-old\n+new\n+next\n")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--numstat", "-z", "--find-renames"})] = GitOutput{Stdout: []byte("2\t1\tsrc/worktree.go\x00")}

	snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeAllUncommitted)
	if err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}
	if snapshot.RepositoryRoot != root || snapshot.Scope != ScopeAllUncommitted || snapshot.ID == "" || snapshot.Totals != (ChangeStats{Additions: 5, Deletions: 2}) {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if got, want := snapshotPaths(snapshot.Files), []string{".env", "new.txt", "src/staged.go", "src/worktree.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
	newFile := snapshotFileByPath(t, snapshot, "new.txt")
	if newFile.Role != FileRoleUnknown || newFile.ContentPolicy != ContentInspect || newFile.Stats != (ChangeStats{Additions: 2}) || len(newFile.Hunks) != 1 || newFile.Hunks[0].Source != "untracked" {
		t.Fatalf("new file = %#v", newFile)
	}
	sensitive := snapshotFileByPath(t, snapshot, ".env")
	if sensitive.Role != FileRoleSensitive || sensitive.ContentPolicy != ContentRedacted || len(sensitive.Hunks) != 0 {
		t.Fatalf("sensitive file = %#v", sensitive)
	}
	if strings.Contains(CompileEvidence(snapshot, evidenceSystem).Text, "never-send-this") {
		t.Fatal("compiled evidence exposes redacted untracked content")
	}
	staged := snapshotFileByPath(t, snapshot, "src/staged.go")
	if staged.Stats != (ChangeStats{Additions: 1, Deletions: 1}) || len(staged.Hunks) != 1 || staged.Hunks[0].Heading != "func staged()" {
		t.Fatalf("staged file = %#v", staged)
	}
}

func TestCaptureSnapshotUsesOnlyCachedGitReadsForStagedScope(t *testing.T) {
	root := t.TempDir()
	runner := newSnapshotRunner(root, "MM mixed.go\x00 M worktree.go\x00?? untracked.go\x00")
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--no-ext-diff", "--find-renames", "--unified=0"})] = GitOutput{Stdout: []byte("diff --git a/mixed.go b/mixed.go\n--- a/mixed.go\n+++ b/mixed.go\n@@ -1 +1 @@\n-old\n+new\n")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--numstat", "-z", "--find-renames"})] = GitOutput{Stdout: []byte("1\t1\tmixed.go\x00")}

	snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeStaged)
	if err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Path != "mixed.go" || snapshot.Files[0].WorktreeStatus != ' ' || snapshot.Files[0].Status != "M mixed.go" {
		t.Fatalf("snapshot files = %#v", snapshot.Files)
	}
	runner.requireNoCall(t, []string{"-C", root, "diff", "--no-ext-diff", "--find-renames", "--unified=0"})
	runner.requireNoCall(t, []string{"-C", root, "diff", "--numstat", "-z", "--find-renames"})
}

func TestCaptureSnapshotReadsPackageManifestFromGitAndWorktree(t *testing.T) {
	root := t.TempDir()
	after := `{"version":"2.0.0","dependencies":{"new":"1.0.0"}}`
	writeSnapshotFile(t, root, "package.json", after)
	runner := newSnapshotRunner(root, " M package.json\x00")
	setEmptySnapshotDiffs(runner, root)
	before := `{"version":"1.0.0"}`
	input := []byte("HEAD:package.json\n")
	runner.inputResponses[snapshotInputKey([]string{"-C", root, "cat-file", "--batch"}, input)] = GitOutput{Stdout: catFileBlob(before)}

	snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeAllUncommitted)
	if err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}
	manifest := snapshotFileByPath(t, snapshot, "package.json").Manifest
	if manifest == nil || manifest.Before == nil || manifest.After == nil || *manifest.Before != before || *manifest.After != after {
		t.Fatalf("manifest = %#v", manifest)
	}
	runner.requireInput(t, []string{"-C", root, "cat-file", "--batch"}, input)
}

func TestCaptureSnapshotFollowsUntrackedSymlinkAndDetectsStaleScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires host permissions")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside-first\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	runner := newSnapshotRunner(root, "?? linked.txt\x00")
	setEmptySnapshotDiffs(runner, root)

	snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeAllUncommitted)
	if err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}
	linked := snapshotFileByPath(t, snapshot, "linked.txt")
	if linked.ContentPolicy != ContentInspect || len(linked.Hunks) != 1 || !strings.Contains(strings.Join(linked.Hunks[0].AddedLines, "\n"), "outside-first") {
		t.Fatalf("linked snapshot = %#v", linked)
	}
	if err := os.WriteFile(outside, []byte("outside-second\n"), 0o600); err != nil {
		t.Fatalf("rewrite outside file: %v", err)
	}
	err = AssertSnapshotCurrent(context.Background(), runner, diskSnapshotFileSystem{}, ScopeAllUncommitted, snapshot.ID)
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Code != ErrorStaleScope {
		t.Fatalf("AssertSnapshotCurrent() error = %#v", err)
	}
}

func TestParseNumstatPreservesNULDelimitedRenamePaths(t *testing.T) {
	stats := parseNumstat([]byte("2\t1\t\x00old\tname\x00new\tname\x00-\t-\tbinary.png\x00"))
	if got := stats["new\tname"]; got != (numstat{Additions: 2, Deletions: 1}) {
		t.Fatalf("rename stat = %#v", got)
	}
	if got := stats["binary.png"]; got != (numstat{Binary: true}) {
		t.Fatalf("binary stat = %#v", got)
	}
}

type diskSnapshotFileSystem struct{}

func (diskSnapshotFileSystem) Lstat(filePath string) (fs.FileInfo, error) {
	return os.Lstat(filePath)
}

func (diskSnapshotFileSystem) Open(filePath string) (io.ReadCloser, error) {
	return os.Open(filePath)
}

func (diskSnapshotFileSystem) ReadFile(filePath string) ([]byte, error) {
	return os.ReadFile(filePath)
}

type snapshotRunner struct {
	mu             sync.Mutex
	responses      map[string]GitOutput
	errors         map[string]error
	inputResponses map[string]GitOutput
	inputErrors    map[string]error
	calls          [][]string
	inputs         []snapshotInputCall
}

type snapshotInputCall struct {
	arguments []string
	input     []byte
}

func newSnapshotRunner(root, status string) *snapshotRunner {
	return &snapshotRunner{responses: map[string]GitOutput{
		snapshotRunnerKey([]string{"rev-parse", "--show-toplevel"}):                                        {Stdout: []byte(root + "\n")},
		snapshotRunnerKey([]string{"-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all"}): {Stdout: []byte(status)},
		snapshotRunnerKey([]string{"-C", root, "ls-files", "--stage", "-z"}):                               {},
	}, errors: map[string]error{}, inputResponses: map[string]GitOutput{}, inputErrors: map[string]error{}}
}

func (runner *snapshotRunner) Run(_ context.Context, arguments []string) (GitOutput, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	copyOfArguments := append([]string(nil), arguments...)
	runner.calls = append(runner.calls, copyOfArguments)
	key := snapshotRunnerKey(arguments)
	if err := runner.errors[key]; err != nil {
		return GitOutput{}, err
	}
	return runner.responses[key], nil
}

func (runner *snapshotRunner) RunInput(_ context.Context, arguments []string, input []byte) (GitOutput, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	copyOfArguments := append([]string(nil), arguments...)
	copyOfInput := append([]byte(nil), input...)
	runner.inputs = append(runner.inputs, snapshotInputCall{arguments: copyOfArguments, input: copyOfInput})
	key := snapshotInputKey(arguments, input)
	if err := runner.inputErrors[key]; err != nil {
		return GitOutput{}, err
	}
	return runner.inputResponses[key], nil
}

func (runner *snapshotRunner) requireNoCall(t *testing.T, arguments []string) {
	t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, call := range runner.calls {
		if reflect.DeepEqual(call, arguments) {
			t.Fatalf("unexpected Git call %#v", arguments)
		}
	}
}

func (runner *snapshotRunner) requireInput(t *testing.T, arguments []string, input []byte) {
	t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, call := range runner.inputs {
		if reflect.DeepEqual(call.arguments, arguments) && reflect.DeepEqual(call.input, input) {
			return
		}
	}
	t.Fatalf("Git input calls = %#v, missing arguments %#v input %q", runner.inputs, arguments, input)
}

func snapshotRunnerKey(arguments []string) string {
	return strings.Join(arguments, "\x1f")
}

func snapshotInputKey(arguments []string, input []byte) string {
	return snapshotRunnerKey(arguments) + "\x1e" + string(input)
}

func setEmptySnapshotDiffs(runner *snapshotRunner, root string) {
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--no-ext-diff", "--find-renames", "--unified=0"})] = GitOutput{}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--numstat", "-z", "--find-renames"})] = GitOutput{}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--no-ext-diff", "--find-renames", "--unified=0"})] = GitOutput{}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--numstat", "-z", "--find-renames"})] = GitOutput{}
}

func catFileBlob(content string) []byte {
	return []byte("deadbeef blob " + strconv.Itoa(len(content)) + "\n" + content + "\n")
}

func writeSnapshotFile(t *testing.T, root, relativePath, content string) {
	t.Helper()
	filePath := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", relativePath, err)
	}
}

func snapshotFileByPath(t *testing.T, snapshot GitSnapshot, filePath string) SnapshotFile {
	t.Helper()
	for _, file := range snapshot.Files {
		if file.Path == filePath {
			return file
		}
	}
	t.Fatalf("snapshot does not contain %q: %#v", filePath, snapshot.Files)
	return SnapshotFile{}
}

func snapshotPaths(files []SnapshotFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}
