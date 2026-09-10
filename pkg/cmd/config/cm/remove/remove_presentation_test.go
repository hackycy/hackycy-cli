package remove

import "testing"

func TestPresentRemoveCancellationUsesTheLegacyMessage(t *testing.T) {
	presenter := &recordingCMRemovePresenter{}

	PresentRemoveCancellation(presenter)

	if presenter.cancellation != "Cancelled" || presenter.success != "" {
		t.Fatalf("presenter = %#v", presenter)
	}
}

func TestPresentRemoveSuccessReportsOnlyTheProfileName(t *testing.T) {
	presenter := &recordingCMRemovePresenter{}

	PresentRemoveSuccess(presenter, "work")

	if presenter.success != "Profile work removed" || presenter.cancellation != "" {
		t.Fatalf("presenter = %#v", presenter)
	}
}

type recordingCMRemovePresenter struct {
	cancellation string
	success      string
}

func (presenter *recordingCMRemovePresenter) Cancel(message string) {
	presenter.cancellation = message
}

func (presenter *recordingCMRemovePresenter) Success(message string) {
	presenter.success = message
}
