package heat

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type workPhaseState uint8

const (
	workPhaseActive workPhaseState = iota
	workPhaseCompleted
	workPhaseCancelled
	workPhaseFailed
)

type workPhaseUpdate struct {
	ID     string
	Name   string
	Detail string
	State  workPhaseState
}

func heatSortLabel(sort Sort) string {
	if sort == SortCount {
		return "change count"
	}
	return "path"
}

// Dependencies are the command-owned boundaries required by git heat.
type Dependencies struct {
	Git GitRunner
	Now func() time.Time
}

// Result records one completed git heat command outcome.
type Result struct {
	Report Report
	Now    time.Time
}

// Module owns git heat behavior behind its typed command interface.
type Module struct {
	git GitRunner
	now func() time.Time
}

// New constructs a git heat command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.Git == nil {
		return nil, errors.New("git heat runner is required")
	}
	if dependencies.Now == nil {
		return nil, errors.New("git heat clock is required")
	}
	return &Module{
		git: dependencies.Git,
		now: dependencies.Now,
	}, nil
}

// Run executes git heat without owning process exit behavior.
func (module *Module) Run(ctx context.Context, input Input) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	options, err := NormalizeInput(input)
	if err != nil {
		return Result{}, err
	}
	return module.runNormalized(ctx, options, nil)
}

func (module *Module) runNormalized(ctx context.Context, options NormalizedInput, reportUpdate func(workPhaseUpdate)) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	const (
		locateID = "locate-repository"
		readID   = "read-git-history"
		rankID   = "rank-hot-paths"
	)
	phase := func(update workPhaseUpdate) {
		if reportUpdate != nil {
			reportUpdate(update)
		}
	}
	failPhase := func(id, name, detail string, err error) (Result, error) {
		state := workPhaseFailed
		if isHeatCancellation(err) {
			state = workPhaseCancelled
		}
		phase(workPhaseUpdate{ID: id, Name: name, Detail: detail, State: state})
		return Result{}, err
	}

	phase(workPhaseUpdate{ID: locateID, Name: "Locate Git repository", Detail: "Locating repository", State: workPhaseActive})
	repositoryRoot, err := DiscoverRepository(ctx, module.git)
	if err != nil {
		return failPhase(locateID, "Locate Git repository", "Unable to locate Git repository", err)
	}
	if err := ctx.Err(); err != nil {
		return failPhase(locateID, "Locate Git repository", "Cancelled while locating repository", err)
	}
	phase(workPhaseUpdate{ID: locateID, Name: "Locate Git repository", Detail: "Repository located", State: workPhaseCompleted})

	phase(workPhaseUpdate{ID: readID, Name: "Read Git history", Detail: "Reading " + rangeLabel(options.Range), State: workPhaseActive})
	log, err := ReadLog(ctx, module.git, repositoryRoot, options.Range)
	if err != nil {
		return failPhase(readID, "Read Git history", "Unable to read Git history", err)
	}
	if err := ctx.Err(); err != nil {
		return failPhase(readID, "Read Git history", "Cancelled while reading Git history", err)
	}
	phase(workPhaseUpdate{ID: readID, Name: "Read Git history", Detail: fmt.Sprintf("Read %d commits", log.CommitCount), State: workPhaseCompleted})

	targetLabel := "files"
	if options.Target == TargetDirectories {
		targetLabel = "directories"
	}
	sortLabel := heatSortLabel(options.Sort)
	phase(workPhaseUpdate{ID: rankID, Name: "Rank hot paths", Detail: "Ranking " + targetLabel + " by " + sortLabel, State: workPhaseActive})
	report := BuildReport(repositoryRoot, options, log)
	if err := ctx.Err(); err != nil {
		return failPhase(rankID, "Rank hot paths", "Cancelled while ranking hot paths", err)
	}
	now := module.now()
	count := len(report.Rows())
	phase(workPhaseUpdate{ID: rankID, Name: "Rank hot paths", Detail: fmt.Sprintf("Ranked %d %s", count, targetLabel), State: workPhaseCompleted})
	return Result{Report: report, Now: now}, nil
}
