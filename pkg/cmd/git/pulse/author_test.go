package pulse

import (
	"reflect"
	"testing"
)

func TestSelectAuthorsSkipsThePromptForZeroOrOneAuthor(t *testing.T) {
	noCommits, cancelled, err := SelectAuthors(nil, panicPulseAuthorPrompter{})
	if err != nil || cancelled || len(noCommits) != 0 {
		t.Fatalf("zero-author selection = (%#v, %t, %v)", noCommits, cancelled, err)
	}

	commits := []Commit{{Author: "Ada", Subject: "one"}, {Author: "Ada", Subject: "two"}}
	selected, cancelled, err := SelectAuthors(commits, panicPulseAuthorPrompter{})
	if err != nil || cancelled || !reflect.DeepEqual(selected, commits) {
		t.Fatalf("one-author selection = (%#v, %t, %v), want %#v, false, nil", selected, cancelled, err, commits)
	}
}

func TestSelectAuthorsUsesLegacyDefaultsAndFiltersSelection(t *testing.T) {
	testCases := []struct {
		name         string
		commits      []Commit
		selected     []string
		wantInitial  []string
		wantFiltered []Commit
	}{
		{
			name:         "two authors begin selected",
			commits:      []Commit{{Author: "Zed", Subject: "first"}, {Author: "Ada", Subject: "second"}},
			selected:     []string{"Ada"},
			wantInitial:  []string{"Ada", "Zed"},
			wantFiltered: []Commit{{Author: "Ada", Subject: "second"}},
		},
		{
			name:         "three authors begin selected",
			commits:      []Commit{{Author: "Cara"}, {Author: "Ada"}, {Author: "Ben"}},
			selected:     []string{"Ben"},
			wantInitial:  []string{"Ada", "Ben", "Cara"},
			wantFiltered: []Commit{{Author: "Ben"}},
		},
		{
			name:         "more than three authors begin unselected",
			commits:      []Commit{{Author: "Dora"}, {Author: "Cara"}, {Author: "Ada"}, {Author: "Ben"}},
			selected:     []string{"Dora"},
			wantInitial:  nil,
			wantFiltered: []Commit{{Author: "Dora"}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			prompter := &scriptedPulseAuthorPrompter{selected: testCase.selected}
			got, cancelled, err := SelectAuthors(testCase.commits, prompter)
			if err != nil || cancelled {
				t.Fatal("SelectAuthors() unexpectedly cancelled")
			}
			if !reflect.DeepEqual(got, testCase.wantFiltered) {
				t.Fatalf("filtered commits = %#v, want %#v", got, testCase.wantFiltered)
			}
			if got, want := prompter.prompt.Message, "Filter by authors:"; got != want {
				t.Fatalf("prompt message = %q, want %q", got, want)
			}
			if !prompter.prompt.Required {
				t.Fatal("author prompt is not required")
			}
			if !reflect.DeepEqual(prompter.prompt.InitialValues, testCase.wantInitial) {
				t.Fatalf("initial authors = %#v, want %#v", prompter.prompt.InitialValues, testCase.wantInitial)
			}
		})
	}
}

func TestSelectAuthorsReturnsCancellationWithoutFiltering(t *testing.T) {
	selected, cancelled, err := SelectAuthors([]Commit{{Author: "Ada"}, {Author: "Ben"}}, &scriptedPulseAuthorPrompter{cancelled: true})
	if err != nil || !cancelled || selected != nil {
		t.Fatalf("SelectAuthors() = (%#v, %t, %v), want nil, true, nil", selected, cancelled, err)
	}
}

type scriptedPulseAuthorPrompter struct {
	prompt    AuthorPrompt
	selected  []string
	cancelled bool
}

func (prompter *scriptedPulseAuthorPrompter) SelectAuthors(prompt AuthorPrompt) ([]string, bool, error) {
	prompter.prompt = prompt
	return prompter.selected, prompter.cancelled, nil
}

type panicPulseAuthorPrompter struct{}

func (panicPulseAuthorPrompter) SelectAuthors(AuthorPrompt) ([]string, bool, error) {
	panic("author prompt must not be called")
}
