package add

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPromptAddCollectsLegacyQuestionsInOrder(t *testing.T) {
	prompter := &scriptedAddPrompter{
		texts: []promptResponse{
			{value: "work"},
			{value: "https://gitlab.example/path"},
		},
		selections: []promptResponse{
			{value: "gitlab"},
			{value: "https"},
		},
		passwords: []promptResponse{{value: " token-with-spaces "}},
	}

	input, cancelled, err := PromptAdd(prompter)
	if err != nil {
		t.Fatalf("PromptAdd() returned an error: %v", err)
	}
	if cancelled {
		t.Fatal("PromptAdd() reported cancellation")
	}
	want := AddInput{
		Alias:  "work",
		Host:   "https://gitlab.example/path",
		Type:   "gitlab",
		Scheme: "https",
		Token:  " token-with-spaces ",
	}
	if input != want {
		t.Fatalf("PromptAdd() = %#v, want %#v", input, want)
	}
	if got, want := prompter.calls, []string{
		"text:Instance name (alias)",
		"text:Host",
		"select:Provider type",
		"select:Protocol",
		"password:Access token",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt calls = %#v, want %#v", got, want)
	}
	if len(prompter.textQuestions) != 3 || prompter.textQuestions[0].Placeholder != "e.g. work, github, company-gl" || prompter.textQuestions[1].Placeholder != "e.g. gitlab.company.com, github.com" {
		t.Fatalf("text questions = %#v", prompter.textQuestions)
	}
	if len(prompter.selectQuestions) != 2 {
		t.Fatalf("select questions = %#v", prompter.selectQuestions)
	}
	if got, want := prompter.selectQuestions[0].Choices, []Choice{{Value: "gitlab", Label: "GitLab"}, {Value: "github", Label: "GitHub"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("provider choices = %#v, want %#v", got, want)
	}
	if got, want := prompter.selectQuestions[1].Choices, []Choice{{Value: "https", Label: "HTTPS"}, {Value: "http", Label: "HTTP (self-hosted / no TLS)"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol choices = %#v, want %#v", got, want)
	}
}

func TestPromptAddStopsWhenAnyQuestionIsCancelled(t *testing.T) {
	tests := []struct {
		name      string
		prompter  scriptedAddPrompter
		wantCalls int
	}{
		{
			name:      "alias",
			prompter:  scriptedAddPrompter{texts: []promptResponse{{cancelled: true}}},
			wantCalls: 1,
		},
		{
			name: "host",
			prompter: scriptedAddPrompter{texts: []promptResponse{
				{value: "work"},
				{cancelled: true},
			}},
			wantCalls: 2,
		},
		{
			name: "provider",
			prompter: scriptedAddPrompter{
				texts:      []promptResponse{{value: "work"}, {value: "gitlab.example"}},
				selections: []promptResponse{{cancelled: true}},
			},
			wantCalls: 3,
		},
		{
			name: "protocol",
			prompter: scriptedAddPrompter{
				texts:      []promptResponse{{value: "work"}, {value: "gitlab.example"}},
				selections: []promptResponse{{value: "gitlab"}, {cancelled: true}},
			},
			wantCalls: 4,
		},
		{
			name: "token",
			prompter: scriptedAddPrompter{
				texts:      []promptResponse{{value: "work"}, {value: "gitlab.example"}},
				selections: []promptResponse{{value: "gitlab"}, {value: "https"}},
				passwords:  []promptResponse{{cancelled: true}},
			},
			wantCalls: 5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, cancelled, err := PromptAdd(&test.prompter)
			if err != nil {
				t.Fatalf("PromptAdd() returned an error: %v", err)
			}
			if !cancelled || input != (AddInput{}) {
				t.Fatalf("PromptAdd() = (%#v, %t), want an empty cancelled result", input, cancelled)
			}
			if got := len(test.prompter.calls); got != test.wantCalls {
				t.Fatalf("prompt call count = %d, want %d", got, test.wantCalls)
			}
		})
	}
}

func TestValidateAddInputMatchesTheLegacyValidationBoundary(t *testing.T) {
	valid := AddInput{
		Alias:  "work",
		Host:   "https://gitlab.example/path",
		Type:   "gitlab",
		Scheme: "http",
		Token:  " token ",
	}
	if err := ValidateAddInput(valid); err != nil {
		t.Fatalf("ValidateAddInput(%#v) returned an error: %v", valid, err)
	}
	if valid.Host != "https://gitlab.example/path" || valid.Token != " token " {
		t.Fatalf("ValidateAddInput() changed input: %#v", valid)
	}

	for _, test := range []struct {
		name  string
		input AddInput
		want  string
	}{
		{name: "missing alias", input: AddInput{Host: "host", Type: "gitlab", Scheme: "https", Token: "token"}, want: "Name is required"},
		{name: "whitespace alias", input: AddInput{Alias: "work name", Host: "host", Type: "gitlab", Scheme: "https", Token: "token"}, want: "Name cannot contain spaces"},
		{name: "missing host", input: AddInput{Alias: "work", Host: " \t", Type: "gitlab", Scheme: "https", Token: "token"}, want: "Host is required"},
		{name: "missing token", input: AddInput{Alias: "work", Host: "host", Type: "gitlab", Scheme: "https", Token: " \n"}, want: "Token is required"},
		{name: "unexpected provider", input: AddInput{Alias: "work", Host: "host", Type: "other", Scheme: "https", Token: "token"}, want: "invalid provider type"},
		{name: "unexpected protocol", input: AddInput{Alias: "work", Host: "host", Type: "gitlab", Scheme: "ssh", Token: "token"}, want: "invalid protocol"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAddInput(test.input)
			if err == nil || err.Error() != test.want {
				t.Fatalf("ValidateAddInput(%#v) = %v, want %q", test.input, err, test.want)
			}
		})
	}
}

