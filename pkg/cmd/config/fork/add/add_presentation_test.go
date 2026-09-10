package add

import (
	"strings"
	"testing"
)

func TestPresentAddCancellationUsesTheLegacyMessage(t *testing.T) {
	presenter := &recordingAddPresenter{}

	PresentAddCancellation(presenter)

	if presenter.cancellation != "Cancelled" {
		t.Fatalf("cancellation = %q, want %q", presenter.cancellation, "Cancelled")
	}
}

func TestPresentAddSuccessReportsTheAddedAliasAndHostWithoutTheToken(t *testing.T) {
	presenter := &recordingAddPresenter{}
	input := AddInput{Alias: "work", Host: "gitlab.example", Token: "secret-token"}

	PresentAddSuccess(presenter, input)

	want := "Instance work (gitlab.example) added successfully"
	if presenter.success != want {
		t.Fatalf("success = %q, want %q", presenter.success, want)
	}
	if strings.Contains(presenter.success, input.Token) {
		t.Fatalf("success exposed the token: %q", presenter.success)
	}
}

type recordingAddPresenter struct {
	cancellation string
	success      string
}

func (presenter *recordingAddPresenter) Cancel(message string) {
	presenter.cancellation = message
}

func (presenter *recordingAddPresenter) Success(message string) {
	presenter.success = message
}
