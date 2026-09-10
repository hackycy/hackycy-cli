package cm

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestInspectRepositoryCapturesSortedStatusAndFiltersSubmodules(t *testing.T) {
	runner := &scriptedGitRunner{responses: map[string]GitOutput{
		"rev-parse --show-toplevel": {Stdout: []byte("/fixture/repository\n")},
		"-C /fixture/repository status --porcelain=v1 -z --untracked-files=all": {Stdout: []byte(" M src/changed.go\x00R  renamed file.go\x00original file.go\x00C  copied file.go\x00source file.go\x00?? -untracked\tname\x00M  vendor/nested\x00")},
		"-C /fixture/repository ls-files --stage -z":                            {Stdout: []byte("100644 deadbeef 0\tsrc/changed.go\x00160000 cafebabe 0\tvendor/nested\x00")},
	}}

	state, err := InspectRepository(context.Background(), runner, ScopeAllUncommitted)
	if err != nil {
		t.Fatalf("InspectRepository() error = %v", err)
	}
	if state.Root != "/fixture/repository" || state.Scope != ScopeAllUncommitted {
		t.Fatalf("state = %#v", state)
	}
	want := []FileChange{
		{Path: "-untracked\tname", Status: "A -untracked\tname", IndexStatus: '?', WorktreeStatus: '?'},
		{Path: "copied file.go", OriginalPath: "source file.go", Status: "C source file.go -> copied file.go", IndexStatus: 'C', WorktreeStatus: ' '},
		{Path: "renamed file.go", OriginalPath: "original file.go", Status: "R original file.go -> renamed file.go", IndexStatus: 'R', WorktreeStatus: ' '},
		{Path: "src/changed.go", Status: "M src/changed.go", IndexStatus: ' ', WorktreeStatus: 'M'},
	}
	if !reflect.DeepEqual(state.Files, want) {
		t.Fatalf("files = %#v, want %#v", state.Files, want)
	}
	runner.requireCalls(t,
		[]string{"rev-parse", "--show-toplevel"},
		[]string{"-C", "/fixture/repository", "status", "--porcelain=v1", "-z", "--untracked-files=all"},
		[]string{"-C", "/fixture/repository", "ls-files", "--stage", "-z"},
	)
}

func TestInspectRepositoryLimitsStagedScopeToIndexChanges(t *testing.T) {
	runner := &scriptedGitRunner{responses: map[string]GitOutput{
		"rev-parse --show-toplevel":                               {Stdout: []byte("/repo\n")},
		"-C /repo status --porcelain=v1 -z --untracked-files=all": {Stdout: []byte(" M worktree.go\x00M  staged.go\x00MM both.go\x00?? untracked.go\x00")},
		"-C /repo ls-files --stage -z":                            {},
	}}

	state, err := InspectRepository(context.Background(), runner, ScopeStaged)
	if err != nil {
		t.Fatalf("InspectRepository() error = %v", err)
	}
	want := []FileChange{
		{Path: "both.go", Status: "MM both.go", IndexStatus: 'M', WorktreeStatus: 'M'},
		{Path: "staged.go", Status: "M staged.go", IndexStatus: 'M', WorktreeStatus: ' '},
	}
	if !reflect.DeepEqual(state.Files, want) {
		t.Fatalf("files = %#v, want %#v", state.Files, want)
	}
}

func TestParseGitStatusPreservesArbitraryPathCharacters(t *testing.T) {
	files := parseGitStatus([]byte("?? space name\x00?? tab\tname\x00?? line\nbreak\x00?? unicode-\xe4\xb8\xad\x00?? --pathspec=magic\x00R  destination\\name\x00original:glob[1]\x00"))
	want := []FileChange{
		{Path: "space name", Status: "A space name", IndexStatus: '?', WorktreeStatus: '?'},
		{Path: "tab\tname", Status: "A tab\tname", IndexStatus: '?', WorktreeStatus: '?'},
		{Path: "line\nbreak", Status: "A line\nbreak", IndexStatus: '?', WorktreeStatus: '?'},
		{Path: "unicode-\u4e2d", Status: "A unicode-\u4e2d", IndexStatus: '?', WorktreeStatus: '?'},
		{Path: "--pathspec=magic", Status: "A --pathspec=magic", IndexStatus: '?', WorktreeStatus: '?'},
		{Path: "destination\\name", OriginalPath: "original:glob[1]", Status: "R original:glob[1] -> destination\\name", IndexStatus: 'R', WorktreeStatus: ' '},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %#v, want %#v", files, want)
	}
}

func TestInspectRepositoryWrapsGitCaptureFailuresAndPreservesCause(t *testing.T) {
	transportFailure := errors.New("Git executable missing")
	runner := &scriptedGitRunner{errors: map[string]error{
		"rev-parse --show-toplevel": transportFailure,
	}}
	_, err := InspectRepository(context.Background(), runner, ScopeAllUncommitted)
	if err == nil || !errors.Is(err, transportFailure) {
		t.Fatalf("InspectRepository() error = %v, want wrapped transport failure", err)
	}
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Code != ErrorGitCapture || commandError.Error() != "Unable to capture Git snapshot: Git executable missing" {
		t.Fatalf("command error = %#v", commandError)
	}
}

func TestInspectRepositoryUsesGitDiagnosticsForNonzeroStatus(t *testing.T) {
	runner := &scriptedGitRunner{responses: map[string]GitOutput{
		"rev-parse --show-toplevel":                               {Stdout: []byte("/repo\n")},
		"-C /repo status --porcelain=v1 -z --untracked-files=all": {Stderr: []byte("fatal: unsafe repository\n"), ExitCode: 128},
		"-C /repo ls-files --stage -z":                            {},
	}}
	_, err := InspectRepository(context.Background(), runner, ScopeAllUncommitted)
	if err == nil || err.Error() != "Unable to capture Git snapshot: fatal: unsafe repository" {
		t.Fatalf("InspectRepository() error = %v", err)
	}
}

type scriptedGitRunner struct {
	mu        sync.Mutex
	responses map[string]GitOutput
	errors    map[string]error
	calls     [][]string
}

func (runner *scriptedGitRunner) Run(_ context.Context, arguments []string) (GitOutput, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	copyOfArguments := append([]string(nil), arguments...)
	runner.calls = append(runner.calls, copyOfArguments)
	key := gitRunnerKey(arguments)
	if err := runner.errors[key]; err != nil {
		return GitOutput{}, err
	}
	return runner.responses[key], nil
}

func (runner *scriptedGitRunner) requireCalls(t *testing.T, want ...[]string) {
	t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	if !reflect.DeepEqual(runner.calls[0], want[0]) {
		t.Fatalf("first call = %#v, want %#v", runner.calls[0], want[0])
	}
	for _, expected := range want[1:] {
		found := false
		for _, actual := range runner.calls[1:] {
			if reflect.DeepEqual(actual, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("calls = %#v, missing %#v", runner.calls, expected)
		}
	}
}

func gitRunnerKey(arguments []string) string {
	result := ""
	for index, argument := range arguments {
		if index > 0 {
			result += " "
		}
		result += argument
	}
	return result
}
