package run

import (
	"reflect"
	"testing"
)

func TestPresentationUsesTheLegacyRunMessages(t *testing.T) {
	presenter := &recordingRunPresenter{}
	presentIntroduction(presenter)
	presentLaunch(presenter, ChildRequest{
		Executable: string(PackageManagerExternal),
		Arguments:  []string{"run", "check"},
	})
	presentCancellation(presenter)

	want := []string{
		"intro:Run Script",
		"info:" + string(PackageManagerExternal) + " run check",
		"blank",
		"cancel:Operation cancelled.",
	}
	if !reflect.DeepEqual(presenter.events, want) {
		t.Fatalf("events = %#v, want %#v", presenter.events, want)
	}
}

type recordingRunPresenter struct {
	events []string
}

func (presenter *recordingRunPresenter) Intro(message string) {
	presenter.events = append(presenter.events, "intro:"+message)
}

func (presenter *recordingRunPresenter) Info(message string) {
	presenter.events = append(presenter.events, "info:"+message)
}

func (presenter *recordingRunPresenter) Blank() {
	presenter.events = append(presenter.events, "blank")
}

func (presenter *recordingRunPresenter) Cancel(message string) {
	presenter.events = append(presenter.events, "cancel:"+message)
}
