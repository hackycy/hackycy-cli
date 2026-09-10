package pulse

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var errPulseGitUnavailable = errors.New("Git is not installed or not available in the system PATH.")

// Presenter owns git pulse's semantic terminal events.
type Presenter interface {
	Introduction(string)
	RepositoriesFound(int)
	NoRepositories()
	NoCommits()
	Cancelled()
	Present(Report)
}

// pulseDetailPresenter is deliberately optional so the module's established
// presenter contract stays compatible for non-terminal callers.
type pulseDetailPresenter interface {
	PulseDateSelection(days int, explicit bool, boundary string)
	PulseAuthorFilterAll(authorCount int)
	PulseScanWarning(root string, paths []string)
	PulseFetchWarning(root string, paths []string)
}

// Dependencies are the command-owned boundaries required by git pulse.
type Dependencies struct {
	WorkingDirectory func() (string, error)
	Stater           PathStater
	Reader           DirectoryReader
	Yield            func()
	Git              GitRunner
	Prompter         Prompter
	Presenter        Presenter
	Tracker          Tracker
	Now              func() time.Time
}

// Result records one completed git pulse command outcome.
type Result struct {
	Report             Report
	FailedRepositories int
}

// Module owns git pulse behavior behind its typed command interface.
type Module struct {
	workingDirectory func() (string, error)
	stater           PathStater
	reader           DirectoryReader
	yield            func()
	git              GitRunner
	prompter         Prompter
	presenter        Presenter
	tracker          Tracker
	now              func() time.Time
}

// New constructs a git pulse command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.WorkingDirectory == nil {
		return nil, errors.New("git pulse working directory is required")
	}
	if dependencies.Stater == nil {
		return nil, errors.New("git pulse path stater is required")
	}
	if dependencies.Reader == nil {
		return nil, errors.New("git pulse directory reader is required")
	}
	if dependencies.Yield == nil {
		return nil, errors.New("git pulse scheduler yield is required")
	}
	if dependencies.Git == nil {
		return nil, errors.New("git pulse runner is required")
	}
	if dependencies.Prompter == nil {
		return nil, errors.New("git pulse prompter is required")
	}
	if dependencies.Presenter == nil {
		return nil, errors.New("git pulse presenter is required")
	}
	if dependencies.Tracker == nil {
		return nil, errors.New("git pulse tracker is required")
	}
	if dependencies.Now == nil {
		return nil, errors.New("git pulse clock is required")
	}
	return &Module{
		workingDirectory: dependencies.WorkingDirectory,
		stater:           dependencies.Stater,
		reader:           dependencies.Reader,
		yield:            dependencies.Yield,
		git:              dependencies.Git,
		prompter:         dependencies.Prompter,
		presenter:        dependencies.Presenter,
		tracker:          dependencies.Tracker,
		now:              dependencies.Now,
	}, nil
}

