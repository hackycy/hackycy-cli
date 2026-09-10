package heat

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestDiscoverRepositoryUsesGitRootCommand(t *testing.T) {
	runner := &scriptedGitRunner{outputs: []GitOutput{{Stdout: []byte(" /tmp/repository\n")}}}
	root, err := DiscoverRepository(context.Background(), runner)
	if err != nil {
		t.Fatalf("DiscoverRepository() error = %v", err)
	}
	if root != "/tmp/repository" {
		t.Fatalf("root = %q", root)
	}
	want := [][]string{{"rev-parse", "--show-toplevel"}}
	if !reflect.DeepEqual(runner.arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", runner.arguments, want)
	}
}

func TestDiscoverRepositoryPreservesRunnerAndGitFailures(t *testing.T) {
	runnerFailure := errors.New("git executable not found")
	testCases := []struct {
		name   string
		runner scriptedGitRunner
		want   error
		text   string
	}{
		{name: "runner", runner: scriptedGitRunner{err: runnerFailure}, want: runnerFailure},
		{name: "stderr", runner: scriptedGitRunner{outputs: []GitOutput{{ExitCode: 1, Stderr: []byte("fatal: not a git repository\n")}}}, text: "fatal: not a git repository"},
		{name: "empty output", runner: scriptedGitRunner{outputs: []GitOutput{{}}}, text: "Current directory is not inside a Git repository."},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DiscoverRepository(context.Background(), &testCase.runner)
			if testCase.want != nil {
				if !errors.Is(err, testCase.want) {
					t.Fatalf("DiscoverRepository() error = %v, want %v", err, testCase.want)
				}
				return
			}
			if err == nil || err.Error() != testCase.text {
				t.Fatalf("DiscoverRepository() error = %v, want %q", err, testCase.text)
			}
		})
	}
}

func TestReadLogUsesPathSafeGitArguments(t *testing.T) {
	root := "/tmp/repository"
	testCases := []struct {
		name       string
		rangeValue Range
		want       []string
	}{
		{
			name:       "limit",
			rangeValue: Range{Limit: 3},
			want: []string{
				"-C", root, "log", "-n", "3", "--no-color", "--name-status", "-z",
				"--pretty=format:%x00" + heatCommitMarker + "%H%x1f%ct%x1f%ci%x00",
			},
		},
		{
			name:       "days",
			rangeValue: Range{Days: 2},
			want: []string{
				"-C", root, "log", "--since=2 days ago", "--no-color", "--name-status", "-z",
				"--pretty=format:%x00" + heatCommitMarker + "%H%x1f%ct%x1f%ci%x00",
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &scriptedGitRunner{outputs: []GitOutput{{}}}
			log, err := ReadLog(context.Background(), runner, root, testCase.rangeValue)
			if err != nil {
				t.Fatalf("ReadLog() error = %v", err)
			}
			if log.CommitCount != 0 || len(log.Changes) != 0 {
				t.Fatalf("log = %#v, want empty", log)
			}
			if !reflect.DeepEqual(runner.arguments, [][]string{testCase.want}) {
				t.Fatalf("arguments = %#v, want %#v", runner.arguments, [][]string{testCase.want})
			}
		})
	}
}

func TestReadLogMapsGitFailures(t *testing.T) {
	testCases := []struct {
		name   string
		runner scriptedGitRunner
		want   error
		text   string
	}{
		{name: "runner", runner: scriptedGitRunner{err: context.Canceled}, want: context.Canceled},
		{name: "stderr", runner: scriptedGitRunner{outputs: []GitOutput{{ExitCode: 1, Stderr: []byte("fatal: bad revision\n")}}}, text: "fatal: bad revision"},
		{name: "fallback", runner: scriptedGitRunner{outputs: []GitOutput{{ExitCode: 1}}}, text: "Failed to read git log."},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ReadLog(context.Background(), &testCase.runner, "/tmp/repository", Range{Limit: 1})
			if testCase.want != nil {
				if !errors.Is(err, testCase.want) {
					t.Fatalf("ReadLog() error = %v, want %v", err, testCase.want)
				}
				return
			}
			if err == nil || err.Error() != testCase.text {
				t.Fatalf("ReadLog() error = %v, want %q", err, testCase.text)
			}
		})
	}
}

