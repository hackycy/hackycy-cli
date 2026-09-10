package remove

import (
	"context"
	"errors"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

var errConfigCMRemoveRequiresInteractive = errors.New("config cm remove requires an interactive terminal")

type terminalCMRemoveAdapter struct {
	run terminalexperience.ExperienceRun
}

func newTerminalCMRemoveAdapter(run terminalexperience.ExperienceRun) *terminalCMRemoveAdapter {
	return &terminalCMRemoveAdapter{run: run}
}

func (adapter *terminalCMRemoveAdapter) Confirm(question RemoveConfirmPrompt) (bool, bool, error) {
	answer, err := adapter.run.Ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionConfirm,
		Message:         question.Message,
		Description:     question.Description,
		ConsoleStepID:   cmRemoveConfirmationFormID,
		TranscriptLabel: "CM removal confirmation",
		HasDefault:      true,
		Default:         terminalexperience.InteractionAnswer{Confirmed: false},
		TranscriptProject: func(terminalexperience.InteractionAnswer) string {
			return ""
		},
	})
	if errors.Is(err, context.Canceled) {
		return false, false, err
	}
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) {
		return false, true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return false, false, errConfigCMRemoveRequiresInteractive
	}
	if err != nil {
		return false, false, err
	}
	return answer.Confirmed, false, nil
}

func (adapter *terminalCMRemoveAdapter) Cancel(message string) {
	_ = adapter.run.Result(terminalCMRemoveDocument(message, true))
}
func (adapter *terminalCMRemoveAdapter) Success(message string) {
	_ = adapter.run.Result(terminalCMRemoveDocument(message, false))
}

func terminalCMRemoveDocument(message string, cancelled bool) terminalexperience.PresentationDocument {
	role := terminalexperience.VisualRoleSuccess
	if cancelled {
		role = terminalexperience.VisualRoleWarning
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: message}}}
}
