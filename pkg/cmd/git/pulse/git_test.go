package pulse

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestFetchCommitsUsesTheLegacyGitLogArgumentsAndParsesRecords(t *testing.T) {
	const repository = "/workspace/project"
	const since = "2026-08-22 00:00:00"
	runner := &scriptedPulseGitRunner{responses: map[string]pulseGitResponse{
		repository: {output: GitOutput{Stdout: []byte("Ada\x1f2026-08-23 10:11:12\x1ffirst\n\nGrace\x1f2026-08-22 09:08:07\x1fsubject\x1fwith separator\nmalformed\n")}},
	}}

	result, err := FetchCommits(context.Background(), runner, []string{repository}, since, nil)
	if err != nil {
		t.Fatalf("FetchCommits() error = %v", err)
	}
	if result.FailedRepositories != 0 {
		t.Fatalf("failed repositories = %d, want 0", result.FailedRepositories)
	}
	want := []Commit{
		{Repository: repository, Author: "Ada", Date: "2026-08-23 10:11:12", Subject: "first"},
		{Repository: repository, Author: "Grace", Date: "2026-08-22 09:08:07", Subject: "subject\x1fwith separator"},
	}
	if !reflect.DeepEqual(result.Commits, want) {
		t.Fatalf("commits = %#v, want %#v", result.Commits, want)
	}
	wantArguments := []string{
		"-C", repository, "log", "--since=" + since,
		"--date=format:%Y-%m-%d %H:%M:%S", "--pretty=format:%an%x1f%ad%x1f%s",
	}
	if !reflect.DeepEqual(runner.arguments, [][]string{wantArguments}) {
		t.Fatalf("arguments = %#v, want %#v", runner.arguments, [][]string{wantArguments})
	}
}

func TestParsePulseLogPreservesUnicodeAndTerminalControlCharacters(t *testing.T) {
	commits := parsePulseLog("/workspace", []byte("Ada\x1f2026-08-23 10:11:12\x1fUnicode \u2603 and \x1b[31mcontrol\x1b[0m"))
	want := []Commit{{
		Repository: "/workspace",
		Author:     "Ada",
		Date:       "2026-08-23 10:11:12",
		Subject:    "Unicode \u2603 and \x1b[31mcontrol\x1b[0m",
	}}
	if !reflect.DeepEqual(commits, want) {
		t.Fatalf("commits = %#v, want %#v", commits, want)
	}
}

func TestFetchCommitsPreservesSilentPartialFailures(t *testing.T) {
	good := "/workspace/good"
	exited := "/workspace/exited"
	missing := "/workspace/missing"
	runner := &scriptedPulseGitRunner{responses: map[string]pulseGitResponse{
		good:    {output: GitOutput{Stdout: []byte("Ada\x1f2026-08-23 10:11:12\x1fkept")}},
		exited:  {output: GitOutput{Stderr: []byte("fatal"), ExitCode: 1}},
		missing: {err: errors.New("missing git")},
	}}

	var progress []string
	result, err := FetchCommits(context.Background(), runner, []string{good, exited, missing}, "2026-08-23 00:00:00", func(repository string, done int) {
		progress = append(progress, repository+"/"+string(rune('0'+done)))
	})
	if err != nil {
		t.Fatalf("FetchCommits() error = %v", err)
	}
	if result.FailedRepositories != 2 {
		t.Fatalf("failed repositories = %d, want 2", result.FailedRepositories)
	}
	if got, want := result.FailedRepositoryPaths, []string{exited, missing}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed repository paths = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(result.Commits, []Commit{{Repository: good, Author: "Ada", Date: "2026-08-23 10:11:12", Subject: "kept"}}) {
		t.Fatalf("commits = %#v", result.Commits)
	}
	if len(progress) != 3 {
		t.Fatalf("progress calls = %#v, want one for every repository", progress)
	}
}

