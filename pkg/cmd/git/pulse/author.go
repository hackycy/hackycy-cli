package pulse

import (
	"sort"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// AuthorChoice is one selectable commit author.
type AuthorChoice struct {
	Value string
	Label string
}

// AuthorPrompt describes the legacy author multi-selection.
type AuthorPrompt struct {
	Message       string
	Options       []AuthorChoice
	InitialValues []string
	Required      bool
}

// AuthorPrompter owns author selection without exposing terminal implementation details.
type AuthorPrompter interface {
	SelectAuthors(AuthorPrompt) (selected []string, cancelled bool, err error)
}

// SelectAuthors keeps all commits for zero or one author and otherwise filters by a prompt selection.
func SelectAuthors(commits []Commit, prompter AuthorPrompter) ([]Commit, bool, error) {
	authors := pulseAuthors(commits)
	if len(authors) <= 1 {
		return commits, false, nil
	}

	initialValues := []string(nil)
	if len(authors) <= 3 {
		initialValues = append([]string(nil), authors...)
	}
	selected, cancelled, err := prompter.SelectAuthors(AuthorPrompt{
		Message:       "Filter by authors:",
		Options:       authorChoices(authors),
		InitialValues: initialValues,
		Required:      true,
	})
	if err != nil {
		return nil, false, err
	}
	if cancelled {
		return nil, true, nil
	}

	selectedAuthors := make(map[string]struct{}, len(selected))
	for _, author := range selected {
		selectedAuthors[author] = struct{}{}
	}
	filtered := make([]Commit, 0, len(commits))
	for _, commit := range commits {
		if _, included := selectedAuthors[commit.Author]; included {
			filtered = append(filtered, commit)
		}
	}
	return filtered, false, nil
}

func pulseAuthors(commits []Commit) []string {
	set := make(map[string]struct{}, len(commits))
	for _, commit := range commits {
		set[commit.Author] = struct{}{}
	}
	authors := make([]string, 0, len(set))
	for author := range set {
		authors = append(authors, author)
	}
	collator := collate.New(language.AmericanEnglish)
	sort.Slice(authors, func(left, right int) bool {
		return collator.CompareString(authors[left], authors[right]) < 0
	})
	return authors
}

func authorChoices(authors []string) []AuthorChoice {
	choices := make([]AuthorChoice, 0, len(authors))
	for _, author := range authors {
		choices = append(choices, AuthorChoice{Value: author, Label: author})
	}
	return choices
}
