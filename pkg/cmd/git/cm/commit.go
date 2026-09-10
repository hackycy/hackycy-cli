package cm

import (
	"context"
	"errors"
	"strings"
	"unicode"
)

// CommitRequest ties one generated message to the snapshot that authorized it.
type CommitRequest struct {
	RepositoryRoot string
	Scope          Scope
	SnapshotID     string
	Message        string
}

// CommitSnapshot rechecks the modeled Git scope before invoking Git's normal commit path.
func CommitSnapshot(ctx context.Context, runner GitRunner, fileSystem SnapshotFileSystem, request CommitRequest) error {
	if request.RepositoryRoot == "" {
		return errors.New("Git commit repository root is required")
	}
	if request.SnapshotID == "" {
		return errors.New("Git commit snapshot ID is required")
	}
	if err := AssertSnapshotCurrent(ctx, runner, fileSystem, request.Scope, request.SnapshotID); err != nil {
		return err
	}
	return commitSnapshotMutation(ctx, runner, request)
}

// commitSnapshotMutation performs only the Git commit after the caller has
// completed the immutable-scope check. It is kept private so the command
// adapter can expose truthful verify/create phase boundaries without changing
// the public CommitSnapshot contract.
func commitSnapshotMutation(ctx context.Context, runner GitRunner, request CommitRequest) error {
	return runGitMutation(ctx, runner, commitArguments(request.RepositoryRoot, request.Message), nil, "git commit failed")
}

func commitArguments(root, message string) []string {
	parts := strings.Split(message, "\n")
	for index := range parts {
		parts[index] = strings.TrimRightFunc(parts[index], unicode.IsSpace)
	}
	arguments := []string{"-C", root, "commit", "-m", parts[0]}
	body := strings.TrimSpace(strings.Join(parts[1:], "\n"))
	if body != "" {
		arguments = append(arguments, "-m", body)
	}
	return arguments
}
