package cm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Scope identifies the Git state included in commit-message generation.
type Scope string

const (
	// ScopeAllUncommitted includes staged, worktree, and untracked changes.
	ScopeAllUncommitted Scope = "all-uncommitted"
	// ScopeStaged includes only index changes.
	ScopeStaged Scope = "staged"
)

// GitOutput is the captured result of one command-owned Git invocation.
type GitOutput struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// GitRunner is the command-owned boundary for the user's Git executable.
type GitRunner interface {
	Run(context.Context, []string) (GitOutput, error)
}

// FileChange is one modeled status entry from the selected Git scope.
type FileChange struct {
	Path           string
	OriginalPath   string
	Status         string
	IndexStatus    byte
	WorktreeStatus byte
}

// RepositoryState is the status and index view used by later snapshot capture.
type RepositoryState struct {
	Root  string
	Scope Scope
	Files []FileChange
}

// ErrorCode classifies Git CM failures before cliapp maps them to an exit outcome.
type ErrorCode string

const (
	// ErrorGitCapture identifies repository inspection and snapshot failures.
	ErrorGitCapture ErrorCode = "GIT_CAPTURE_FAILED"
	// ErrorStaleScope identifies a scope changed after message generation.
	ErrorStaleScope ErrorCode = "STALE_GIT_SCOPE"
	// ErrorNoChanges identifies a selected Git scope with no modeled files.
	ErrorNoChanges ErrorCode = "NO_CHANGES"
	// ErrorEvidenceBuild identifies a failure compiling local semantic evidence.
	ErrorEvidenceBuild ErrorCode = "EVIDENCE_BUILD_FAILED"
	// ErrorModelUnavailable identifies a provider transport or response failure.
	ErrorModelUnavailable ErrorCode = "MODEL_UNAVAILABLE"
	// ErrorInvalidModelOutput identifies a response that cannot become a commit message.
	ErrorInvalidModelOutput ErrorCode = "INVALID_MODEL_OUTPUT"
)

// CommandError preserves the command-local failure category and its source error.
type CommandError struct {
	Code  ErrorCode
	Text  string
	Cause error
}

func (err *CommandError) Error() string {
	return err.Text
}

func (err *CommandError) Unwrap() error {
	return err.Cause
}

// InspectRepository resolves the active repository and reads its selected change set.
func InspectRepository(ctx context.Context, runner GitRunner, scope Scope) (RepositoryState, error) {
	root, err := discoverRepository(ctx, runner)
	if err != nil {
		return RepositoryState{}, captureError(err)
	}

	type response struct {
		output GitOutput
		err    error
	}
	statusResult := make(chan response, 1)
	indexResult := make(chan response, 1)
	go func() {
		output, err := runner.Run(ctx, []string{"-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all"})
		statusResult <- response{output: output, err: err}
	}()
	go func() {
		output, err := runner.Run(ctx, []string{"-C", root, "ls-files", "--stage", "-z"})
		indexResult <- response{output: output, err: err}
	}()
	status := <-statusResult
	index := <-indexResult
	if status.err != nil {
		return RepositoryState{}, captureError(status.err)
	}
	if index.err != nil {
		return RepositoryState{}, captureError(index.err)
	}
	if status.output.ExitCode != 0 {
		return RepositoryState{}, captureError(gitOutputError(status.output, "git status failed"))
	}
	if index.output.ExitCode != 0 {
		return RepositoryState{}, captureError(gitOutputError(index.output, "git ls-files failed"))
	}

	files := parseGitStatus(status.output.Stdout)
	submodules := parseSubmodulePaths(index.output.Stdout)
	files = filterScope(files, scope)
	files = filterSubmodules(files, submodules)
	sort.Slice(files, func(left, right int) bool {
		return files[left].Path < files[right].Path
	})
	return RepositoryState{Root: root, Scope: scope, Files: files}, nil
}

func discoverRepository(ctx context.Context, runner GitRunner) (string, error) {
	output, err := runner.Run(ctx, []string{"rev-parse", "--show-toplevel"})
	if err != nil {
		return "", err
	}
	if output.ExitCode != 0 {
		return "", gitOutputError(output, "Current directory is not inside a Git repository.")
	}
	root := strings.TrimSpace(string(output.Stdout))
	if root == "" {
		return "", errors.New("Current directory is not inside a Git repository.")
	}
	return root, nil
}

func captureError(cause error) error {
	var commandError *CommandError
	if errors.As(cause, &commandError) {
		return cause
	}
	return &CommandError{Code: ErrorGitCapture, Text: "Unable to capture Git snapshot: " + cause.Error(), Cause: cause}
}

func gitOutputError(output GitOutput, fallback string) error {
	message := strings.TrimSpace(string(output.Stderr))
	if message == "" {
		message = fallback
	}
	return errors.New(message)
}

func parseGitStatus(output []byte) []FileChange {
	entries := strings.Split(string(output), "\x00")
	files := make([]FileChange, 0, len(entries))
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if entry == "" {
			continue
		}
		if len(entry) < 3 {
			continue
		}
		indexStatus := entry[0]
		worktreeStatus := entry[1]
		filePath := entry[3:]
		originalPath := ""
		if (indexStatus == 'R' || indexStatus == 'C') && index+1 < len(entries) {
			index++
			originalPath = entries[index]
		}
		files = append(files, FileChange{
			Path:           filePath,
			OriginalPath:   originalPath,
			Status:         formatGitStatus(indexStatus, worktreeStatus, filePath, originalPath),
			IndexStatus:    indexStatus,
			WorktreeStatus: worktreeStatus,
		})
	}
	return files
}

func formatGitStatus(indexStatus, worktreeStatus byte, filePath, originalPath string) string {
	if indexStatus == '?' && worktreeStatus == '?' {
		return "A " + filePath
	}
	if indexStatus == 'R' || indexStatus == 'C' {
		if originalPath == "" {
			originalPath = filePath
		}
		return fmt.Sprintf("%c %s -> %s", indexStatus, originalPath, filePath)
	}
	status := strings.TrimSpace(string([]byte{indexStatus, worktreeStatus}))
	if status == "" {
		status = "M"
	}
	return status + " " + filePath
}

func parseSubmodulePaths(output []byte) map[string]struct{} {
	paths := make(map[string]struct{})
	for _, entry := range strings.Split(string(output), "\x00") {
		tab := strings.IndexByte(entry, '\t')
		if tab < 0 || !strings.HasPrefix(entry, "160000 ") {
			continue
		}
		paths[entry[tab+1:]] = struct{}{}
	}
	return paths
}

func filterScope(files []FileChange, scope Scope) []FileChange {
	if scope != ScopeStaged {
		return files
	}
	filtered := make([]FileChange, 0, len(files))
	for _, file := range files {
		if file.IndexStatus == ' ' || file.IndexStatus == '?' {
			continue
		}
		filtered = append(filtered, file)
	}
	return filtered
}

func filterSubmodules(files []FileChange, paths map[string]struct{}) []FileChange {
	filtered := make([]FileChange, 0, len(files))
	for _, file := range files {
		if _, found := paths[file.Path]; !found {
			filtered = append(filtered, file)
		}
	}
	return filtered
}
