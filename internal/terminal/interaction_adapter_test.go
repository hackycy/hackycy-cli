package terminal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRuntimeAskRejectsInvalidRequestBeforeStartingRichOrRecordingTranscript(t *testing.T) {
	var diagnostics bytes.Buffer
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: RichInteractive},
		Input:        strings.NewReader("ignored\n"),
		Diagnostics:  &diagnostics,
	})
	run := runtime.Open(context.Background()).(*runtimeRun)

	_, err := run.Ask(InteractionRequest{Kind: InteractionSelect, Message: "Choose", Options: []InteractionOption{
		{Label: "One", Value: "duplicate"},
		{Label: "Two", Value: "duplicate"},
	}})
	if !errors.Is(err, ErrInvalidInteractionRequest) {
		t.Fatalf("Ask() error = %v, want ErrInvalidInteractionRequest", err)
	}
	if run.controller != nil {
		t.Fatal("invalid Ask() started a Rich controller")
	}
	if events := run.ledger.Events(); len(events) != 0 {
		t.Fatalf("invalid Ask() recorded transcript events: %#v", events)
	}
	if got := diagnostics.String(); got != "" {
		t.Fatalf("invalid Ask() wrote diagnostics: %q", got)
	}
}

func TestAutomationRunDoesNotRecordInteractionTranscript(t *testing.T) {
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: Automation},
		Output:       &output,
		Diagnostics:  &diagnostics,
	})
	run := runtime.Open(context.Background()).(*runtimeRun)

	if err := run.Milestone(PresentationDocument{Blocks: []PresentationBlock{{Text: "checkpoint"}}}); err != nil {
		t.Fatalf("Milestone() error = %v", err)
	}
	updates := make(chan OperationPhase, 2)
	updates <- OperationPhase{ID: "work", State: PhaseActive}
	updates <- OperationPhase{ID: "work", State: PhaseCompleted}
	close(updates)
	if err := run.Track(TrackedOperation{
		Label:   "Work",
		Phases:  []PhaseDefinition{{ID: "work", Name: "Work"}},
		Updates: updates,
	}); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if err := run.Finish(Succeeded, &PresentationDocument{Blocks: []PresentationBlock{{Text: "result"}}}); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if events := run.ledger.Events(); len(events) != 0 {
		t.Fatalf("Automation transcript events = %#v, want none", events)
	}
	if got, want := output.String(), "result\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := diagnostics.String(); got != "" {
		t.Fatalf("diagnostics = %q, want none", got)
	}
}
