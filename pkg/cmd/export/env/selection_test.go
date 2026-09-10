package env

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectUsesExplicitEnvironmentAndMergeOrder(t *testing.T) {
	discovery := Discovery{
		Directory:        "/project",
		BaseFile:         ".env",
		EnvironmentFiles: []string{".env.production"},
	}

	got, err := Select(discovery, SelectionOptions{Environment: "production", Merge: true}, nil)

	if err != nil {
		t.Fatalf("Select returned an error: %v", err)
	}
	want := Selection{Files: []string{".env", ".env.production"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select() = %#v, want %#v", got, want)
	}
}

func TestSelectPromptsWithLegacyChoices(t *testing.T) {
	discovery := Discovery{
		Directory:        "/project",
		BaseFile:         ".env",
		EnvironmentFiles: []string{".env.local", ".env.production"},
	}
	selector := &recordingSelector{value: ".env.production"}

	got, err := Select(discovery, SelectionOptions{}, selector)

	if err != nil {
		t.Fatalf("Select returned an error: %v", err)
	}
	want := Selection{Files: []string{".env.production"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select() = %#v, want %#v", got, want)
	}
	if selector.message != "Select environment" {
		t.Fatalf("selector message = %q, want %q", selector.message, "Select environment")
	}
	wantChoices := []EnvironmentChoice{
		{Value: ".env", Label: "default"},
		{Value: ".env.local", Label: "local"},
		{Value: ".env.production", Label: "production"},
	}
	if !reflect.DeepEqual(selector.choices, wantChoices) {
		t.Fatalf("selector choices = %#v, want %#v", selector.choices, wantChoices)
	}
}

func TestSelectUsesBaseWithoutPromptWhenItIsTheOnlyChoice(t *testing.T) {
	discovery := Discovery{Directory: "/project", BaseFile: ".env", EnvironmentFiles: []string{}}

	for _, options := range []SelectionOptions{{}, {Merge: true}} {
		got, err := Select(discovery, options, nil)

		if err != nil {
			t.Fatalf("Select returned an error: %v", err)
		}
		want := Selection{Files: []string{".env"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Select() = %#v, want %#v", got, want)
		}
	}
}

func TestSelectUsesOneEnvironmentWithoutBaseWithoutPrompt(t *testing.T) {
	discovery := Discovery{Directory: "/project", EnvironmentFiles: []string{".env.production"}}

	got, err := Select(discovery, SelectionOptions{}, nil)

	if err != nil {
		t.Fatalf("Select returned an error: %v", err)
	}
	want := Selection{Files: []string{".env.production"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select() = %#v, want %#v", got, want)
	}
}

func TestSelectHidesBaseWhenMergingAndReturnsCancellation(t *testing.T) {
	discovery := Discovery{
		Directory:        "/project",
		BaseFile:         ".env",
		EnvironmentFiles: []string{".env.local", ".env.production"},
	}
	selector := &recordingSelector{cancel: true}

	got, err := Select(discovery, SelectionOptions{Merge: true}, selector)

	if err != nil {
		t.Fatalf("Select returned an error: %v", err)
	}
	want := Selection{Files: []string{}, Cancelled: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select() = %#v, want %#v", got, want)
	}
	wantChoices := []EnvironmentChoice{
		{Value: ".env.local", Label: "local"},
		{Value: ".env.production", Label: "production"},
	}
	if !reflect.DeepEqual(selector.choices, wantChoices) {
		t.Fatalf("selector choices = %#v, want %#v", selector.choices, wantChoices)
	}
}

func TestSelectReturnsSelectorFailureBeforeChoosingFiles(t *testing.T) {
	failure := errors.New("interactive terminal unavailable")
	discovery := Discovery{
		Directory:        "/project",
		BaseFile:         ".env",
		EnvironmentFiles: []string{".env.production"},
	}

	selection, err := Select(discovery, SelectionOptions{}, &recordingSelector{err: failure})

	if !reflect.DeepEqual(selection, Selection{}) || !errors.Is(err, failure) {
		t.Fatalf("Select() = (%#v, %v), want selector failure", selection, err)
	}
}

func TestSelectRejectsMissingExplicitEnvironment(t *testing.T) {
	discovery := Discovery{Directory: "/project", EnvironmentFiles: []string{".env.local"}}

	_, err := Select(discovery, SelectionOptions{Environment: "production"}, nil)

	if err == nil {
		t.Fatal("Select returned nil error")
	}
	if want := "No .env.production file found in /project"; err.Error() != want {
		t.Fatalf("Select error = %q, want %q", err, want)
	}
}

type recordingSelector struct {
	message string
	choices []EnvironmentChoice
	value   string
	cancel  bool
	err     error
}

func (selector *recordingSelector) SelectEnvironment(message string, choices []EnvironmentChoice) (string, bool, error) {
	selector.message = message
	selector.choices = choices
	return selector.value, selector.cancel, selector.err
}
