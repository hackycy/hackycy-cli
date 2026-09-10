package fork

import (
	"context"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
)

type gitRunnerAdapter struct {
	runner *gitprocess.Runner
}

func (adapter gitRunnerAdapter) Run(ctx context.Context, arguments []string) (CloneOutput, error) {
	output, err := adapter.runner.Run(ctx, arguments)
	return CloneOutput{Stderr: output.Stderr, ExitCode: output.ExitCode}, err
}

type forkGitSignalOutcome = gitprocess.SignalOutcome