func TestFetchCommitsLimitsGitExecutionToFiveChildren(t *testing.T) {
	repositories := []string{"one", "two", "three", "four", "five", "six", "seven"}
	runner := newConcurrentPulseGitRunner()
	type fetchOutcome struct {
		result FetchResult
		err    error
	}
	resultChannel := make(chan fetchOutcome, 1)
	go func() {
		result, err := FetchCommits(context.Background(), runner, repositories, "2026-08-23 00:00:00", nil)
		resultChannel <- fetchOutcome{result: result, err: err}
	}()

	for range gitLogConcurrency {
		select {
		case <-runner.started:
		case <-time.After(2 * time.Second):
			t.Fatal("did not start five Git children")
		}
	}
	if got := runner.maximumActive(); got != gitLogConcurrency {
		t.Fatalf("maximum concurrent Git children = %d, want %d", got, gitLogConcurrency)
	}
	close(runner.release)
	select {
	case outcome := <-resultChannel:
		if outcome.err != nil {
			t.Fatalf("FetchCommits() error = %v", outcome.err)
		}
		if outcome.result.FailedRepositories != 0 {
			t.Fatalf("failed repositories = %d", outcome.result.FailedRepositories)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchCommits did not complete")
	}
}

func TestFetchCommitsStopsSchedulingRepositoriesAfterCancellation(t *testing.T) {
	repositories := []string{"one", "two", "three", "four", "five", "six", "seven"}
	runner := newCancellationPulseGitRunner()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type fetchOutcome struct {
		result FetchResult
		err    error
	}
	resultChannel := make(chan fetchOutcome, 1)
	go func() {
		result, err := FetchCommits(ctx, runner, repositories, "2026-08-23 00:00:00", nil)
		resultChannel <- fetchOutcome{result: result, err: err}
	}()

	for range gitLogConcurrency {
		select {
		case <-runner.started:
		case <-time.After(2 * time.Second):
			t.Fatal("did not start five Git children")
		}
	}
	cancel()
	select {
	case outcome := <-resultChannel:
		if !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("FetchCommits() error = %v, want context cancellation", outcome.err)
		}
		var exitOutcome pulseExitCodedError
		if !errors.As(outcome.err, &exitOutcome) || exitOutcome.ExitCode() != 143 {
			t.Fatalf("FetchCommits() error = %v, want exit code 143", outcome.err)
		}
		if calls := runner.callCount(); calls != gitLogConcurrency {
			t.Fatalf("Git calls = %d, want %d", calls, gitLogConcurrency)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchCommits did not return after cancellation")
	}
}

type pulseGitResponse struct {
	output GitOutput
	err    error
}

type scriptedPulseGitRunner struct {
	mu        sync.Mutex
	responses map[string]pulseGitResponse
	arguments [][]string
}

func (runner *scriptedPulseGitRunner) Run(_ context.Context, arguments []string) (GitOutput, error) {
	copyOfArguments := append([]string(nil), arguments...)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.arguments = append(runner.arguments, copyOfArguments)
	return runner.responses[arguments[1]].output, runner.responses[arguments[1]].err
}

type concurrentPulseGitRunner struct {
	started chan struct{}
	release chan struct{}

	mu     sync.Mutex
	active int
	max    int
}

func newConcurrentPulseGitRunner() *concurrentPulseGitRunner {
	return &concurrentPulseGitRunner{
		started: make(chan struct{}, gitLogConcurrency),
		release: make(chan struct{}),
	}
}

func (runner *concurrentPulseGitRunner) Run(_ context.Context, _ []string) (GitOutput, error) {
	runner.mu.Lock()
	runner.active++
	if runner.active > runner.max {
		runner.max = runner.active
	}
	runner.mu.Unlock()
	runner.started <- struct{}{}
	<-runner.release
	runner.mu.Lock()
	runner.active--
	runner.mu.Unlock()
	return GitOutput{}, nil
}

func (runner *concurrentPulseGitRunner) maximumActive() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.max
}

type cancellationPulseGitRunner struct {
	started chan struct{}

	mu    sync.Mutex
	calls int
}

func newCancellationPulseGitRunner() *cancellationPulseGitRunner {
	return &cancellationPulseGitRunner{started: make(chan struct{}, gitLogConcurrency)}
}

func (runner *cancellationPulseGitRunner) Run(context context.Context, _ []string) (GitOutput, error) {
	runner.mu.Lock()
	runner.calls++
	runner.mu.Unlock()
	runner.started <- struct{}{}
	<-context.Done()
	return GitOutput{}, pulseFetchExitError{cause: context.Err(), code: 143}
}

type pulseFetchExitError struct {
	cause error
	code  int
}

func (err pulseFetchExitError) Error() string {
	return err.cause.Error()
}

func (err pulseFetchExitError) Unwrap() error {
	return err.cause
}

func (err pulseFetchExitError) ExitCode() int {
	return err.code
}

func (runner *cancellationPulseGitRunner) callCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.calls
}
