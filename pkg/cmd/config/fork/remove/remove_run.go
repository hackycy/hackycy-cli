package remove

import (
	"context"
	"errors"
)

// RemoveRequest is the typed CLI request for an operand-free config fork remove command.
type RemoveRequest struct{}

// RemoveResult records a successful non-mutating remove outcome.
type RemoveResult struct {
	Empty     bool
	Cancelled bool
	Declined  bool
}

// RemoveInteraction owns the selection and confirmation prompts for Fork removal.
type RemoveInteraction interface {
	RemovePrompter
	RemoveConfirmationPrompter
}

// RemoveDependencies are the command-owned adapters for config fork remove.
type RemoveDependencies struct {
	Reader    RemoveReader
	Prompter  RemoveInteraction
	Writer    RemoveWriter
	Presenter RemovePresenter
}

// RemoveModule owns config fork remove behavior behind its typed command interface.
type RemoveModule struct {
	reader    RemoveReader
	prompter  RemoveInteraction
	writer    RemoveWriter
	presenter RemovePresenter
}

// NewRemove constructs a config fork remove command module.
func NewRemove(dependencies RemoveDependencies) (*RemoveModule, error) {
	if dependencies.Reader == nil {
		return nil, errors.New("config fork remove reader is required")
	}
	if dependencies.Prompter == nil {
		return nil, errors.New("config fork remove prompter is required")
	}
	if dependencies.Writer == nil {
		return nil, errors.New("config fork remove writer is required")
	}
	if dependencies.Presenter == nil {
		return nil, errors.New("config fork remove presenter is required")
	}
	return &RemoveModule{
		reader:    dependencies.Reader,
		prompter:  dependencies.Prompter,
		writer:    dependencies.Writer,
		presenter: dependencies.Presenter,
	}, nil
}

// Run selects, confirms, removes, and presents one Fork removal outcome.
func (module *RemoveModule) Run(_ context.Context, _ RemoveRequest) (RemoveResult, error) {
	selection, err := SelectRemove(module.reader, module.prompter)
	if err != nil {
		return RemoveResult{}, err
	}
	if selection.Empty {
		PresentRemoveEmpty(module.presenter)
		return RemoveResult{Empty: true}, nil
	}
	if selection.Cancelled {
		PresentRemoveCancellation(module.presenter)
		return RemoveResult{Cancelled: true}, nil
	}

	confirmation, err := ConfirmRemove(selection.Name, module.prompter)
	if err != nil {
		return RemoveResult{}, err
	}
	switch confirmation {
	case RemoveConfirmationCancelled:
		PresentRemoveCancellation(module.presenter)
		return RemoveResult{Cancelled: true}, nil
	case RemoveDeclined:
		PresentRemoveCancellation(module.presenter)
		return RemoveResult{Declined: true}, nil
	}

	if _, err := RemoveSelected(module.writer, selection.Name); err != nil {
		return RemoveResult{}, err
	}
	PresentRemoveSuccess(module.presenter, selection.Name)
	return RemoveResult{}, nil
}
