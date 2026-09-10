package rm

import (
	"fmt"
	"path/filepath"
)

// ExplicitConfirmationPrompt describes the default-negative explicit deletion prompt.
type ExplicitConfirmationPrompt struct {
	Message     string
	Initial     bool
	Description string
}

// SmartActionPrompt describes the first smart-cleanup prompt.
type SmartActionPrompt struct {
	Message string
	Options []SmartAction
}

// SmartTargetChoice is one path available in the smart-cleanup multiselect.
type SmartTargetChoice struct {
	Value string
	Label string
}

// SmartTargetPrompt describes the smart-cleanup multiselect.
type SmartTargetPrompt struct {
	Message       string
	Options       []SmartTargetChoice
	InitialValues []string
}

// Prompter owns the terminal interactions required by rm.
type Prompter interface {
	ConfirmExplicit(ExplicitConfirmationPrompt) (confirmed bool, cancelled bool, err error)
	SelectSmartAction(SmartActionPrompt) (SmartAction, bool, error)
	SelectSmartTargets(SmartTargetPrompt) ([]string, bool, error)
}

func selectExplicitTargets(targets []string, force bool, prompter Prompter) ([]string, bool, error) {
	if force {
		return targets, false, nil
	}
	confirmed, cancelled, err := prompter.ConfirmExplicit(ExplicitConfirmationPrompt{
		Message: fmt.Sprintf("Delete %d item%s?", len(targets), pluralSuffix(len(targets))),
		Initial: false,
	})
	if err != nil {
		return nil, false, err
	}
	if cancelled || !confirmed {
		return []string{}, true, nil
	}
	return targets, false, nil
}

func selectSmartAction(prompter Prompter) (SmartAction, bool, error) {
	options := append([]SmartAction(nil), smartActions...)
	return prompter.SelectSmartAction(SmartActionPrompt{
		Message: "Select a clean action",
		Options: options,
	})
}

func selectSmartTargets(workingDirectory string, targets []string, force bool, prompter Prompter) ([]string, bool, error) {
	if force {
		return targets, false, nil
	}
	options := make([]SmartTargetChoice, 0, len(targets))
	for _, target := range targets {
		label, err := filepath.Rel(workingDirectory, target)
		if err != nil {
			return nil, false, err
		}
		options = append(options, SmartTargetChoice{Value: target, Label: label})
	}
	selected, cancelled, err := prompter.SelectSmartTargets(SmartTargetPrompt{
		Message:       "Select items to delete",
		Options:       options,
		InitialValues: targets,
	})
	if err != nil {
		return nil, false, err
	}
	if cancelled {
		return []string{}, true, nil
	}
	return selected, false, nil
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
