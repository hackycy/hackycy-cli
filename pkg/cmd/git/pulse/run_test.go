package pulse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestModuleRunComposesScanPromptFetchAuthorSelectionAndReport(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	makePulseDirectory(t, filepath.Join(root, ".git"))
	makePulseDirectory(t, filepath.Join(nested, ".git"))
	runner := &modulePulseGitRunner{logs: map[string]pulseGitResponse{
		root:   {output: GitOutput{Stdout: []byte("Ada\x1f2026-08-23 10:00:00\x1froot commit")}},
		nested: {output: GitOutput{Stdout: []byte("Ben\x1f2026-08-22 10:00:00\x1fnested commit")}},
	}}
	prompter := &scriptedPulsePrompter{days: 2, authors: []string{"Ada"}}
	presenter := &recordingPulsePresenter{}
	module := newPulseModuleForTest(t, root, runner, prompter, presenter)

	result, err := module.Run(context.Background(), Input{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FailedRepositories != 0 || result.Report.CommitCount != 1 {
		t.Fatalf("result = %#v", result)
	}
	wantReport := BuildReport([]Commit{{Repository: root, Author: "Ada", Date: "2026-08-23 10:00:00", Subject: "root commit"}})
	if !reflect.DeepEqual(result.Report, wantReport) {
		t.Fatalf("report = %#v, want %#v", result.Report, wantReport)
	}
	if got, want := presenter.introductions, []string{root}; !reflect.DeepEqual(got, want) {
		t.Fatalf("introductions = %#v, want %#v", got, want)
	}
	if presenter.repositoriesFound != 2 {
		t.Fatalf("repository result presentation = %#v", presenter)
	}
	if !reflect.DeepEqual(presenter.reports, []Report{wantReport}) {
		t.Fatalf("presented reports = %#v", presenter.reports)
	}
	if got := runner.sinceArguments(); !reflect.DeepEqual(got, []string{"--since=2026-08-22 00:00:00", "--since=2026-08-22 00:00:00"}) {
		t.Fatalf("since arguments = %#v", got)
	}
}

func TestModuleRunEmitsTheDeclaredPrepareScanFetchAndBuildPhases(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	makePulseDirectory(t, filepath.Join(root, ".git"))
	makePulseDirectory(t, filepath.Join(nested, ".git"))
	tracker := &recordingPulseTracker{}
	module := newPulseModuleWithDependencies(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Stater:           osPulseStater{},
		Reader:           testPulseDirectoryReader{},
		Yield:            func() {},
		Git: &modulePulseGitRunner{logs: map[string]pulseGitResponse{
			root:   {output: GitOutput{Stdout: []byte("Ada\x1f2026-08-23 10:00:00\x1froot")}},
			nested: {output: GitOutput{Stdout: []byte("Ben\x1f2026-08-22 10:00:00\x1fnested")}},
		}},
		Prompter:  &scriptedPulsePrompter{days: 1, authors: []string{"Ada"}},
		Presenter: &recordingPulsePresenter{},
		Tracker:   tracker,
		Now:       time.Now,
	})

	if _, err := module.Run(context.Background(), Input{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := tracker.started, []PhaseKind{PhasePrepare, PhaseScan, PhaseFetch, PhaseBuild}; !reflect.DeepEqual(got, want) {
		t.Fatalf("started phases = %#v, want %#v", got, want)
	}
	if tracker.closed != 4 {
		t.Fatalf("closed phase reporters = %d, want 4", tracker.closed)
	}
	if len(tracker.phases) < 8 {
		t.Fatalf("phase updates = %#v", tracker.phases)
	}
	prepare := phaseUpdates(tracker.phases, PhasePrepare)
	if first, last := prepare[0], prepare[len(prepare)-1]; first.State != PhaseActive || first.Detail != "Checking workspace and Git" || last.State != PhaseCompleted || last.Detail != "Checking workspace and Git" {
		t.Fatalf("prepare phase boundaries = %#v", prepare)
	}
	scan := phaseUpdates(tracker.phases, PhaseScan)
	if first, last := scan[0], scan[len(scan)-1]; first.State != PhaseActive || first.Root != root || last.State != PhaseCompleted || last.RepositoryCount != 2 || last.Detail != "Found 2 repositories" {
		t.Fatalf("scan phase boundaries = %#v", tracker.phases)
	}
	fetch := phaseUpdates(tracker.phases, PhaseFetch)
	if first, last := fetch[0], fetch[len(fetch)-1]; first.State != PhaseActive || first.Total != 2 || last.State != PhaseCompleted || last.Successful != 2 || last.Detail != "Read 2 of 2 repositories" {
		t.Fatalf("fetch phase boundaries = %#v", tracker.phases)
	}
	build := phaseUpdates(tracker.phases, PhaseBuild)
	if first, last := build[0], build[len(build)-1]; first.State != PhaseActive || first.Detail != "Grouping commits by repository" || last.State != PhaseCompleted || last.CommitCount != 1 || last.RepositoryCount != 1 || last.Detail != "Built report with 1 commits in 1 repositories" {
		t.Fatalf("build phase boundaries = %#v", build)
	}
}

func phaseUpdates(phases []Phase, kind PhaseKind) []Phase {
	updates := make([]Phase, 0)
	for _, phase := range phases {
		if phase.Kind == kind {
			updates = append(updates, phase)
		}
	}
	return updates
}

func TestModuleRunReturnsSuccessfulNoResultAndPromptCancellationOutcomes(t *testing.T) {
	t.Run("no repositories", func(t *testing.T) {
		root := t.TempDir()
		runner := &modulePulseGitRunner{}
		prompter := &scriptedPulsePrompter{}
		presenter := &recordingPulsePresenter{}
		module := newPulseModuleForTest(t, root, runner, prompter, presenter)

		result, err := module.Run(context.Background(), Input{})
		if err != nil || !reflect.DeepEqual(result, Result{}) {
			t.Fatalf("Run() = (%#v, %v)", result, err)
		}
		if presenter.noRepositories != 1 || runner.logCallCount() != 0 || prompter.dayCalls != 0 {
			t.Fatalf("no-repository outcome = presenter %#v, Git logs %d, day calls %d", presenter, runner.logCallCount(), prompter.dayCalls)
		}
	})

	t.Run("day cancellation", func(t *testing.T) {
		root := t.TempDir()
		makePulseDirectory(t, filepath.Join(root, ".git"))
		runner := &modulePulseGitRunner{}
		prompter := &scriptedPulsePrompter{daysCancelled: true}
		presenter := &recordingPulsePresenter{}
		module := newPulseModuleForTest(t, root, runner, prompter, presenter)

		result, err := module.Run(context.Background(), Input{})
		if err != nil || !reflect.DeepEqual(result, Result{}) || presenter.cancelled != 1 || runner.logCallCount() != 0 {
			t.Fatalf("day cancellation = (%#v, %v), presenter %#v, logs %d", result, err, presenter, runner.logCallCount())
		}
	})

	t.Run("all failed repositories", func(t *testing.T) {
		root := t.TempDir()
		makePulseDirectory(t, filepath.Join(root, ".git"))
		runner := &modulePulseGitRunner{logs: map[string]pulseGitResponse{root: {err: errors.New("failed")}}}
		prompter := &scriptedPulsePrompter{days: 1}
		presenter := &recordingPulsePresenter{}
		module := newPulseModuleForTest(t, root, runner, prompter, presenter)

		result, err := module.Run(context.Background(), Input{})
		if err != nil || result.FailedRepositories != 1 || presenter.noCommits != 1 {
			t.Fatalf("partial no-result = (%#v, %v), presenter %#v", result, err, presenter)
		}
	})

	t.Run("author cancellation", func(t *testing.T) {
		root := t.TempDir()
		makePulseDirectory(t, filepath.Join(root, ".git"))
		runner := &modulePulseGitRunner{logs: map[string]pulseGitResponse{root: {output: GitOutput{Stdout: []byte("Ada\x1f2026-08-23 10:00:00\x1fone\nBen\x1f2026-08-23 09:00:00\x1ftwo")}}}}
		prompter := &scriptedPulsePrompter{days: 1, authorsCancelled: true}
		presenter := &recordingPulsePresenter{}
		module := newPulseModuleForTest(t, root, runner, prompter, presenter)

		result, err := module.Run(context.Background(), Input{})
		if err != nil || !reflect.DeepEqual(result.Report, Report{}) || presenter.cancelled != 1 || len(presenter.reports) != 0 {
			t.Fatalf("author cancellation = (%#v, %v), presenter %#v", result, err, presenter)
		}
	})
}

func TestModuleRunPublishesBoundedPresentationDetailsWithoutChangingPartialResults(t *testing.T) {
	root := t.TempDir()
	visible := filepath.Join(root, "visible")
	unreadable := filepath.Join(root, "unreadable")
	for _, repository := range []string{root, visible, unreadable} {
		makePulseDirectory(t, filepath.Join(repository, ".git"))
	}
	reader := directoryReaderFunc(func(path string) ([]os.DirEntry, error) {
		if path == unreadable {
			return nil, errors.New("permission denied")
		}
		return os.ReadDir(path)
	})
	presenter := &recordingPulsePresenter{}
	module := newPulseModuleWithDependencies(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Stater:           osPulseStater{},
		Reader:           reader,
		Yield:            func() {},
		Git: &modulePulseGitRunner{logs: map[string]pulseGitResponse{
			root:    {output: GitOutput{Stdout: []byte("Ada\x1f2026-08-23 10:00:00\x1froot")}},
			visible: {err: errors.New("repository omitted")},
		}},
		Prompter:  &scriptedPulsePrompter{days: 1},
		Presenter: presenter,
		Now: func() time.Time {
			return time.Date(2026, time.August, 23, 14, 0, 0, 0, time.UTC)
		},
	})

	result, err := module.Run(context.Background(), Input{Days: pulseInt(1)})
	if err != nil || result.Report.CommitCount != 1 || result.FailedRepositories != 1 {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	if got, want := presenter.dateSelections, []pulseDateSelection{{days: 1, explicit: true, boundary: "2026-08-23 00:00:00"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("date selections = %#v, want %#v", got, want)
	}
	if got, want := presenter.scanWarnings, []pulsePathWarning{{root: root, paths: []string{unreadable}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scan warnings = %#v, want %#v", got, want)
	}
	if got, want := presenter.fetchWarnings, []pulsePathWarning{{root: root, paths: []string{visible}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetch warnings = %#v, want %#v", got, want)
	}
	if got, want := presenter.allAuthorCounts, []int{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("all-author details = %#v, want %#v", got, want)
	}
}

func TestModuleRunValidatesRootAndGitBeforePresentation(t *testing.T) {
	root := t.TempDir()
	presenter := &recordingPulsePresenter{}
	runner := &modulePulseGitRunner{}
	module := newPulseModuleForTest(t, root, runner, &scriptedPulsePrompter{}, presenter)

	_, err := module.Run(context.Background(), Input{Directory: "missing"})
	if got, want := err.Error(), "Directory not found: "+filepath.Join(root, "missing"); got != want {
		t.Fatalf("missing root error = %q, want %q", got, want)
	}
	if runner.callCount() != 0 || len(presenter.introductions) != 0 {
		t.Fatalf("missing root performed Git or presentation work")
	}

	makePulseDirectory(t, filepath.Join(root, "valid"))
	runner.version = GitOutput{ExitCode: 1}
	_, err = module.Run(context.Background(), Input{Directory: "valid"})
	if !errors.Is(err, errPulseGitUnavailable) || len(presenter.introductions) != 0 {
		t.Fatalf("unavailable Git result = %v, presenter %#v", err, presenter)
	}
}

func TestModuleRunReturnsPreCancelledContextWithoutWork(t *testing.T) {
	root := t.TempDir()
	runner := &modulePulseGitRunner{}
	presenter := &recordingPulsePresenter{}
	module := newPulseModuleForTest(t, root, runner, &scriptedPulsePrompter{}, presenter)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := module.Run(ctx, Input{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context cancellation", err)
	}
	if runner.callCount() != 0 || len(presenter.introductions) != 0 {
		t.Fatal("pre-cancelled Run() performed work")
	}
}

func TestModuleRunDoesNotPromptAfterContextCancellationAtPromptBoundaries(t *testing.T) {
	t.Run("before day prompt", func(t *testing.T) {
		root := t.TempDir()
		makePulseDirectory(t, filepath.Join(root, ".git"))
		ctx, cancel := context.WithCancel(context.Background())
		reader := directoryReaderFunc(func(path string) ([]os.DirEntry, error) {
			entries, err := os.ReadDir(path)
			if path == root {
				cancel()
			}
			return entries, err
		})
		prompter := &scriptedPulsePrompter{}
		module := newPulseModuleWithDependencies(t, Dependencies{
			WorkingDirectory: func() (string, error) { return root, nil },
			Stater:           osPulseStater{},
			Reader:           reader,
			Yield:            func() {},
			Git:              &modulePulseGitRunner{},
			Prompter:         prompter,
			Presenter:        &recordingPulsePresenter{},
			Now:              time.Now,
		})

		_, err := module.Run(ctx, Input{})
		if !errors.Is(err, context.Canceled) || prompter.dayCalls != 0 {
			t.Fatalf("Run() = (%v, day calls %d)", err, prompter.dayCalls)
		}
	})

	t.Run("before author prompt", func(t *testing.T) {
		root := t.TempDir()
		makePulseDirectory(t, filepath.Join(root, ".git"))
		ctx, cancel := context.WithCancel(context.Background())
		runner := &cancellingAfterPulseLogRunner{root: root, cancel: cancel}
		prompter := &scriptedPulsePrompter{days: 1}
		module := newPulseModuleForTest(t, root, runner, prompter, &recordingPulsePresenter{})

		_, err := module.Run(ctx, Input{})
		if !errors.Is(err, context.Canceled) || prompter.authorPrompt.Message != "" {
			t.Fatalf("Run() = (%v, author prompt %#v)", err, prompter.authorPrompt)
		}
	})
}

func TestModuleRunPreservesTypedSignalErrorFromGitAvailabilityCheck(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signal := pulseFetchExitError{cause: context.Canceled, code: 143}
	module := newPulseModuleForTest(t, root, pulseGitRunnerFunc(func(_ context.Context, _ []string) (GitOutput, error) {
		cancel()
		return GitOutput{}, signal
	}), &scriptedPulsePrompter{}, &recordingPulsePresenter{})

	_, err := module.Run(ctx, Input{})
	var exitOutcome pulseExitCodedError
	if !errors.As(err, &exitOutcome) || exitOutcome.ExitCode() != 143 {
		t.Fatalf("Run() error = %v, want typed signal outcome", err)
	}
}

func newPulseModuleForTest(t *testing.T, root string, runner GitRunner, prompter Prompter, presenter Presenter) *Module {
	t.Helper()
	return newPulseModuleWithDependencies(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Stater:           osPulseStater{},
		Reader:           testPulseDirectoryReader{},
		Yield:            func() {},
		Git:              runner,
		Prompter:         prompter,
		Presenter:        presenter,
		Now: func() time.Time {
			return time.Date(2026, time.August, 23, 14, 0, 0, 0, time.UTC)
		},
	})
}

func newPulseModuleWithDependencies(t *testing.T, dependencies Dependencies) *Module {
	t.Helper()
	if dependencies.Tracker == nil {
		dependencies.Tracker = discardPulseTracker{}
	}
	module, err := New(dependencies)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return module
}

type cancellingAfterPulseLogRunner struct {
	root   string
	cancel context.CancelFunc
}

func (runner *cancellingAfterPulseLogRunner) Run(_ context.Context, arguments []string) (GitOutput, error) {
	if reflect.DeepEqual(arguments, []string{"--version"}) {
		return GitOutput{}, nil
	}
	runner.cancel()
	return GitOutput{Stdout: []byte("Ada\x1f2026-08-23 10:00:00\x1fone\nBen\x1f2026-08-23 09:00:00\x1ftwo")}, nil
}

type pulseGitRunnerFunc func(context.Context, []string) (GitOutput, error)

func (function pulseGitRunnerFunc) Run(context context.Context, arguments []string) (GitOutput, error) {
	return function(context, arguments)
}

type scriptedPulsePrompter struct {
	dayPrompt        DayPrompt
	days             int
	daysCancelled    bool
	daysErr          error
	dayCalls         int
	authorPrompt     AuthorPrompt
	authors          []string
	authorsCancelled bool
	authorsErr       error
}

func (prompter *scriptedPulsePrompter) SelectDays(prompt DayPrompt) (int, bool, error) {
	prompter.dayPrompt = prompt
	prompter.dayCalls++
	return prompter.days, prompter.daysCancelled, prompter.daysErr
}

func (prompter *scriptedPulsePrompter) SelectAuthors(prompt AuthorPrompt) ([]string, bool, error) {
	prompter.authorPrompt = prompt
	return prompter.authors, prompter.authorsCancelled, prompter.authorsErr
}

type modulePulseGitRunner struct {
	mu         sync.Mutex
	version    GitOutput
	versionErr error
	logs       map[string]pulseGitResponse
	calls      [][]string
}

func (runner *modulePulseGitRunner) Run(_ context.Context, arguments []string) (GitOutput, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	if reflect.DeepEqual(arguments, []string{"--version"}) {
		return runner.version, runner.versionErr
	}
	if len(arguments) < 2 {
		return GitOutput{}, fmt.Errorf("unexpected Git arguments: %q", arguments)
	}
	response := runner.logs[arguments[1]]
	return response.output, response.err
}

func (runner *modulePulseGitRunner) callCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return len(runner.calls)
}

func (runner *modulePulseGitRunner) logCallCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	count := 0
	for _, arguments := range runner.calls {
		if !reflect.DeepEqual(arguments, []string{"--version"}) {
			count++
		}
	}
	return count
}

func (runner *modulePulseGitRunner) sinceArguments() []string {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	arguments := make([]string, 0)
	for _, invocation := range runner.calls {
		for _, argument := range invocation {
			if len(argument) >= len("--since=") && argument[:len("--since=")] == "--since=" {
				arguments = append(arguments, argument)
			}
		}
	}
	return arguments
}

type recordingPulsePresenter struct {
	introductions     []string
	scanStarts        int
	foundRepositories int
	repositoriesFound int
	noRepositories    int
	fetchStarts       int
	fetchProgress     int
	noCommits         int
	cancelled         int
	reports           []Report
	dateSelections    []pulseDateSelection
	allAuthorCounts   []int
	scanWarnings      []pulsePathWarning
	fetchWarnings     []pulsePathWarning
}

type pulseDateSelection struct {
	days     int
	explicit bool
	boundary string
}

type pulsePathWarning struct {
	root  string
	paths []string
}

type discardPulseTracker struct{}

func (discardPulseTracker) Start(context.Context, PhaseKind) (PhaseReporter, error) {
	return discardPulsePhaseReporter{}, nil
}

type discardPulsePhaseReporter struct{}

func (discardPulsePhaseReporter) Report(Phase) {}

func (discardPulsePhaseReporter) Close() error { return nil }

type recordingPulseTracker struct {
	started []PhaseKind
	phases  []Phase
	closed  int
}

func (tracker *recordingPulseTracker) Start(_ context.Context, kind PhaseKind) (PhaseReporter, error) {
	tracker.started = append(tracker.started, kind)
	return recordingPulsePhaseReporter{tracker: tracker}, nil
}

type recordingPulsePhaseReporter struct {
	tracker *recordingPulseTracker
}

func (reporter recordingPulsePhaseReporter) Report(phase Phase) {
	reporter.tracker.phases = append(reporter.tracker.phases, phase)
}

func (reporter recordingPulsePhaseReporter) Close() error {
	reporter.tracker.closed++
	return nil
}

func (presenter *recordingPulsePresenter) Introduction(root string) {
	presenter.introductions = append(presenter.introductions, root)
}

func (presenter *recordingPulsePresenter) ScanStarted() {
	presenter.scanStarts++
}

func (presenter *recordingPulsePresenter) RepositoryFound(string, string, int) {
	presenter.foundRepositories++
}

func (presenter *recordingPulsePresenter) RepositoriesFound(count int) {
	presenter.repositoriesFound = count
}

func (presenter *recordingPulsePresenter) NoRepositories() {
	presenter.noRepositories++
}

func (presenter *recordingPulsePresenter) FetchStarted(int) {
	presenter.fetchStarts++
}

func (presenter *recordingPulsePresenter) FetchProgress(string, string, int, int) {
	presenter.fetchProgress++
}

func (presenter *recordingPulsePresenter) NoCommits() {
	presenter.noCommits++
}

func (presenter *recordingPulsePresenter) Cancelled() {
	presenter.cancelled++
}

func (presenter *recordingPulsePresenter) Present(report Report) {
	presenter.reports = append(presenter.reports, report)
}

func (presenter *recordingPulsePresenter) PulseDateSelection(days int, explicit bool, boundary string) {
	presenter.dateSelections = append(presenter.dateSelections, pulseDateSelection{days: days, explicit: explicit, boundary: boundary})
}

func (presenter *recordingPulsePresenter) PulseAuthorFilterAll(authorCount int) {
	presenter.allAuthorCounts = append(presenter.allAuthorCounts, authorCount)
}

func (presenter *recordingPulsePresenter) PulseScanWarning(root string, paths []string) {
	presenter.scanWarnings = append(presenter.scanWarnings, pulsePathWarning{root: root, paths: append([]string(nil), paths...)})
}

func (presenter *recordingPulsePresenter) PulseFetchWarning(root string, paths []string) {
	presenter.fetchWarnings = append(presenter.fetchWarnings, pulsePathWarning{root: root, paths: append([]string(nil), paths...)})
}
