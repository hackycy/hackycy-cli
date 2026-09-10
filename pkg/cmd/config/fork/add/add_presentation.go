package add

import "fmt"

// AddPresenter renders the user-visible outcomes of config fork add.
type AddPresenter interface {
	Cancel(message string)
	Success(message string)
}

// PresentAddCancellation reports an interactive cancellation without an error outcome.
func PresentAddCancellation(presenter AddPresenter) {
	presenter.Cancel("Cancelled")
}

// PresentAddSuccess reports the persisted alias and host without disclosing its token.
func PresentAddSuccess(presenter AddPresenter, input AddInput) {
	presenter.Success(fmt.Sprintf("Instance %s (%s) added successfully", input.Alias, input.Host))
}