func TestReadLogParsesNativeGitNULRecordsFromNestedDirectory(t *testing.T) {
	repository := t.TempDir()
	runHeatGit(t, repository, "init", "-q")
	runHeatGit(t, repository, "config", "user.name", "Heat Test")
	runHeatGit(t, repository, "config", "user.email", "heat@example.test")

	firstName, addedName, quotedName, pathspecName := heatNativeFixtureNames()
	firstPath := filepath.Join(repository, firstName)
	if err := os.WriteFile(firstPath, []byte("one\n"), 0o600); err != nil {
		t.Fatalf("write first file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "unicode-中.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatalf("write unicode file: %v", err)
	}
	for _, name := range []string{quotedName, pathspecName} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte("special\n"), 0o600); err != nil {
			t.Fatalf("write special file %q: %v", name, err)
		}
	}
	runHeatGit(t, repository, "add", ".")
	runHeatGit(t, repository, "commit", "-qm", "initial")

	if err := os.WriteFile(firstPath, []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite first file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, addedName), []byte("three\n"), 0o600); err != nil {
		t.Fatalf("write newline file: %v", err)
	}
	runHeatGit(t, repository, "add", ".")
	runHeatGit(t, repository, "commit", "-qm", "second")

	nested := filepath.Join(repository, "nested", "directory")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	runner := nativeGitRunner{directory: nested}
	root, err := DiscoverRepository(context.Background(), runner)
	if err != nil {
		t.Fatalf("DiscoverRepository() error = %v", err)
	}
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatalf("resolve temporary repository: %v", err)
	}
	if normalizedGitPath(root) != normalizedGitPath(resolvedRepository) {
		t.Fatalf("root = %q, want %q", root, resolvedRepository)
	}
	log, err := ReadLog(context.Background(), runner, root, Range{Limit: 2})
	if err != nil {
		t.Fatalf("ReadLog() error = %v", err)
	}
	if log.CommitCount != 2 {
		t.Fatalf("commit count = %d, want 2", log.CommitCount)
	}
	if !hasHeatChange(log.Changes, ChangeModified, firstName) {
		t.Fatalf("changes do not include modified tab path: %#v", log.Changes)
	}
	if !hasHeatChange(log.Changes, ChangeAdded, addedName) {
		t.Fatalf("changes do not include added newline path: %#v", log.Changes)
	}
	if !hasHeatChange(log.Changes, ChangeAdded, "unicode-中.txt") {
		t.Fatalf("changes do not include unicode path: %#v", log.Changes)
	}
	if !hasHeatChange(log.Changes, ChangeAdded, quotedName) {
		t.Fatalf("changes do not include quote/backslash path: %#v", log.Changes)
	}
	if !hasHeatChange(log.Changes, ChangeAdded, pathspecName) {
		t.Fatalf("changes do not include pathspec-like path: %#v", log.Changes)
	}
}

func TestReadLogSupportsNativeRenameAndConfigurationDependentCopy(t *testing.T) {
	repository := newHeatRepository(t)
	runHeatGit(t, repository, "config", "diff.renames", "copies")
	writeHeatFile(t, repository, "old.txt", "same content\n")
	runHeatGit(t, repository, "add", ".")
	runHeatGit(t, repository, "commit", "-qm", "initial")

	runHeatGit(t, repository, "mv", "old.txt", "renamed.txt")
	writeHeatFile(t, repository, "copied.txt", "same content\n")
	runHeatGit(t, repository, "add", ".")
	runHeatGit(t, repository, "commit", "-qm", "rename and copy")

	runner := nativeGitRunner{directory: repository}
	root, err := DiscoverRepository(context.Background(), runner)
	if err != nil {
		t.Fatalf("DiscoverRepository() error = %v", err)
	}
	log, err := ReadLog(context.Background(), runner, root, Range{Limit: 1})
	if err != nil {
		t.Fatalf("ReadLog() error = %v", err)
	}
	if !hasHeatChange(log.Changes, ChangeRenamed, "renamed.txt") {
		t.Fatalf("changes do not include rename destination: %#v", log.Changes)
	}
	if !hasHeatChange(log.Changes, ChangeCopied, "copied.txt") {
		t.Fatalf("changes do not include copy destination: %#v", log.Changes)
	}
}

