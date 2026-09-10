package cm

import "errors"

// executionMode is the command-owned interpretation of the legacy flag matrix.
type executionMode struct {
	Scope        Scope
	PromptStage  bool
	StageAll     bool
	CreateCommit bool
	Push         bool
	PushRemote   string
	Interactive  bool
}

func resolveExecutionMode(input Input) (executionMode, error) {
	stagePush := truthyOptional(input.StagePush)
	push := truthyOptional(input.Push)
	if (input.Stage || stagePush) && input.StageAll {
		return executionMode{}, errors.New("Use either --stage/--stage-push or --stage-all, not both.")
	}
	if (input.Stage || stagePush) && input.DryRun {
		return executionMode{}, errors.New("Use either --stage/--stage-push or --dry-run, not both.")
	}
	pushRemote, hasPush := firstTruthyOptional(input.StagePush, input.Push)
	if hasPush && input.DryRun {
		return executionMode{}, errors.New("Use either --push/--stage-push or --dry-run, not both.")
	}
	if push && !input.Stage && !input.Staged && !input.StageAll && !stagePush {
		return executionMode{}, errors.New("Use --push with --stage, --staged, or --stage-all.")
	}

	promptStage := input.Stage || stagePush
	stageAll := input.StageAll && !input.DryRun
	stagedOnly := input.Staged || promptStage || stageAll
	mode := executionMode{
		Scope:        ScopeAllUncommitted,
		PromptStage:  promptStage,
		StageAll:     stageAll,
		CreateCommit: stagedOnly && !input.DryRun,
		Interactive:  input.Stage || stagePush || input.Staged || input.StageAll || push,
	}
	if stagedOnly {
		mode.Scope = ScopeStaged
	}
	if hasPush && mode.CreateCommit {
		mode.Push = true
		mode.PushRemote = pushRemote
	}
	return mode, nil
}

// RequiresInteraction reports whether the established flag combination reaches
// file selection or commit confirmation.
func RequiresInteraction(input Input) (bool, error) {
	mode, err := resolveExecutionMode(input)
	if err != nil {
		return false, err
	}
	return mode.PromptStage || mode.CreateCommit, nil
}

func truthyOptional(value *string) bool {
	return value != nil && *value != ""
}

func firstTruthyOptional(values ...*string) (string, bool) {
	for _, value := range values {
		if truthyOptional(value) {
			return *value, true
		}
	}
	return "", false
}
