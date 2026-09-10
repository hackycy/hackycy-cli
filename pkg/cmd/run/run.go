package run

import (
	"context"
	"errors"
	"path/filepath"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

// Dependencies are the command-owned boundaries required by run.
type Dependencies struct {
	WorkingDirectory func() (string, error)
	Reader           FileReader
	Exists           FileExists
	Prompter         Prompter
	Runner           ChildRunner
	Presenter        Presenter
}

// Module owns run behavior behind its typed command interface.
type Module struct {
	workingDirectory func() (string, error)
	reader           FileReader
	exists           FileExists
	prompter         Prompter
	runner           ChildRunner
	presenter        Presenter
}

// New constructs a run command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.WorkingDirectory == nil {
		return nil, errors.New("run working directory is required")
	}
	if dependencies.Reader == nil {
		return nil, errors.New("run package reader is required")
	}
	if dependencies.Exists == nil {
		return nil, errors.New("run path existence checker is required")
	}
	if dependencies.Prompter == nil {
		return nil, errors.New("run prompter is required")
	}
	if dependencies.Runner == nil {
		return nil, errors.New("run child runner is required")
	}
	if dependencies.Presenter == nil {
		return nil, errors.New("run presenter is required")
	}
	return &Module{
		workingDirectory: dependencies.WorkingDirectory,
		reader:           dependencies.Reader,
		exists:           dependencies.Exists,
		prompter:         dependencies.Prompter,
		runner:           dependencies.Runner,
		presenter:        dependencies.Presenter,
	}, nil
}

// Run executes one package script selection without owning process exit behavior.
func (module *Module) Run(context context.Context, input Input) (Result, error) {
	if err := context.Err(); err != nil {
		return Result{}, err
	}
	workingDirectory, err := module.workingDirectory()
	if err != nil {
		return Result{}, err
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		return Result{}, err
	}
	observer := detailedRunObserver(module.presenter)
	reportRunPhase(observer, runResolveProjectPhaseID, terminalexperience.PhaseActive, "Discovering package.json and runnable scripts")
	discovery, err := DiscoverProject(workingDirectory, input.Directory, module.reader)
	if err != nil {
		reportRunPhase(observer, runResolveProjectPhaseID, runPhaseError(context, err), "Project discovery failed")
		return Result{}, err
	}
	reportRunPhase(observer, runResolveProjectPhaseID, terminalexperience.PhaseCompleted, runProjectDetail(discovery))

	presentIntroduction(module.presenter)
	script, cancelled, err := selectScript(module.prompter, discovery.Scripts)
	if err != nil {
		return Result{}, err
	}
	if cancelled {
		reportRunMilestone(observer, "Script selection cancelled")
		presentCancellation(module.presenter)
		return Result{}, nil
	}
	if err := context.Err(); err != nil {
		return Result{}, err
	}
	reportRunPhase(observer, runResolveManagerPhaseID, terminalexperience.PhaseActive, "Inspecting lockfiles")
	managers, err := PackageManagerOrder(discovery.Directory, module.exists)
	if err != nil {
		reportRunPhase(observer, runResolveManagerPhaseID, runPhaseError(context, err), "Package manager resolution failed")
		return Result{}, err
	}
	reportRunPhase(observer, runResolveManagerPhaseID, terminalexperience.PhaseCompleted, runManagerDetail(managers))
	manager, cancelled, err := selectPackageManager(module.prompter, managers)
	if err != nil {
		return Result{}, err
	}
	if cancelled {
		reportRunMilestone(observer, "Package manager selection cancelled")
		presentCancellation(module.presenter)
		return Result{}, nil
	}
	if err := context.Err(); err != nil {
		return Result{}, err
	}
	request := childRequest(discovery.Directory, manager, script)
	reportRunPhase(observer, runPrepareCommandPhaseID, terminalexperience.PhaseActive, runChildDetail(request))
	presentLaunch(module.presenter, request)
	reportRunPhase(observer, runPrepareCommandPhaseID, terminalexperience.PhaseCompleted, runChildDetail(request))
	return module.runner.Run(context, request)
}
