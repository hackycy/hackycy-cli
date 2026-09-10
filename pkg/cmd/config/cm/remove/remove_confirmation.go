package remove

import "fmt"

// RemoveConfirmPrompt describes the confirmation preceding a CM profile removal.
type RemoveConfirmPrompt struct {
	Message     string
	Description string
}

// RemoveConfirmationPrompter confirms one requested CM profile removal.
type RemoveConfirmationPrompter interface {
	Confirm(RemoveConfirmPrompt) (confirmed bool, cancelled bool, err error)
}

// RemoveConfirmation records the read-only decision before a CM profile deletion.
type RemoveConfirmation uint8

const (
	RemoveDeclined RemoveConfirmation = iota
	RemoveConfirmed
	RemoveConfirmationCancelled
)

// ConfirmRemove asks for the legacy CM profile removal confirmation without mutating configuration.
func ConfirmRemove(name string, prompter RemoveConfirmationPrompter) (RemoveConfirmation, error) {
	confirmed, cancelled, err := prompter.Confirm(RemoveConfirmPrompt{Message: fmt.Sprintf("Remove CM profile \"%s\"?", name)})
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
