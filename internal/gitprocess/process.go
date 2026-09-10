// Package gitprocess owns the process-level Git capability used by command leaves.
package gitprocess

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

const terminationGrace = 250 * time.Millisecond

// Output is the captured result of one external Git invocation. Command
// leaves adapt it to their own result types.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner executes the user's Git executable. An empty Executable uses git.
type Runner struct {
	Executable string
}

// Run executes Git with argv semantics and captures both output streams.
func (runner *Runner) Run(ctx context.Context, arguments []string) (Output, error) {
	return runner.RunInput(ctx, arguments, nil)
}

// RunInput preserves the shared Git lifecycle while allowing byte-oriented
// Git plumbing commands to receive standard input.
func (runner *Runner) RunInput(ctx context.Context, arguments []string, input []byte) (Output, error) {
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}

	executable := "git"
	if runner != nil && runner.Executable != "" {
		executable = runner.Executable
	}
	child := exec.Command(executable, arguments...)
	if input != nil {
		child.Stdin = bytes.NewReader(input)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	child.Stdout = &stdout
	child.Stderr = &stderr
	configureChild(child)
	if err := child.Start(); err != nil {
		return Output{}, normalizeProcessStartError(err)
	}

	exited := make(chan error, 1)
	go func() {
		exited <- child.Wait()
	}()

	select {
	case err := <-exited:
		return completed(stdout.Bytes(), stderr.Bytes(), err)
	case <-ctx.Done():
		if err := stopChild(child.Process, terminationSignal(ctx)); err != nil {
			return Output{}, err
		}
		var waitErr error
		select {
		case waitErr = <-exited:
		case <-time.After(terminationGrace):
			if err := killChild(child.Process); err != nil {
				return Output{}, err
			}
			waitErr = <-exited
		}
		if err := reapChild(child.Process); err != nil {
			return Output{}, err
		}
		_, _ = completed(stdout.Bytes(), stderr.Bytes(), waitErr)
		if code, ok := signalExitCode(ctx); ok {
			return Output{}, &SignalOutcome{code: code, cause: ctx.Err()}
		}
		return Output{}, ctx.Err()
	}
}

func completed(stdout, stderr []byte, err error) (Output, error) {
	output := Output{Stdout: stdout, Stderr: stderr}
	if err == nil {
		return output, nil
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		output.ExitCode = exited.ExitCode()
		return output, nil
	}
	return output, err
}

// SignalOutcome preserves the exit status users expect after a signal while
// retaining the cancellation cause for callers that inspect it.
type SignalOutcome struct {
	code  int
	cause error
}

func (outcome *SignalOutcome) Error() string {
	return outcome.cause.Error()
}

func (outcome *SignalOutcome) Unwrap() error {
	return outcome.cause
}

func (outcome *SignalOutcome) ExitCode() int {
	return outcome.code
}

type signalCause interface {
	Signal() os.Signal
}

func terminationSignal(ctx context.Context) os.Signal {
	if cause, ok := context.Cause(ctx).(signalCause); ok && cause.Signal() != nil {
		return cause.Signal()
	}
	return defaultTerminationSignal()
}
