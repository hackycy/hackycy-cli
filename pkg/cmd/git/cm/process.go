package cm

import (
	"context"
	"io"
	"io/fs"
	"os"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
)

type gitRunnerAdapter struct {
	runner *gitprocess.Runner
}

func (adapter gitRunnerAdapter) Run(ctx context.Context, arguments []string) (GitOutput, error) {
	output, err := adapter.runner.Run(ctx, arguments)
	return cmGitOutput(output), err
}

func (adapter gitRunnerAdapter) RunInput(ctx context.Context, arguments []string, input []byte) (GitOutput, error) {
	output, err := adapter.runner.RunInput(ctx, arguments, input)
	return cmGitOutput(output), err
}

func cmGitOutput(output gitprocess.Output) GitOutput {
	return GitOutput{
		Stdout:   output.Stdout,
		Stderr:   output.Stderr,
		ExitCode: output.ExitCode,
	}
}

type osCMSnapshotFileSystem struct{}

func (osCMSnapshotFileSystem) Lstat(path string) (fs.FileInfo, error) {
	return os.Lstat(path)
}

func (osCMSnapshotFileSystem) Open(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (osCMSnapshotFileSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