type promptResponse struct {
	value     string
	cancelled bool
	err       error
}

type scriptedAddPrompter struct {
	texts           []promptResponse
	selections      []promptResponse
	passwords       []promptResponse
	textQuestions   []TextPrompt
	selectQuestions []SelectPrompt
	calls           []string
}

func (prompter *scriptedAddPrompter) Text(question TextPrompt) (string, bool, error) {
	prompter.calls = append(prompter.calls, "text:"+question.Message)
	prompter.textQuestions = append(prompter.textQuestions, question)
	return prompter.next(&prompter.texts)
}

func (prompter *scriptedAddPrompter) Select(question SelectPrompt) (string, bool, error) {
	prompter.calls = append(prompter.calls, "select:"+question.Message)
	prompter.selectQuestions = append(prompter.selectQuestions, question)
	return prompter.next(&prompter.selections)
}

func (prompter *scriptedAddPrompter) Password(question TextPrompt) (string, bool, error) {
	prompter.calls = append(prompter.calls, "password:"+question.Message)
	prompter.textQuestions = append(prompter.textQuestions, question)
	return prompter.next(&prompter.passwords)
}

func (prompter *scriptedAddPrompter) next(responses *[]promptResponse) (string, bool, error) {
	if len(*responses) == 0 {
		return "", true, nil
	}
	response := (*responses)[0]
	*responses = (*responses)[1:]
	return response.value, response.cancelled, response.err
}

func TestTextPromptValidationFunctionsExposeLegacyMessages(t *testing.T) {
	prompter := &scriptedAddPrompter{
		texts:      []promptResponse{{value: "work"}, {value: "host"}},
		selections: []promptResponse{{value: "gitlab"}, {value: "https"}},
		passwords:  []promptResponse{{value: "token"}},
	}
	_, _, err := PromptAdd(prompter)
	if err != nil {
		t.Fatalf("PromptAdd() returned an error: %v", err)
	}
	for _, test := range []struct {
		question TextPrompt
		value    string
		want     error
	}{
		{question: prompter.textQuestions[0], value: "", want: errors.New("Name is required")},
		{question: prompter.textQuestions[0], value: "two words", want: errors.New("Name cannot contain spaces")},
		{question: prompter.textQuestions[1], value: "", want: errors.New("Host is required")},
		{question: prompter.textQuestions[2], value: "", want: errors.New("Token is required")},
	} {
		if err := test.question.Validate(test.value); !errors.Is(err, test.want) && (err == nil || err.Error() != test.want.Error()) {
			t.Fatalf("Validate(%q) = %v, want %v", test.value, err, test.want)
		}
	}
	if err := prompter.textQuestions[1].Validate(" https://example.test "); err != nil {
		t.Fatalf("host validation returned an error: %v", err)
	}
	if strings.TrimSpace(" token ") == "" {
		t.Fatal("test token unexpectedly empty")
	}
}
