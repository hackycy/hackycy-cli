package pulse

// Input is the typed request for git pulse.
type Input struct {
	Directory string
	Days      *int
}

// DayChoice is one selectable legacy date range.
type DayChoice struct {
	Value int
	Label string
}

// DayPrompt describes the date-range selection.
type DayPrompt struct {
	Message string
	Options []DayChoice
}

// Prompter owns the two interactive selections required by git pulse.
type Prompter interface {
	SelectDays(DayPrompt) (days int, cancelled bool, err error)
	AuthorPrompter
}

func selectDays(input Input, prompter Prompter) (int, bool, error) {
	if input.Days != nil {
		return *input.Days, false, nil
	}
	return prompter.SelectDays(DayPrompt{
		Message: "Select date range:",
		Options: []DayChoice{
			{Value: 1, Label: "Today"},
			{Value: 2, Label: "Yesterday"},
			{Value: 3, Label: "Last 3 days"},
			{Value: 7, Label: "Last 7 days"},
			{Value: 30, Label: "Last 30 days"},
		},
	})
}
