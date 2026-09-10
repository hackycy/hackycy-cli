package remove

import (
	"errors"
	"testing"
)

func TestConfirmRemoveUsesLegacyQuestionAndAcceptsConfirmation(t *testing.T) {
	prompter := &scriptedCMRemoveConfirmationPrompter{confirmed: true}

	outcome, err := ConfirmRemove("work", prompter)

	if err != nil || outcome != RemoveConfirmed {
		t.Fatalf("ConfirmRemove() = (%v, %v), want confirmed", outcome, err)
	}
	if got, want := prompter.question, (RemoveConfirmPrompt{Message: "Remove CM profile \"work\"?"}); got != want {
		t.Fatalf("confirmation question = %#v, want %#v", got, want)
	}
}

func TestConfirmRemoveDoesNotEscapeAConfiguredProfileName(t *testing.T) {
	prompter := &scriptedCMRemoveConfirmationPrompter{confirmed: true}

	_, _ = ConfirmRemove("work\"profile", prompter)

	if got, want := prompter.question, (RemoveConfirmPrompt{Message: "Remove CM profile \"work\"profile\"?"}); got != want {
		t.Fatalf("confirmation question = %#v, want %#v", got, want)
	}
}

func TestConfirmRemoveRetainsNegativeConfirmation(t *testing.T) {
	prompter := &scriptedCMRemoveConfirmationPrompter{}

	if outcome, err := ConfirmRemove("work", prompter); err != nil || outcome != RemoveDeclined {
		t.Fatalf("ConfirmRemove() = (%v, %v), want declined", outcome, err)
	}
}

func TestConfirmRemoveRetainsCancellation(t *testing.T) {
	prompter := &scriptedCMRemoveConfirmationPrompter{cancelled: true}

	if outcome, err := ConfirmRemove("work", prompter); err != nil || outcome != RemoveConfirmationCancelled {
		t.Fatalf("ConfirmRemove() = (%v, %v), want cancelled", outcome, err)
	}
}

func TestConfirmRemovePropagatesInteractionFailure(t *testing.T) {
	failure := errors.New("terminal unavailable")

	if outcome, err := ConfirmRemove("work", &scriptedCMRemoveConfirmationPrompter{err: failure}); outcome != RemoveDeclined || !errors.Is(err, failure) {
		t.Fatalf("ConfirmRemove() = (%v, %v), want failure", outcome, err)
	}
}

type scriptedCMRemoveConfirmationPrompter struct {
	confirmed bool
	cancelled bool
	err       error
	question  RemoveConfirmPrompt
}

func (prompter *scriptedCMRemoveConfirmationPrompter) Confirm(question RemoveConfirmPrompt) (bool, bool, error) {
	prompter.question = question
	return prompter.confirmed, prompter.cancelled, prompter.err
}
