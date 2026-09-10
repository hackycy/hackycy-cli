package run

import "strings"

// Presenter reports run progress and cancellation to the terminal.
type Presenter interface {
	Intro(string)
	Info(string)
	Blank()
	Cancel(string)
}

func presentIntroduction(presenter Presenter) {
	presenter.Intro("Run Script")
}

func presentLaunch(presenter Presenter, request ChildRequest) {
	presenter.Info(strings.Join(append([]string{request.Executable}, request.Arguments...), " "))
	presenter.Blank()
}

func presentCancellation(presenter Presenter) {
	presenter.Cancel("Operation cancelled.")
}
