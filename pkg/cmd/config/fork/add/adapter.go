package add

import (
	"context"
	"errors"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

type terminalForkAddAdapter struct {
	run terminalexperience.ExperienceRun
}

func newTerminalForkAddAdapter(run terminalexperience.ExperienceRun) *terminalForkAddAdapter {
	return &terminalForkAddAdapter{run: run}
}

func (adapter *terminalForkAddAdapter) Text(question TextPrompt) (string, bool, error) {
	return adapter.ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionText,
		Message:         question.Message,
		Placeholder:     question.Placeholder,
		ConsoleStepID:   forkAddTextFormID(question.Message),
		TranscriptLabel: question.Message,
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string {
			if question.Message == "Host" {
				return safeForkAddHost(answer.Value)
			}
			return safeForkAddField(answer.Value, "Instance configured")
		},
		Validate: func(answer terminalexperience.InteractionAnswer) error {
			return question.Validate(answer.Value)
		},
	})
}

func (adapter *terminalForkAddAdapter) Select(question SelectPrompt) (string, bool, error) {
	request := terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionSelect,
		Message:         question.Message,
		ConsoleStepID:   forkAddSelectFormID(question.Message),
		TranscriptLabel: question.Message,
		Options:         interactionOptions(question.Choices),
	}
	if len(question.Choices) > 0 {
		request.HasDefault = true
		request.Default = terminalexperience.InteractionAnswer{Value: question.Choices[0].Value}
	}
	return adapter.ask(request)
}

func (adapter *terminalForkAddAdapter) Password(question TextPrompt) (string, bool, error) {
	return adapter.ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionSecret,
		Message:         question.Message,
		ConsoleStepID:   forkAddCredentialFormID,
		TranscriptLabel: question.Message,
		Sensitive:       true,
		Validate: func(answer terminalexperience.InteractionAnswer) error {
			return question.Validate(answer.Value)
		},
	})
}

func forkAddTextFormID(message string) string {
	if message == "Host" {
		return forkAddHostFormID
	}
	return forkAddIdentityFormID
}

func forkAddSelectFormID(message string) string {
	if message == "Protocol" {
		return forkAddProtocolFormID
	}
	return forkAddProviderFormID
}

func (adapter *terminalForkAddAdapter) Cancel(message string) {
	_ = adapter.run.Result(terminalForkAddDocument(message, true))
}

func (adapter *terminalForkAddAdapter) Success(message string) {
	_ = adapter.run.Result(terminalForkAddDocument(message, false))
}

func (adapter *terminalForkAddAdapter) ask(request terminalexperience.InteractionRequest) (string, bool, error) {
	answer, err := adapter.run.Ask(request)
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) || errors.Is(err, context.Canceled) {
		return "", true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return "", false, errConfigForkAddRequiresInteractive
	}
	if err != nil {
		return "", false, err
	}
	return answer.Value, false, nil
}

func interactionOptions(choices []Choice) []terminalexperience.InteractionOption {
	options := make([]terminalexperience.InteractionOption, 0, len(choices))
	for _, choice := range choices {
		options = append(options, terminalexperience.InteractionOption{Label: choice.Label, Value: choice.Value})
	}
	return options
}

func terminalForkAddDocument(message string, cancelled bool) terminalexperience.PresentationDocument {
	role := terminalexperience.VisualRoleSuccess
	if cancelled {
		role = terminalexperience.VisualRoleWarning
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: message}}}
}

func terminalForkAddOutcomeDocument(message string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: message}}}
}
