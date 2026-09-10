package remove

import "fmt"

// ConfirmPrompt describes one terminal confirmation question.
type ConfirmPrompt struct {
	Message string
}

// RemoveConfirmationPrompter confirms the selected Fork removal.
type RemoveConfirmationPrompter interface {
	Confirm(ConfirmPrompt) (confirmed bool, cancelled bool, err error)
}

// RemoveConfirmation is the read-only decision that precedes a Fork deletion.
type RemoveConfirmation uint8

const (
	RemoveDeclined RemoveConfirmation = iota
	RemoveConfirmed
	RemoveConfirmationCancelled
)

// ConfirmRemove asks for the legacy removal confirmation without mutating configuration.
func ConfirmRemove(name string, prompter RemoveConfirmationPrompter) (RemoveConfirmation, error) {
	confirmed, cancelled, err := prompter.Confirm(ConfirmPrompt{Message: fmt.Sprintf("Remove instance \"%s\"?", name)})
	if err != nil {
		return RemoveDeclined, err
	}
	if cancelled {
		return RemoveConfirmationCancelled, nil
	}
	if !confirmed {
		return RemoveDeclined, nil
	}
	return RemoveConfirmed, nil
}
