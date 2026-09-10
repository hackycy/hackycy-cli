package heat

import (
	"context"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
)

type gitRunnerAdapter struct {
	runner *gitprocess.Runner
}

func (adapter gitRunnerAdapter) Run(ctx context.Context, arguments []string) (GitOutput, error) {
	output, err := adapter.runner.Run(ctx, arguments)
	return GitOutput{
		Stdout:   output.Stdout,
		Stderr:   output.Stderr,
		ExitCode: output.ExitCode,
	}, err
}

type heatGitSignalOutcome = gitprocess.SignalOutcome
