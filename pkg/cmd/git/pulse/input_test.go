package pulse

import (
	"reflect"
	"testing"
)

func TestSelectDaysUsesExplicitLegacyValuesWithoutPrompting(t *testing.T) {
	days := -1
	got, cancelled, err := selectDays(Input{Days: &days}, panicPulsePrompter{})
	if err != nil || cancelled || got != -1 {
		t.Fatalf("selectDays() = (%d, %t, %v), want -1, false, nil", got, cancelled, err)
	}
}

func TestSelectDaysProvidesTheLegacyPromptChoices(t *testing.T) {
	prompter := &scriptedPulsePrompter{days: 7}
	got, cancelled, err := selectDays(Input{}, prompter)
	if err != nil || cancelled || got != 7 {
		t.Fatalf("selectDays() = (%d, %t, %v), want 7, false, nil", got, cancelled, err)
	}
	want := DayPrompt{
		Message: "Select date range:",
		Options: []DayChoice{
			{Value: 1, Label: "Today"},
			{Value: 2, Label: "Yesterday"},
			{Value: 3, Label: "Last 3 days"},
			{Value: 7, Label: "Last 7 days"},
			{Value: 30, Label: "Last 30 days"},
		},
	}
	if !reflect.DeepEqual(prompter.dayPrompt, want) {
		t.Fatalf("day prompt = %#v, want %#v", prompter.dayPrompt, want)
	}
}

func TestSelectDaysReturnsPromptCancellation(t *testing.T) {
	_, cancelled, err := selectDays(Input{}, &scriptedPulsePrompter{daysCancelled: true})
	if err != nil || !cancelled {
		t.Fatal("selectDays() did not return cancellation")
	}
}

type panicPulsePrompter struct{}

func (panicPulsePrompter) SelectDays(DayPrompt) (int, bool, error) {
	panic("day prompt must not be called")
}

func (panicPulsePrompter) SelectAuthors(AuthorPrompt) ([]string, bool, error) {
	panic("author prompt must not be called")
}
