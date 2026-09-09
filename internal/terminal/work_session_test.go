package terminal

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestControlledWorkSessionAllowsDeclaredFormAndRetainsTranscriptOrder(t *testing.T) {
	var diagnostics bytes.Buffer
	run, err := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: PlainInteractive},
		Input:        strings.NewReader("continue\n"),
		Diagnostics:  &diagnostics,
	}).OpenConsole(context.Background(), ConsoleDescriptor{
		Command: "YCY / workflow",
		FormCatalog: []ConsoleFormStep{{
			ID:   "approval",
			Name: "Continue",
		}},
	})
	if err != nil {
		t.Fatalf("OpenConsole() error = %v", err)
	}
	work, err := StartWork(run, WorkCatalog{
		ID:    "workflow",
		Label: "Workflow",
		Phases: []PhaseDefinition{
			{ID: "scan", Name: "Scan"},
			{ID: "build", Name: "Build"},
		},
	})
	if err != nil {
		t.Fatalf("StartWork() error = %v", err)
	}
	if err := work.Update(OperationPhase{ID: "scan", State: PhaseActive, Detail: "Scanning"}); err != nil {
		t.Fatalf("scan active update error = %v", err)
	}
	if err := work.Update(OperationPhase{ID: "scan", State: PhaseCompleted, Detail: "Found input"}); err != nil {
		t.Fatalf("scan completion update error = %v", err)
	}
	answer, err := run.Ask(InteractionRequest{
		Kind:            InteractionText,
		Message:         "Continue",
		ConsoleStepID:   "approval",
		TranscriptLabel: "Continue",
	})
	if err != nil || answer.Value != "continue" {
		t.Fatalf("Ask() = (%#v, %v)", answer, err)
	}
	if err := work.Update(OperationPhase{ID: "build", State: PhaseActive, Detail: "Building"}); err != nil {
		t.Fatalf("build active update error = %v", err)
	}
	if err := work.Update(OperationPhase{ID: "build", State: PhaseCompleted, Detail: "Built output"}); err != nil {
		t.Fatalf("build completion update error = %v", err)
	}
	if _, err := run.Ask(InteractionRequest{Kind: InteractionText, Message: "Undeclared", ConsoleStepID: "undeclared"}); !errors.Is(err, ErrUndeclaredWorkSessionForm) {
		t.Fatalf("undeclared form error = %v, want ErrUndeclaredWorkSessionForm", err)
	}
	if err := work.Close(); err != nil {
		t.Fatalf("WorkSession.Close() error = %v", err)
	}

	concrete := run.(*runtimeRun)
	events := concrete.ledger.Events()
	got := make([]TranscriptEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == TranscriptPhase || event.Kind == TranscriptAsk {
			got = append(got, event)
		}
	}
	want := []TranscriptEvent{
		{Kind: TranscriptPhase, Label: "Scan", Text: "Found input", PhaseID: "scan", State: PhaseCompleted},
		{Kind: TranscriptAsk, Label: "Continue", Text: "continue"},
		{Kind: TranscriptPhase, Label: "Build", Text: "Built output", PhaseID: "build", State: PhaseCompleted},
	}
	for index := range got {
		got[index].Sequence = 0
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("transcript events = %#v, want %#v", got, want)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestControlledWorkSessionRecordsLaterReachedPhasesAcrossOmittedCatalogEntries(t *testing.T) {
	var diagnostics bytes.Buffer
	run, err := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: PlainInteractive},
		Input:        strings.NewReader("continue\n"),
		Diagnostics:  &diagnostics,
	}).OpenConsole(context.Background(), ConsoleDescriptor{
		Command: "YCY / conditional workflow",
		FormCatalog: []ConsoleFormStep{{
			ID:   "approval",
			Name: "Continue",
		}},
	})
	if err != nil {
		t.Fatalf("OpenConsole() error = %v", err)
	}
	work, err := StartWork(run, WorkCatalog{
		ID:    "conditional-workflow",
		Label: "Conditional workflow",
		Phases: []PhaseDefinition{
			{ID: "inspect", Name: "Inspect"},
			{ID: "optional", Name: "Optional preparation"},
			{ID: "publish", Name: "Publish"},
		},
	})
	if err != nil {
		t.Fatalf("StartWork() error = %v", err)
	}
	if err := work.Update(OperationPhase{ID: "inspect", State: PhaseActive}); err != nil {
		t.Fatalf("inspect active update error = %v", err)
	}
	if err := work.Update(OperationPhase{ID: "inspect", State: PhaseCompleted, Detail: "Inspection complete"}); err != nil {
		t.Fatalf("inspect completion update error = %v", err)
	}
	if _, err := run.Ask(InteractionRequest{
		Kind:            InteractionText,
		Message:         "Continue",
		ConsoleStepID:   "approval",
		TranscriptLabel: "Continue",
	}); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if err := work.Update(OperationPhase{ID: "publish", State: PhaseActive}); err != nil {
		t.Fatalf("publish active update error = %v", err)
	}
	if err := work.Update(OperationPhase{ID: "publish", State: PhaseCompleted, Detail: "Published output"}); err != nil {
		t.Fatalf("publish completion update error = %v", err)
	}
	if err := work.Close(); err != nil {
		t.Fatalf("WorkSession.Close() error = %v", err)
	}

	concrete := run.(*runtimeRun)
	events := concrete.ledger.Events()
	got := make([]TranscriptEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == TranscriptPhase || event.Kind == TranscriptAsk {
			got = append(got, event)
		}
	}
	want := []TranscriptEvent{
		{Kind: TranscriptPhase, Label: "Inspect", Text: "Inspection complete", PhaseID: "inspect", State: PhaseCompleted},
		{Kind: TranscriptAsk, Label: "Continue", Text: "continue"},
		{Kind: TranscriptPhase, Label: "Publish", Text: "Published output", PhaseID: "publish", State: PhaseCompleted},
	}
	for index := range got {
		got[index].Sequence = 0
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("transcript events = %#v, want %#v", got, want)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
