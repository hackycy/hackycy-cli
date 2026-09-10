package rm

import (
	"context"
	"errors"
	"path/filepath"
)

// Input is the typed request for rm.
type Input struct {
	Paths []string
	Force bool
	Depth *int
}

// Result records the successful non-process outcome of rm.
type Result struct{}

// Dependencies are the command-owned external boundaries for rm.
type Dependencies struct {
	WorkingDirectory func() (string, error)
	Prompter         Prompter
	Remover          PathRemover
	Presenter        Presenter
}

// Module owns rm behavior behind its typed command interface.
type Module struct {
	workingDirectory func() (string, error)
	prompter         Prompter
	remover          PathRemover
	presenter        Presenter
}

// New constructs an rm command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.WorkingDirectory == nil {
		return nil, errors.New("rm working directory is required")
	}
	if dependencies.Prompter == nil {
		return nil, errors.New("rm prompter is required")
	}
	if dependencies.Remover == nil {
		return nil, errors.New("rm remover is required")
	}
	if dependencies.Presenter == nil {
		return nil, errors.New("rm presenter is required")
	}
	return &Module{
		workingDirectory: dependencies.WorkingDirectory,
		prompter:         dependencies.Prompter,
		remover:          dependencies.Remover,
		presenter:        dependencies.Presenter,
	}, nil
}

// Run executes one rm request without owning process exit behavior.
func (module *Module) Run(context context.Context, input Input) (Result, error) {
	if err := context.Err(); err != nil {
		return Result{}, err
	}
	presentIntroduction(module.presenter)
	workingDirectory, err := module.workingDirectory()
	if err != nil {
		return Result{}, err
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		return Result{}, err
	}
	if len(input.Paths) > 0 {
		return module.runExplicit(context, workingDirectory, input)
	}
	return module.runSmart(context, workingDirectory, input)
}

func (module *Module) runExplicit(context context.Context, workingDirectory string, input Input) (Result, error) {
	plan, err := planExplicit(workingDirectory, input.Paths)
	if err != nil {
		return Result{}, err
	}
	presentMissingPaths(module.presenter, plan.missing)
	if len(plan.existing) == 0 {
		presentNoValidPaths(module.presenter)
		return Result{}, nil
	}
	if !input.Force {
		presentExplicitPaths(module.presenter, plan.existing)
	}
	targets, cancelled, err := selectExplicitTargets(plan.existing, input.Force, module.prompter)
	if err != nil {
		return Result{}, err
	}
	if cancelled {
		presentCancellation(module.presenter)
		return Result{}, nil
	}
	if err := context.Err(); err != nil {
		return Result{}, err
	}
	presentDeleteStart(module.presenter, len(targets))
	presentDeleteResult(module.presenter, deletePaths(targets, module.remover))
	return Result{}, nil
}

func (module *Module) runSmart(context context.Context, workingDirectory string, input Input) (Result, error) {
	action, cancelled, err := selectSmartAction(module.prompter)
	if err != nil {
		return Result{}, err
	}
	if cancelled {
		presentCancellation(module.presenter)
		return Result{}, nil
	}
	if err := context.Err(); err != nil {
		return Result{}, err
	}
	presentScanStart(module.presenter)
	targets := discoverSmart(workingDirectory, action, resolvedSmartDepth(input.Depth))
	presentScanStop(module.presenter, len(targets))
	if len(targets) == 0 {
		presentNothingToClean(module.presenter)
		return Result{}, nil
	}
	selected, cancelled, err := selectSmartTargets(workingDirectory, targets, input.Force, module.prompter)
	if err != nil {
		return Result{}, err
	}
	if cancelled {
		presentCancellation(module.presenter)
		return Result{}, nil
	}
	if len(selected) == 0 {
		presentNothingSelected(module.presenter)
		return Result{}, nil
	}
	if err := context.Err(); err != nil {
		return Result{}, err
	}
	presentDeleteStart(module.presenter, len(selected))
	presentDeleteResult(module.presenter, deletePaths(selected, module.remover))
	return Result{}, nil
}

func resolvedSmartDepth(depth *int) int {
	if depth == nil {
		return 5
	}
	return *depth
}
