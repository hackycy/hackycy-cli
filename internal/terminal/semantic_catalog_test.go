package terminal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOpenConsoleNormalizesAndRetainsCompleteFormCatalog(t *testing.T) {
	runtime := NewExperience(ExperienceOptions{})
	run, err := runtime.OpenConsole(context.Background(), ConsoleDescriptor{
		Command: "YCY CONFIG",
		FormCatalog: []ConsoleFormStep{
			{ID: " workspace ", Name: " Workspace\nname ", Detail: " repository\tcontext "},
			{ID: "token", Name: "Access token", Sensitive: true},
		},
	})
	if err != nil {
		t.Fatalf("OpenConsole() error = %v", err)
	}
	concrete := run.(*runtimeRun)
	want := []ConsoleFormStep{
		{ID: "workspace", Name: "Workspace name", Detail: "repository context"},
		{ID: "token", Name: "Access token", Sensitive: true},
	}
	if got := concrete.console.FormCatalog; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("FormCatalog = %#v, want %#v", got, want)
	}
}

func TestOpenConsoleRejectsUnsafeFormCatalogShape(t *testing.T) {
	runtime := NewExperience(ExperienceOptions{})
	cases := []ConsoleDescriptor{
		{Command: "YCY", FormCatalog: []ConsoleFormStep{{ID: "same", Name: "one"}, {ID: "same", Name: "two"}}},
		{Command: "YCY", FormCatalog: []ConsoleFormStep{{Name: "missing ID"}}},
		{Command: "YCY", FormCatalog: []ConsoleFormStep{{ID: "missing name"}}},
	}
	for index, descriptor := range cases {
		if _, err := runtime.OpenConsole(context.Background(), descriptor); !errors.Is(err, ErrInvalidConsoleDescriptor) {
			t.Errorf("case %d error = %v, want ErrInvalidConsoleDescriptor", index, err)
		}
	}
	tooMany := ConsoleDescriptor{Command: "YCY", FormCatalog: make([]ConsoleFormStep, maxConsoleFormSteps+1)}
	for index := range tooMany.FormCatalog {
		tooMany.FormCatalog[index] = ConsoleFormStep{ID: "step-" + strings.Repeat("x", index%3+1), Name: "Step"}
	}
	if _, err := runtime.OpenConsole(context.Background(), tooMany); !errors.Is(err, ErrInvalidConsoleDescriptor) {
		t.Fatalf("too many catalog steps error = %v, want ErrInvalidConsoleDescriptor", err)
	}
}

func TestFinishRequestNormalizesSafeSummaryWithoutDerivingResult(t *testing.T) {
	var output bytes.Buffer
	run := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: PlainInteractive},
		Output:       &output,
	}).Open(context.Background())
	request := FinishRequest{
		Outcome:  Succeeded,
		Location: " phase\nwrite ",
		Summary: PresentationDocument{Blocks: []PresentationBlock{
			{Role: VisualRoleSuccess, Text: "saved\x1b[2K", Sensitive: false},
			{Role: VisualRoleMuted, Text: "credential", Sensitive: true},
		}},
	}
	result := PresentationDocument{Blocks: []PresentationBlock{{Text: "durable\nresult"}}}
	if err := run.Finish(request, &result); err != nil {
		t.Fatalf("Finish(FinishRequest) error = %v", err)
	}
	if got, want := output.String(), "durable\nresult\n"; got != want {
		t.Fatalf("result output = %q, want %q", got, want)
	}
	if err := run.Finish(request); !errors.Is(err, ErrExperienceRunFinished) {
		t.Fatalf("second Finish() error = %v, want ErrExperienceRunFinished", err)
	}
}

func TestFinishRequestRejectsInvalidSemanticValues(t *testing.T) {
	run := NewExperience(ExperienceOptions{Capabilities: Capabilities{Interaction: PlainInteractive}}).Open(context.Background())
	if err := run.Finish(FinishRequest{Outcome: FinishOutcome(99)}); !errors.Is(err, ErrInvalidFinishRequest) {
		t.Fatalf("invalid request error = %v, want ErrInvalidFinishRequest", err)
	}
	if err := run.Finish(Succeeded, nil, nil); !errors.Is(err, ErrInvalidFinishRequest) {
		t.Fatalf("duplicate legacy documents error = %v, want ErrInvalidFinishRequest", err)
	}
	if err := run.Finish(FinishOutcome(99), nil); !errors.Is(err, ErrInvalidFinishOutcome) {
		t.Fatalf("invalid legacy outcome error = %v, want ErrInvalidFinishOutcome", err)
	}
}
