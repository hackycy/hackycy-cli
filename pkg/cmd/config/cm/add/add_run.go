package add

import (
	"context"
	"errors"
)

// AddRequest is the typed CLI request for an operand-free config cm add command.
type AddRequest struct{}

// AddResult records a successful add outcome or an interactive cancellation.
type AddResult struct {
	Cancelled bool
}

// AddDependencies are the command-owned adapters for config cm add.
type AddDependencies struct {
	Prompter  AddPrompter
	Writer    AddWriter
	Presenter AddPresenter
}

// AddModule owns config cm add behavior behind its typed command interface.
type AddModule struct {
	prompter  AddPrompter
	writer    AddWriter
	presenter AddPresenter
}

// NewAdd constructs a config cm add command module.
func NewAdd(dependencies AddDependencies) (*AddModule, error) {
	if dependencies.Prompter == nil {
		return nil, errors.New("config cm add prompter is required")
	}
	if dependencies.Writer == nil {
		return nil, errors.New("config cm add writer is required")
	}
	if dependencies.Presenter == nil {
		return nil, errors.New("config cm add presenter is required")
	}
	return &AddModule{
		prompter:  dependencies.Prompter,
		writer:    dependencies.Writer,
		presenter: dependencies.Presenter,
	}, nil
}

// Run prompts, persists, and presents one config cm add outcome.
func (module *AddModule) Run(_ context.Context, _ AddRequest) (AddResult, error) {
	input, cancelled, err := PromptAdd(module.prompter)
	if err != nil {
		return AddResult{}, err
	}
	if cancelled {
		PresentAddCancellation(module.presenter)
		return AddResult{Cancelled: true}, nil
	}
	if err := SaveAdd(module.writer, input); err != nil {
		return AddResult{}, err
	}
	PresentAddSuccess(module.presenter, input)
	return AddResult{}, nil
}
