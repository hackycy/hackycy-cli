package remove

import "testing"

func TestPresentRemoveEmptyUsesTheLegacyNoopMessages(t *testing.T) {
	presenter := &recordingRemovePresenter{}

	PresentRemoveEmpty(presenter)

	if got, want := presenter.infos, []string{"No instances configured"}; !sameAddStrings(got, want) {
		t.Fatalf("info messages = %#v, want %#v", got, want)
	}
	if got, want := presenter.outcomes, []string{"Nothing to remove"}; !sameAddStrings(got, want) {
		t.Fatalf("outcome messages = %#v, want %#v", got, want)
	}
}

func TestPresentRemoveCancellationUsesTheLegacyMessage(t *testing.T) {
	presenter := &recordingRemovePresenter{}

	PresentRemoveCancellation(presenter)

	if got, want := presenter.outcomes, []string{"Cancelled"}; !sameAddStrings(got, want) {
		t.Fatalf("outcome messages = %#v, want %#v", got, want)
	}
}

func TestPresentRemoveSuccessDoesNotExposeAnyForkCredential(t *testing.T) {
	presenter := &recordingRemovePresenter{}

	PresentRemoveSuccess(presenter, "work")

	if got, want := presenter.outcomes, []string{"Instance work removed"}; !sameAddStrings(got, want) {
		t.Fatalf("outcome messages = %#v, want %#v", got, want)
	}
}

type recordingRemovePresenter struct {
	infos    []string
	outcomes []string
}

func (presenter *recordingRemovePresenter) Info(message string) {
	presenter.infos = append(presenter.infos, message)
}

func (presenter *recordingRemovePresenter) Outcome(message string) {
	presenter.outcomes = append(presenter.outcomes, message)
}
