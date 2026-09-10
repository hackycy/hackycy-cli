package rm

import (
	"errors"
	"reflect"
	"testing"
)

func TestPresentIntroductionMissingPathsAndExplicitTargets(t *testing.T) {
	presenter := &recordingPresenter{}
	paths := []string{"/tmp/one", "/tmp/two"}

	presentIntroduction(presenter)
	presentMissingPaths(presenter, paths)
	presentExplicitPaths(presenter, paths)

	wantEvents := []string{
		"intro:Remove",
		"notice:  not found, skipping: /tmp/one",
		"notice:  not found, skipping: /tmp/two",
		"paths",
	}
	if !reflect.DeepEqual(presenter.events, wantEvents) || !reflect.DeepEqual(presenter.paths, paths) {
		t.Fatalf("presenter = %#v, want events %#v and paths %#v", presenter, wantEvents, paths)
	}
}

func TestPresentCancellationOutcomesUseLegacyMessages(t *testing.T) {
	testCases := []struct {
		name    string
		present func(Presenter)
		message string
	}{
		{name: "no valid paths", present: presentNoValidPaths, message: "No valid paths to delete."},
		{name: "cancelled", present: presentCancellation, message: "Cancelled."},
		{name: "nothing selected", present: presentNothingSelected, message: "Nothing selected."},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			presenter := &recordingPresenter{}
			testCase.present(presenter)
			if !reflect.DeepEqual(presenter.events, []string{"cancel:" + testCase.message}) {
				t.Fatalf("events = %#v", presenter.events)
			}
		})
	}
}

func TestPresentScanAndNoTargetOutcomesUseLegacyCounts(t *testing.T) {
	presenter := &recordingPresenter{}

	presentScanStart(presenter)
	presentScanStop(presenter, 0)
	presentNothingToClean(presenter)
	presentScanStart(presenter)
	presentScanStop(presenter, 1)
	presentScanStart(presenter)
	presentScanStop(presenter, 2)

	want := []string{
		"start:Scanning...",
		"stop:No targets found.",
		"outro:Nothing to clean.",
		"start:Scanning...",
		"stop:Found 1 target",
		"start:Scanning...",
		"stop:Found 2 targets",
	}
	if !reflect.DeepEqual(presenter.events, want) {
		t.Fatalf("events = %#v, want %#v", presenter.events, want)
	}
}

func TestPresentDeleteResultKeepsPartialFailuresAndSuccessfulCompletion(t *testing.T) {
	presenter := &recordingPresenter{}
	firstFailure := errors.New("first failure")
	secondFailure := errors.New("second failure")

	presentDeleteStart(presenter, 2)
	presentDeleteResult(presenter, deletionResult{succeeded: 1, failures: []error{firstFailure, secondFailure}})

	want := []string{
		"start:Deleting 2 items...",
		"stop:Deleted 1 item",
		"notice:  skipped: first failure",
		"notice:  skipped: second failure",
		"outro:Done!",
	}
	if !reflect.DeepEqual(presenter.events, want) {
		t.Fatalf("events = %#v, want %#v", presenter.events, want)
	}
}

type recordingPresenter struct {
	events []string
	paths  []string
}

func (presenter *recordingPresenter) Intro(message string) {
	presenter.events = append(presenter.events, "intro:"+message)
}

func (presenter *recordingPresenter) Paths(paths []string) {
	presenter.events = append(presenter.events, "paths")
	presenter.paths = append([]string(nil), paths...)
}

func (presenter *recordingPresenter) Notice(message string) {
	presenter.events = append(presenter.events, "notice:"+message)
}

func (presenter *recordingPresenter) ProgressStart(message string) {
	presenter.events = append(presenter.events, "start:"+message)
}

func (presenter *recordingPresenter) ProgressStop(message string) {
	presenter.events = append(presenter.events, "stop:"+message)
}

func (presenter *recordingPresenter) Cancel(message string) {
	presenter.events = append(presenter.events, "cancel:"+message)
}

func (presenter *recordingPresenter) Outro(message string) {
	presenter.events = append(presenter.events, "outro:"+message)
}