// Run executes git pulse without owning process exit behavior.
func (module *Module) Run(ctx context.Context, input Input) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	var root string
	err := module.track(ctx, Phase{Kind: PhasePrepare, State: PhaseActive, Detail: "Checking workspace and Git"}, func(report func(Phase)) error {
		workingDirectory, err := module.workingDirectory()
		if err != nil {
			return err
		}
		root, err = resolvePulseRoot(workingDirectory, input.Directory, module.stater)
		if err != nil {
			return err
		}
		report(Phase{Kind: PhasePrepare, State: PhaseActive, Root: root, Detail: "Checking workspace and Git"})
		if err := verifyPulseGit(ctx, module.git); err != nil {
			return err
		}
		return ctx.Err()
	})
	if err != nil {
		return Result{}, err
	}

	module.presenter.Introduction(root)
	found := 0
	var scan ScanRepositoryResult
	err = module.track(ctx, Phase{Kind: PhaseScan, State: PhaseActive, Root: root, Detail: "Found 0 repositories"}, func(report func(Phase)) error {
		var scanErr error
		scan, scanErr = ScanRepositoryDetails(ctx, root, module.reader, func(repository string) {
			found++
			report(Phase{Kind: PhaseScan, State: PhaseActive, Root: root, Repository: repository, Completed: found, RepositoryCount: found, Detail: fmt.Sprintf("Found %d %s", found, pulsePlural(found, "repository", "repositories"))})
		}, module.yield)
		if scanErr == nil {
			report(Phase{Kind: PhaseScan, State: PhaseActive, Root: root, Completed: len(scan.Repositories), RepositoryCount: len(scan.Repositories), Detail: fmt.Sprintf("Found %d %s", len(scan.Repositories), pulsePlural(len(scan.Repositories), "repository", "repositories"))})
		}
		return scanErr
	})
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if details, ok := module.presenter.(pulseDetailPresenter); ok && len(scan.UnreadableDirectories) > 0 {
		details.PulseScanWarning(root, append([]string(nil), scan.UnreadableDirectories...))
	}
	if len(scan.Repositories) == 0 {
		module.presenter.NoRepositories()
		return Result{}, nil
	}
	module.presenter.RepositoriesFound(len(scan.Repositories))

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	days, cancelled, err := selectDays(input, module.prompter)
	if err != nil {
		return Result{}, err
	}
	if cancelled {
		module.presenter.Cancelled()
		return Result{}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	boundary := SinceBoundary(module.now(), days)
	if details, ok := module.presenter.(pulseDetailPresenter); ok {
		details.PulseDateSelection(days, input.Days != nil, boundary)
	}

	var fetched FetchResult
	err = module.track(ctx, Phase{Kind: PhaseFetch, State: PhaseActive, Root: root, Total: len(scan.Repositories), Detail: fmt.Sprintf("[0/%d] Reading repositories", len(scan.Repositories))}, func(report func(Phase)) error {
		var fetchErr error
		fetched, fetchErr = FetchCommits(ctx, module.git, scan.Repositories, boundary, func(repository string, done int) {
			report(Phase{Kind: PhaseFetch, State: PhaseActive, Root: root, Repository: repository, Completed: done, Total: len(scan.Repositories), Detail: fmt.Sprintf("[%d/%d] Reading %s", done, len(scan.Repositories), pulseRelativePath(root, repository))})
		})
		if fetchErr == nil {
			successful := len(scan.Repositories) - fetched.FailedRepositories
			report(Phase{Kind: PhaseFetch, State: PhaseActive, Root: root, Completed: len(scan.Repositories), Total: len(scan.Repositories), Successful: successful, Detail: fmt.Sprintf("Read %d of %d repositories", successful, len(scan.Repositories))})
		}
		return fetchErr
	})
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if details, ok := module.presenter.(pulseDetailPresenter); ok && len(fetched.FailedRepositoryPaths) > 0 {
		details.PulseFetchWarning(root, append([]string(nil), fetched.FailedRepositoryPaths...))
	}
	if len(fetched.Commits) == 0 {
		module.presenter.NoCommits()
		return Result{FailedRepositories: fetched.FailedRepositories}, nil
	}

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	authors := pulseAuthors(fetched.Commits)
	commits, cancelled, err := SelectAuthors(fetched.Commits, module.prompter)
	if err != nil {
		return Result{}, err
	}
	if cancelled {
		module.presenter.Cancelled()
		return Result{FailedRepositories: fetched.FailedRepositories}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(authors) <= 1 {
		if details, ok := module.presenter.(pulseDetailPresenter); ok {
			details.PulseAuthorFilterAll(len(authors))
		}
	}

	var report Report
	err = module.track(ctx, Phase{Kind: PhaseBuild, State: PhaseActive, Root: root, Detail: "Grouping commits by repository"}, func(phaseReport func(Phase)) error {
		report = BuildReport(commits)
		if err := ctx.Err(); err != nil {
			return err
		}
		phaseReport(Phase{Kind: PhaseBuild, State: PhaseActive, Root: root, CommitCount: report.CommitCount, RepositoryCount: len(report.Repositories), Detail: fmt.Sprintf("Built report with %d commits in %d repositories", report.CommitCount, len(report.Repositories))})
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	module.presenter.Present(report)
	return Result{Report: report, FailedRepositories: fetched.FailedRepositories}, nil
}

func verifyPulseGit(ctx context.Context, runner GitRunner) error {
	output, err := runner.Run(ctx, []string{"--version"})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return err
		}
		return errPulseGitUnavailable
	}
	if output.ExitCode != 0 {
		return errPulseGitUnavailable
	}
	return nil
}
