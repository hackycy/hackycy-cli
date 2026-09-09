package add

import (
	"context"
	"errors"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

type terminalCMAddAdapter struct {
	run terminalexperience.ExperienceRun
}

func newTerminalCMAddAdapter(run terminalexperience.ExperienceRun) *terminalCMAddAdapter {
	return &terminalCMAddAdapter{run: run}
}

func (adapter *terminalCMAddAdapter) Text(question AddTextPrompt) (string, bool, error) {
	return adapter.ask(terminalexperience.InteractionRequest{Kind: terminalexperience.InteractionText, Message: question.Message, Placeholder: question.Placeholder, ConsoleStepID: cmAddTextFormID(question.Message), TranscriptLabel: question.Message, TranscriptProject: cmAddTranscriptProject(question.Message), Validate: func(answer terminalexperience.InteractionAnswer) error { return question.Validate(answer.Value) }})
}

func (adapter *terminalCMAddAdapter) Password(question AddTextPrompt) (string, bool, error) {
	return adapter.ask(terminalexperience.InteractionRequest{Kind: terminalexperience.InteractionSecret, Message: question.Message, ConsoleStepID: cmAddCredentialFormID, TranscriptLabel: question.Message, Sensitive: true, Validate: func(answer terminalexperience.InteractionAnswer) error { return question.Validate(answer.Value) }})
}

func cmAddTextFormID(message string) string {
	switch message {
	case "OpenAI-compatible base URL":
		return cmAddEndpointFormID
	case "Model":
		return cmAddModelFormID
	default:
		return cmAddIdentityFormID
	}
}

func cmAddTranscriptProject(message string) func(terminalexperience.InteractionAnswer) string {
	switch message {
	case "Profile name":
		return func(answer terminalexperience.InteractionAnswer) string { return safeCMAddName(answer.Value) }
	case "OpenAI-compatible base URL":
		return func(answer terminalexperience.InteractionAnswer) string { return safeCMAddURL(answer.Value) }
	case "Model":
		return func(answer terminalexperience.InteractionAnswer) string { return safeCMAddModel(answer.Value) }
	default:
		return nil
	}
}

func (adapter *terminalCMAddAdapter) Cancel(message string) {
	_ = adapter.run.Result(terminalCMAddDocument(message, true))
}
func (adapter *terminalCMAddAdapter) Success(message string) {
	_ = adapter.run.Result(terminalCMAddDocument(message, false))
}

func (adapter *terminalCMAddAdapter) ask(request terminalexperience.InteractionRequest) (string, bool, error) {
	answer, err := adapter.run.Ask(request)
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) || errors.Is(err, context.Canceled) {
		return "", true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return "", false, errConfigCMAddRequiresInteractive
	}
	if err != nil {
		return "", false, err
	}
	return answer.Value, false, nil
}

func terminalCMAddDocument(message string, cancelled bool) terminalexperience.PresentationDocument {
	role := terminalexperience.VisualRoleSuccess
	if cancelled {
		role = terminalexperience.VisualRoleWarning
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: message}}}
}

func terminalCMAddOutcomeDocument(message string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: message}}}
}
