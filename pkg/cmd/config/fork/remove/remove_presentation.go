package remove

import "fmt"

// RemovePresenter renders config fork remove outcomes.
type RemovePresenter interface {
	Info(message string)
	Outcome(message string)
}

// PresentRemoveEmpty reports the legacy empty-configuration outcome.
func PresentRemoveEmpty(presenter RemovePresenter) {
	presenter.Info("No instances configured")
	presenter.Outcome("Nothing to remove")
}

// PresentRemoveCancellation reports selection cancellation or a declined confirmation.
func PresentRemoveCancellation(presenter RemovePresenter) {
	presenter.Outcome("Cancelled")
}

// PresentRemoveSuccess reports the selected name after appconfig accepted the deletion attempt.
func PresentRemoveSuccess(presenter RemovePresenter, name string) {
	presenter.Outcome(fmt.Sprintf("Instance %s removed", name))
}