func TestNativeGitRepositoryKinds(t *testing.T) {
	t.Run("unborn repository fails at log", func(t *testing.T) {
		repository := t.TempDir()
		runHeatGit(t, repository, "init", "-q")
		runner := nativeGitRunner{directory: repository}
		root, err := DiscoverRepository(context.Background(), runner)
		if err != nil {
			t.Fatalf("DiscoverRepository() error = %v", err)
		}
		if _, err := ReadLog(context.Background(), runner, root, Range{Limit: 1}); err == nil {
			t.Fatal("ReadLog() error = nil for unborn repository")
		}
	})

	t.Run("bare repository fails at root discovery", func(t *testing.T) {
		root := t.TempDir()
		bare := filepath.Join(root, "bare.git")
		command := exec.Command("git", "init", "--bare", "-q", bare)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("create bare repository: %v\n%s", err, output)
		}
		if _, err := DiscoverRepository(context.Background(), nativeGitRunner{directory: bare}); err == nil {
			t.Fatal("DiscoverRepository() error = nil for bare repository")
		}
	})

	t.Run("linked worktree resolves and reads its own root", func(t *testing.T) {
		repository := newHeatRepository(t)
		writeHeatFile(t, repository, "file.txt", "initial\n")
		runHeatGit(t, repository, "add", ".")
		runHeatGit(t, repository, "commit", "-qm", "initial")
		worktree := filepath.Join(t.TempDir(), "linked")
		runHeatGit(t, repository, "worktree", "add", "-q", "-b", "heat-linked", worktree)
		t.Cleanup(func() {
			runHeatGit(t, repository, "worktree", "remove", "--force", worktree)
		})

		runner := nativeGitRunner{directory: worktree}
		root, err := DiscoverRepository(context.Background(), runner)
		if err != nil {
			t.Fatalf("DiscoverRepository() error = %v", err)
		}
		resolvedWorktree, err := filepath.EvalSymlinks(worktree)
		if err != nil {
			t.Fatalf("resolve worktree: %v", err)
		}
		if normalizedGitPath(root) != normalizedGitPath(resolvedWorktree) {
			t.Fatalf("root = %q, want %q", root, resolvedWorktree)
		}
		log, err := ReadLog(context.Background(), runner, root, Range{Limit: 1})
		if err != nil || log.CommitCount != 1 {
			t.Fatalf("ReadLog() = (%#v, %v)", log, err)
		}
	})
}

type scriptedGitRunner struct {
	outputs   []GitOutput
	err       error
	arguments [][]string
}

func (runner *scriptedGitRunner) Run(_ context.Context, arguments []string) (GitOutput, error) {
	runner.arguments = append(runner.arguments, append([]string(nil), arguments...))
	if runner.err != nil {
		return GitOutput{}, runner.err
	}
	if len(runner.outputs) == 0 {
		return GitOutput{}, nil
	}
	output := runner.outputs[0]
	runner.outputs = runner.outputs[1:]
	return output, nil
}

type nativeGitRunner struct {
	directory string
}

func (runner nativeGitRunner) Run(context context.Context, arguments []string) (GitOutput, error) {
	command := exec.CommandContext(context, "git", arguments...)
	command.Dir = runner.directory
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	output := GitOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		output.ExitCode = exitError.ExitCode()
		return output, nil
	}
	return output, err
}

func runHeatGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", arguments, err, output)
	}
}

func newHeatRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runHeatGit(t, repository, "init", "-q")
	runHeatGit(t, repository, "config", "user.name", "Heat Test")
	runHeatGit(t, repository, "config", "user.email", "heat@example.test")
	return repository
}

func writeHeatFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %q: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}

func heatNativeFixtureNames() (first, added, quoted, pathspec string) {
	if runtime.GOOS == "windows" {
		return "first name.txt", "line-name.txt", "quote-name.txt", "leading-pathspec.txt"
	}
	return "first\tname.txt", "line\nname.txt", "quote\"name\\path.txt", "-leading-[*]:.txt"
}

func normalizedGitPath(value string) string {
	return filepath.Clean(filepath.FromSlash(value))
}

func hasHeatChange(changes []Change, kind ChangeKind, path string) bool {
	for _, change := range changes {
		if change.Kind == kind && change.Path == path {
			return true
		}
	}
	return false
}
