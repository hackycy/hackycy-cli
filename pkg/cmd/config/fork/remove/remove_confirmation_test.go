package remove

import "testing"

func TestConfirmRemoveUsesLegacyQuestionAndAcceptsConfirmation(t *testing.T) {
	prompter := &scriptedRemoveConfirmationPrompter{confirmed: true}

	outcome, err := ConfirmRemove("work", prompter)
	if err != nil {
		t.Fatalf("ConfirmRemove() error = %v", err)
	}

	if outcome != RemoveConfirmed {
		t.Fatalf("ConfirmRemove() = %v, want confirmed", outcome)
	}
	if got, want := prompter.question, (ConfirmPrompt{Message: `Remove instance "work"?`}); got != want {
		t.Fatalf("confirmation question = %#v, want %#v", got, want)
	}
}

func TestConfirmRemoveDoesNotEscapeAConfiguredAlias(t *testing.T) {
	prompter := &scriptedRemoveConfirmationPrompter{confirmed: true}

	if _, err := ConfirmRemove(`work"alias`, prompter); err != nil {
		t.Fatalf("ConfirmRemove() error = %v", err)
	}

	if got, want := prompter.question, (ConfirmPrompt{Message: `Remove instance "work"alias"?`}); got != want {
		t.Fatalf("confirmation question = %#v, want %#v", got, want)
	}
}

func TestConfirmRemoveRetainsNegativeConfirmation(t *testing.T) {
	prompter := &scriptedRemoveConfirmationPrompter{}

	if outcome, err := ConfirmRemove("work", prompter); err != nil || outcome != RemoveDeclined {
		t.Fatalf("ConfirmRemove() = (%v, %v), want declined", outcome, err)
	}
}

func TestConfirmRemoveRetainsCancellation(t *testing.T) {
	prompter := &scriptedRemoveConfirmationPrompter{cancelled: true}

	if outcome, err := ConfirmRemove("work", prompter); err != nil || outcome != RemoveConfirmationCancelled {
		t.Fatalf("ConfirmRemove() = (%v, %v), want cancelled", outcome, err)
	}
}

type scriptedRemoveConfirmationPrompter struct {
	confirmed bool
	cancelled bool
	err       error
	question  ConfirmPrompt
}

func (prompter *scriptedRemoveConfirmationPrompter) Confirm(question ConfirmPrompt) (bool, bool, error) {
	prompter.question = question
	return prompter.confirmed, prompter.cancelled, prompter.err
}
