package remove

import (
	"context"
	"errors"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

var errConfigForkRemoveRequiresInteractive = errors.New("config fork remove requires an interactive terminal")

type terminalForkRemoveAdapter struct {
	run terminalexperience.ExperienceRun
}

func newTerminalForkRemoveAdapter(run terminalexperience.ExperienceRun) *terminalForkRemoveAdapter {
	return &terminalForkRemoveAdapter{run: run}
}

func (adapter *terminalForkRemoveAdapter) Select(question SelectPrompt) (string, bool, error) {
	request := terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionSelect,
		Message:         question.Message,
		ConsoleStepID:   forkRemoveSelectionFormID,
		TranscriptLabel: "Selected instance",
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string {
			return safeForkRemoveName(answer.Value)
		},
		Options: interactionOptions(question.Choices),
	}
	if len(question.Choices) > 0 {
		request.HasDefault = true
		request.Default = terminalexperience.InteractionAnswer{Value: question.Choices[0].Value}
	}
	answer, cancelled, err := adapter.ask(request)
	return answer.Value, cancelled, err
}

func (adapter *terminalForkRemoveAdapter) Confirm(question ConfirmPrompt) (bool, bool, error) {
	answer, cancelled, err := adapter.ask(terminalexperience.InteractionRequest{
		Kind:          terminalexperience.InteractionConfirm,
		Message:       question.Message,
		ConsoleStepID: forkRemoveConfirmationFormID,
		HasDefault:    true,
		Default:       terminalexperience.InteractionAnswer{Confirmed: false},
		// The command emits the semantic confirmation milestone after it can
		// associate the answer with the selected safe instance projection.
		TranscriptProject: func(terminalexperience.InteractionAnswer) string { return "" },
	})
	return answer.Confirmed, cancelled, err
}

func (adapter *terminalForkRemoveAdapter) Info(message string) {
	_ = adapter.run.Notice(terminalForkRemoveDocument(message, terminalexperience.VisualRoleMuted))
}

func (adapter *terminalForkRemoveAdapter) Outcome(message string) {
	role := terminalexperience.VisualRoleSuccess
	if message == "Cancelled" {
		role = terminalexperience.VisualRoleWarning
	}
	_ = adapter.run.Result(terminalForkRemoveDocument(message, role))
}

func (adapter *terminalForkRemoveAdapter) ask(request terminalexperience.InteractionRequest) (terminalexperience.InteractionAnswer, bool, error) {
	answer, err := adapter.run.Ask(request)
	if errors.Is(err, context.Canceled) {
		return terminalexperience.InteractionAnswer{}, false, err
	}
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) {
		return terminalexperience.InteractionAnswer{}, true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return terminalexperience.InteractionAnswer{}, false, errConfigForkRemoveRequiresInteractive
	}
	if err != nil {
		return terminalexperience.InteractionAnswer{}, false, err
	}
	return answer, false, nil
}

func interactionOptions(choices []Choice) []terminalexperience.InteractionOption {
	options := make([]terminalexperience.InteractionOption, 0, len(choices))
	for _, choice := range choices {
		options = append(options, terminalexperience.InteractionOption{Label: choice.Label, Value: choice.Value, Description: choice.Description})
	}
	return options
}

func terminalForkRemoveDocument(message string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: message}}}
}
