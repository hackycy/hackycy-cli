package pulse

import (
	"context"
	"errors"
	"sort"
	"strings"
)

const gitLogConcurrency = 5

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

// Commit is one activity record read from a repository's current HEAD history.
type Commit struct {
	Repository string
	Author     string
	Date       string
	Subject    string
}

// FetchResult retains the otherwise silent repository inspection failures.
// They remain separate from commits so the caller can preserve legacy no-result behavior.
type FetchResult struct {
	Commits               []Commit
	FailedRepositories    int
	FailedRepositoryPaths []string
}

// FetchCommits reads the selected range from each repository with the legacy five-child limit.
func FetchCommits(ctx context.Context, runner GitRunner, repositories []string, since string, onProgress func(string, int)) (FetchResult, error) {
	if len(repositories) == 0 {
		return FetchResult{}, nil
	}

	type response struct {
		repository string
		commits    []Commit
		failed     bool
		err        error
	}
	responses := make(chan response, len(repositories))
	started := 0
	completed := 0
	result := FetchResult{}
	cancelled := false
	var cancellationOutcome error

	for started < len(repositories) || completed < started {
		if !cancelled && ctx.Err() != nil {
			cancelled = true
		}
		for !cancelled && started < len(repositories) && started-completed < gitLogConcurrency {
			repository := repositories[started]
			started++
			go func() {
				output, err := runner.Run(ctx, pulseGitLogArguments(repository, since))
				if err != nil || output.ExitCode != 0 {
					responses <- response{repository: repository, failed: true, err: err}
					return
				}
				responses <- response{repository: repository, commits: parsePulseLog(repository, output.Stdout)}
			}()
		}

		if completed == started {
			break
		}
		response := <-responses
		completed++
		if response.failed {
			result.FailedRepositories++
			result.FailedRepositoryPaths = append(result.FailedRepositoryPaths, response.repository)
			if cancellationOutcome == nil && isPulseExitCodedError(response.err) {
				cancellationOutcome = response.err
			}
		} else {
			result.Commits = append(result.Commits, response.commits...)
		}
		if onProgress != nil {
			onProgress(response.repository, completed)
		}
	}
	sort.Strings(result.FailedRepositoryPaths)

	if err := ctx.Err(); err != nil {
		if cancellationOutcome != nil {
			return result, cancellationOutcome
		}
		return result, err
	}
	return result, nil
}

type pulseExitCodedError interface {
	error
	ExitCode() int
}

func isPulseExitCodedError(err error) bool {
	var outcome pulseExitCodedError
	return errors.As(err, &outcome)
}

func pulseGitLogArguments(repository, since string) []string {
	return []string{
		"-C",
		repository,
		"log",
		"--since=" + since,
		"--date=format:%Y-%m-%d %H:%M:%S",
		"--pretty=format:%an%x1f%ad%x1f%s",
	}
}

func parsePulseLog(repository string, output []byte) []Commit {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) < 3 {
			continue
		}
		commits = append(commits, Commit{
			Repository: repository,
			Author:     parts[0],
			Date:       parts[1],
			Subject:    strings.Join(parts[2:], "\x1f"),
		})
	}
	return commits
}
