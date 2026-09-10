package cm

import (
	"context"
	"errors"
	"strings"
)

// PushCommit pushes the current branch through Git's normal upstream workflow.
func PushCommit(ctx context.Context, runner GitRunner, root, remote string) error {
	branchOutput, err := runner.Run(ctx, []string{"-C", root, "branch", "--show-current"})
	if err != nil {
		return err
	}
	if branchOutput.ExitCode != 0 {
		return gitOutputError(branchOutput, "git branch --show-current failed")
	}
	branch := strings.TrimSpace(string(branchOutput.Stdout))
	if branch == "" {
		return errors.New("Cannot push from detached HEAD. Check out a branch first.")
	}
	if remote == "" {
		remote = "origin"
	}
	return runGitMutation(ctx, runner, []string{"-C", root, "push", "-u", remote, branch}, nil, "git push failed")
}
