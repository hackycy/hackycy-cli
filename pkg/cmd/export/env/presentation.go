package env

// Presenter reports command progress and results to the terminal.
type Presenter interface {
	Outro(message string)
	Print(value string)
	Cancel(message string)
}

// Present reports a successful export without performing any file I/O.
func Present(presenter Presenter, output, target string) {
	if target != "" {
		presenter.Outro("Writing output to " + target)
		return
	}
	presenter.Outro("Exported variables:")
	presenter.Print(output)
}

// PresentCancellation reports an interactive cancellation without exiting the process.
func PresentCancellation(presenter Presenter) {
	presenter.Cancel("Cancelled")
}
