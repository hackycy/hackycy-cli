package cm

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
)

// StageOption is one displayed Git status entry in the legacy selection prompt.
type StageOption struct {
	Value string
	Label string
}

// StagePrompt describes the select-all-by-default staging prompt.
type StagePrompt struct {
	Message       string
	Options       []StageOption
	InitialValues []string
}

// StagePrompter owns the interactive file selection boundary.
type StagePrompter interface {
	SelectFiles(StagePrompt) (selected []string, cancelled bool, err error)
}

// StageResult records the observable stage-selection outcome.
type StageResult struct {
	RepositoryRoot  string
	NoChanges       bool
	Cancelled       bool
	NothingSelected bool
}

// StageSelectedChanges collects every uncommitted path, prompts once, then rewrites the index.
func StageSelectedChanges(ctx context.Context, runner GitRunner, fileSystem SnapshotFileSystem, prompter StagePrompter) (StageResult, error) {
	if fileSystem == nil {
		return StageResult{}, errors.New("Git staging filesystem is required")
	}
	if prompter == nil {
		return StageResult{}, errors.New("Git staging prompt is required")
	}
	plan, err := prepareStage(ctx, runner)
	if err != nil {
		return StageResult{}, err
	}
	result := StageResult{RepositoryRoot: plan.state.Root}
	if len(plan.state.Files) == 0 {
		result.NoChanges = true
		return result, nil
	}
	selected, cancelled, err := prompter.SelectFiles(plan.prompt)
	if err != nil {
		return result, err
	}
	if cancelled {
		result.Cancelled = true
		return result, nil
	}
	return applyStagePlan(ctx, runner, fileSystem, plan, selected)
}

type stagePlan struct {
	state  RepositoryState
	prompt StagePrompt
}

func prepareStage(ctx context.Context, runner GitRunner) (stagePlan, error) {
	state, err := InspectRepository(ctx, runner, ScopeAllUncommitted)
	if err != nil {
		return stagePlan{}, err
	}
	plan := stagePlan{state: state}
	if len(state.Files) == 0 {
		return plan, nil
	}
	plan.prompt = StagePrompt{
		Message:       "Select files to stage",
		Options:       make([]StageOption, 0, len(state.Files)),
		InitialValues: make([]string, 0, len(state.Files)),
	}
	for _, file := range state.Files {
		plan.prompt.Options = append(plan.prompt.Options, StageOption{Value: file.Path, Label: file.Status})
		plan.prompt.InitialValues = append(plan.prompt.InitialValues, file.Path)
	}
	return plan, nil
}

func applyStagePlan(ctx context.Context, runner GitRunner, fileSystem SnapshotFileSystem, plan stagePlan, selected []string) (StageResult, error) {
	result := StageResult{RepositoryRoot: plan.state.Root}
	if len(selected) == 0 {
		result.NothingSelected = true
		return result, nil
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, filePath := range selected {
		selectedSet[filePath] = struct{}{}
	}
	unselected := make([]string, 0)
	for _, file := range plan.state.Files {
		if file.IndexStatus == '?' {
			continue
		}
		if _, found := selectedSet[file.Path]; !found {
			unselected = append(unselected, file.Path)
		}
	}
	if len(unselected) > 0 {
		if err := runGitMutation(ctx, runner, []string{"-C", plan.state.Root, "restore", "--staged", "--"}, unselected, "git restore --staged failed"); err != nil {
			return StageResult{}, err
		}
	}
	if err := stageFilePaths(ctx, runner, fileSystem, plan.state.Root, selected); err != nil {
		return StageResult{}, err
	}
	return result, nil
}

// StageAllChanges reproduces the legacy bulk index mutation.
func StageAllChanges(ctx context.Context, runner GitRunner) (string, error) {
	root, err := discoverRepository(ctx, runner)
	if err != nil {
		return "", captureError(err)
	}
	if err := runGitMutation(ctx, runner, []string{"-C", root, "add", "-A"}, nil, "git add -A failed"); err != nil {
		return "", err
	}
	return root, nil
}

func stageFilePaths(ctx context.Context, runner GitRunner, fileSystem SnapshotFileSystem, root string, filePaths []string) error {
	existing := make([]string, 0, len(filePaths))
	missing := make([]string, 0, len(filePaths))
	for _, filePath := range filePaths {
		_, err := fileSystem.Lstat(filepath.Join(root, filePath))
		if err == nil {
			existing = append(existing, filePath)
			continue
		}
		if errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, filePath)
			continue
		}
		return err
	}
	if len(existing) > 0 {
		if err := runGitMutation(ctx, runner, []string{"-C", root, "add", "-A", "--"}, existing, "git add failed"); err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		if err := runGitMutation(ctx, runner, []string{"-C", root, "update-index", "--remove", "--"}, missing, "git update-index --remove failed"); err != nil {
			return err
		}
	}
	return nil
}

func runGitMutation(ctx context.Context, runner GitRunner, prefix, paths []string, fallback string) error {
	arguments := append(append([]string(nil), prefix...), paths...)
	output, err := runner.Run(ctx, arguments)
	if err != nil {
		return err
	}
	if output.ExitCode != 0 {
		return gitOutputError(output, fallback)
	}
	return nil
}
