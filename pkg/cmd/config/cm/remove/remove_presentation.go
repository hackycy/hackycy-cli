package remove

import "fmt"

// RemovePresenter renders user-visible config cm remove outcomes.
type RemovePresenter interface {
	Cancel(message string)
	Success(message string)
}

// PresentRemoveCancellation reports a cancelled or declined removal without an error outcome.
func PresentRemoveCancellation(presenter RemovePresenter) {
	presenter.Cancel("Cancelled")
}

// PresentRemoveSuccess reports only the deleted profile identity.
func PresentRemoveSuccess(presenter RemovePresenter, name string) {
	presenter.Success(fmt.Sprintf("Profile %s removed", name))
}
