package add

import (
	"strings"
	"testing"
)

func TestPresentAddCancellationUsesTheLegacyMessage(t *testing.T) {
	presenter := &recordingCMAddPresenter{}

	PresentAddCancellation(presenter)

	if presenter.cancellation != "Cancelled" {
		t.Fatalf("cancellation = %q, want %q", presenter.cancellation, "Cancelled")
	}
}

func TestPresentAddSuccessReportsTheAddedProfileWithoutTheAPIKey(t *testing.T) {
	presenter := &recordingCMAddPresenter{}
	input := AddInput{Name: "work", APIKey: "secret-api-key"}

	PresentAddSuccess(presenter, input)

	if got, want := presenter.success, "Profile work added"; got != want {
		t.Fatalf("success = %q, want %q", got, want)
	}
	if strings.Contains(presenter.success, input.APIKey) {
		t.Fatalf("success exposed the API key: %q", presenter.success)
	}
}

type recordingCMAddPresenter struct {
	cancellation string
	success      string
}

func (presenter *recordingCMAddPresenter) Cancel(message string) {
	presenter.cancellation = message
}

func (presenter *recordingCMAddPresenter) Success(message string) {
	presenter.success = message
}
