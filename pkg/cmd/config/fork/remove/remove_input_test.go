package remove

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestSelectRemoveReportsEmptyConfigurationWithoutPrompting(t *testing.T) {
	prompter := &scriptedRemovePrompter{}

	selection, err := SelectRemove(removeReader{instances: nil}, prompter)

	if err != nil {
		t.Fatalf("SelectRemove() returned an error: %v", err)
	}
	if selection != (RemoveSelection{Empty: true}) {
		t.Fatalf("SelectRemove() = %#v, want empty selection", selection)
	}
	if len(prompter.questions) != 0 {
		t.Fatalf("SelectRemove() prompted for empty configuration: %#v", prompter.questions)
	}
}

func TestSelectRemovePreservesConfiguredOrderAndHostLabels(t *testing.T) {
	prompter := &scriptedRemovePrompter{value: "personal"}
	instances := []appconfig.ForkInstance{
		{Name: "work", Host: "gitlab.example"},
		{Name: "personal", Host: "github.example"},
	}

	selection, err := SelectRemove(removeReader{instances: instances}, prompter)

	if err != nil {
		t.Fatalf("SelectRemove() returned an error: %v", err)
	}
	if selection != (RemoveSelection{Name: "personal"}) {
		t.Fatalf("SelectRemove() = %#v, want personal selection", selection)
	}
	if got, want := prompter.questions, []SelectPrompt{{
		Message: "Select instance to remove",
		Choices: []Choice{
			{Value: "work", Label: "work (gitlab.example)"},
			{Value: "personal", Label: "personal (github.example)"},
		},
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("select questions = %#v, want %#v", got, want)
	}
}

func TestSelectRemoveReturnsCancellationWithoutMutationIntent(t *testing.T) {
	prompter := &scriptedRemovePrompter{cancelled: true}

	selection, err := SelectRemove(removeReader{instances: []appconfig.ForkInstance{{Name: "work", Host: "gitlab.example"}}}, prompter)

	if err != nil {
		t.Fatalf("SelectRemove() returned an error: %v", err)
	}
	if selection != (RemoveSelection{Cancelled: true}) {
		t.Fatalf("SelectRemove() = %#v, want cancelled selection", selection)
	}
}

func TestSelectRemovePropagatesReadFailureBeforePrompting(t *testing.T) {
	prompter := &scriptedRemovePrompter{}
	want := errors.New("read configuration")

	selection, err := SelectRemove(removeReader{err: want}, prompter)

	if !errors.Is(err, want) || selection != (RemoveSelection{}) {
		t.Fatalf("SelectRemove() = (%#v, %v), want read error", selection, err)
	}
	if len(prompter.questions) != 0 {
		t.Fatalf("SelectRemove() prompted after read failure: %#v", prompter.questions)
	}
}

type removeReader struct {
	instances []appconfig.ForkInstance
	err       error
}

func (reader removeReader) ListForkInstances() ([]appconfig.ForkInstance, error) {
	return reader.instances, reader.err
}

type scriptedRemovePrompter struct {
	value     string
	cancelled bool
	err       error
	questions []SelectPrompt
}

func (prompter *scriptedRemovePrompter) Select(question SelectPrompt) (string, bool, error) {
	prompter.questions = append(prompter.questions, question)
	return prompter.value, prompter.cancelled, prompter.err
}
