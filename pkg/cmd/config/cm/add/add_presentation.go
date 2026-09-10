package add

import "fmt"

// AddPresenter renders the user-visible outcomes of config cm add.
type AddPresenter interface {
	Cancel(message string)
	Success(message string)
}

// PresentAddCancellation reports an interactive cancellation without an error outcome.
func PresentAddCancellation(presenter AddPresenter) {
	presenter.Cancel("Cancelled")
}

// PresentAddSuccess reports the persisted profile name without disclosing its API key.
func PresentAddSuccess(presenter AddPresenter, input AddInput) {
	presenter.Success(fmt.Sprintf("Profile %s added", input.Name))
}
