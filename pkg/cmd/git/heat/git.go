package heat

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

const heatCommitMarker = "__HACKYCY_HEAT_COMMIT__"

// GitOutput is the captured result of one external Git invocation.
type GitOutput struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// GitRunner is the command-owned boundary for invoking the user's Git binary.
type GitRunner interface {
	Run(context.Context, []string) (GitOutput, error)
}

// DiscoverRepository asks Git to resolve the current repository root.
func DiscoverRepository(context context.Context, runner GitRunner) (string, error) {
	output, err := runner.Run(context, []string{"rev-parse", "--show-toplevel"})
	if err != nil {
		return "", err
	}
	if output.ExitCode != 0 {
		return "", gitFailure(output.Stderr, "Current directory is not inside a Git repository.")
	}
	root := strings.TrimSpace(string(output.Stdout))
	if root == "" {
		return "", errors.New("Current directory is not inside a Git repository.")
	}
	return root, nil
}

// ReadLog runs the path-safe Git log plumbing command for one selected range.
func ReadLog(context context.Context, runner GitRunner, repositoryRoot string, rangeValue Range) (Log, error) {
	output, err := runner.Run(context, gitLogArguments(repositoryRoot, rangeValue))
	if err != nil {
		return Log{}, err
	}
	if output.ExitCode != 0 {
		return Log{}, gitFailure(output.Stderr, "Failed to read git log.")
	}
	return ParseLog(output.Stdout)
}

func gitLogArguments(repositoryRoot string, rangeValue Range) []string {
	arguments := []string{"-C", repositoryRoot, "log"}
	if rangeValue.IsDayRange() {
		arguments = append(arguments, "--since="+strconv.Itoa(rangeValue.Days)+" days ago")
	} else {
		arguments = append(arguments, "-n", strconv.Itoa(rangeValue.Limit))
	}
	return append(arguments,
		"--no-color",
		"--name-status",
		"-z",
		"--pretty=format:%x00"+heatCommitMarker+"%H%x1f%ct%x1f%ci%x00",
	)
}

func gitFailure(stderr []byte, fallback string) error {
	message := strings.TrimSpace(string(stderr))
	if message == "" {
		message = fallback
	}
	return errors.New(message)
}
