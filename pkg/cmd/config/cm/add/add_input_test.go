package add

import (
	"errors"
	"reflect"
	"testing"
)

func TestPromptAddCollectsLegacyQuestionsInOrder(t *testing.T) {
	prompter := &scriptedCMAddPrompter{responses: []cmAddPromptResponse{
		{value: "work"},
		{value: " https://provider.example/v1/// "},
		{value: " gpt-4.1-mini "},
		{value: " api-key "},
	}}

	input, cancelled, err := PromptAdd(prompter)
	if err != nil {
		t.Fatalf("PromptAdd() returned an error: %v", err)
	}
	if cancelled {
		t.Fatal("PromptAdd() reported cancellation")
	}
	want := AddInput{
		Name:    "work",
		BaseURL: " https://provider.example/v1/// ",
		Model:   " gpt-4.1-mini ",
		APIKey:  " api-key ",
	}
	if input != want {
		t.Fatalf("PromptAdd() = %#v, want %#v", input, want)
	}
	if got, want := prompter.calls, []string{
		"text:Profile name",
		"text:OpenAI-compatible base URL",
		"text:Model",
		"password:API key",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt calls = %#v, want %#v", got, want)
	}
	if got, want := prompter.questions[0].Placeholder, "e.g. openai, deepseek, work"; got != want {
		t.Fatalf("name placeholder = %q, want %q", got, want)
	}
	if got, want := prompter.questions[1].Placeholder, "https://api.openai.com/v1"; got != want {
		t.Fatalf("base URL placeholder = %q, want %q", got, want)
	}
	if got, want := prompter.questions[2].Placeholder, "gpt-4.1-mini"; got != want {
		t.Fatalf("model placeholder = %q, want %q", got, want)
	}
}

func TestPromptAddStopsWhenAnyQuestionIsCancelled(t *testing.T) {
	tests := []struct {
		name      string
		responses []cmAddPromptResponse
		wantCalls int
	}{
		{name: "name", responses: []cmAddPromptResponse{{cancelled: true}}, wantCalls: 1},
		{name: "base URL", responses: []cmAddPromptResponse{{value: "work"}, {cancelled: true}}, wantCalls: 2},
		{name: "model", responses: []cmAddPromptResponse{{value: "work"}, {value: "https://provider.example"}, {cancelled: true}}, wantCalls: 3},
		{name: "API key", responses: []cmAddPromptResponse{{value: "work"}, {value: "https://provider.example"}, {value: "model"}, {cancelled: true}}, wantCalls: 4},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompter := &scriptedCMAddPrompter{responses: test.responses}
			input, cancelled, err := PromptAdd(prompter)
			if err != nil {
				t.Fatalf("PromptAdd() returned an error: %v", err)
			}
			if !cancelled || input != (AddInput{}) {
				t.Fatalf("PromptAdd() = (%#v, %t), want an empty cancelled result", input, cancelled)
			}
			if got := len(prompter.calls); got != test.wantCalls {
				t.Fatalf("prompt call count = %d, want %d", got, test.wantCalls)
			}
		})
	}
}

func TestValidateAddInputMatchesTheLegacyValidationBoundary(t *testing.T) {
	valid := AddInput{
		Name:    "work",
		BaseURL: " https://provider.example/v1/// ",
		Model:   " model ",
		APIKey:  " key ",
	}
	if err := ValidateAddInput(valid); err != nil {
		t.Fatalf("ValidateAddInput(%#v) returned an error: %v", valid, err)
	}
	if valid.BaseURL != " https://provider.example/v1/// " || valid.Model != " model " || valid.APIKey != " key " {
		t.Fatalf("ValidateAddInput() changed input: %#v", valid)
	}

	for _, test := range []struct {
		name  string
		input AddInput
		want  string
	}{
		{name: "missing name", input: AddInput{BaseURL: "url", Model: "model", APIKey: "key"}, want: "Name is required"},
		{name: "whitespace name", input: AddInput{Name: "two words", BaseURL: "url", Model: "model", APIKey: "key"}, want: "Name cannot contain spaces"},
		{name: "missing base URL", input: AddInput{Name: "work", BaseURL: " \t", Model: "model", APIKey: "key"}, want: "Base URL is required"},
		{name: "missing model", input: AddInput{Name: "work", BaseURL: "url", Model: " \n", APIKey: "key"}, want: "Model is required"},
		{name: "missing API key", input: AddInput{Name: "work", BaseURL: "url", Model: "model", APIKey: " \r"}, want: "API key is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAddInput(test.input)
			if err == nil || err.Error() != test.want {
				t.Fatalf("ValidateAddInput(%#v) = %v, want %q", test.input, err, test.want)
			}
		})
	}
}

func TestTextPromptValidationFunctionsExposeLegacyMessages(t *testing.T) {
	prompter := &scriptedCMAddPrompter{responses: []cmAddPromptResponse{
		{value: "work"},
		{value: "https://provider.example"},
		{value: "model"},
		{value: "key"},
	}}
	if _, _, err := PromptAdd(prompter); err != nil {
		t.Fatalf("PromptAdd() returned an error: %v", err)
	}
	for _, test := range []struct {
		question AddTextPrompt
		value    string
		want     error
	}{
		{question: prompter.questions[0], value: "", want: errors.New("Name is required")},
		{question: prompter.questions[0], value: "two words", want: errors.New("Name cannot contain spaces")},
		{question: prompter.questions[1], value: "", want: errors.New("Base URL is required")},
		{question: prompter.questions[2], value: "", want: errors.New("Model is required")},
		{question: prompter.questions[3], value: "", want: errors.New("API key is required")},
	} {
		if err := test.question.Validate(test.value); !errors.Is(err, test.want) && (err == nil || err.Error() != test.want.Error()) {
			t.Fatalf("Validate(%q) = %v, want %v", test.value, err, test.want)
		}
	}
}

type cmAddPromptResponse struct {
	value     string
	cancelled bool
	err       error
}

type scriptedCMAddPrompter struct {
	responses []cmAddPromptResponse
	questions []AddTextPrompt
	calls     []string
}

func (prompter *scriptedCMAddPrompter) Text(question AddTextPrompt) (string, bool, error) {
	prompter.calls = append(prompter.calls, "text:"+question.Message)
	prompter.questions = append(prompter.questions, question)
	return prompter.next()
}

func (prompter *scriptedCMAddPrompter) Password(question AddTextPrompt) (string, bool, error) {
	prompter.calls = append(prompter.calls, "password:"+question.Message)
	prompter.questions = append(prompter.questions, question)
	return prompter.next()
}

func (prompter *scriptedCMAddPrompter) next() (string, bool, error) {
	if len(prompter.responses) == 0 {
		return "", true, nil
	}
	response := prompter.responses[0]
	prompter.responses = prompter.responses[1:]
	return response.value, response.cancelled, response.err
}
