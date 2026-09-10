package terminal_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRecordingExperienceCapturesOnlyTerminalSemantics(t *testing.T) {
	updates := make(chan terminal.OperationPhase)
	close(updates)
	request := terminal.InteractionRequest{
		Kind:    terminal.InteractionSelect,
		Message: "Choose a project",
		Options: []terminal.InteractionOption{{Label: "CLI", Value: "cli"}},
	}
	document := terminal.PresentationDocument{
		Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleSuccess, Text: "Done"}},
	}
	operation := terminal.TrackedOperation{
		Label:   "Scan repositories",
		Updates: updates,
	}
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminal.InteractionAnswer{Value: "cli"}},
	)

	var _ terminal.Experience = experience
	var _ terminal.ExperienceRun = experience.Run

	run := experience.Open(context.Background())
	answer, err := run.Ask(request)
	if err != nil || answer.Value != "cli" {
		t.Fatalf("Ask() = (%#v, %v)", answer, err)
	}
	if err := run.Notice(document); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if err := run.Track(operation); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if err := run.ResultCheckpoint("startup", document); err != nil {
		t.Fatalf("ResultCheckpoint() error = %v", err)
	}
	if err := run.Result(document); err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	want := []terminaltest.Operation{
		{Kind: terminaltest.AskOperation, Value: request},
		{Kind: terminaltest.NoticeOperation, Value: document},
		{Kind: terminaltest.TrackOperation, Value: operation},
		{Kind: terminaltest.ResultCheckpointOperation, Value: terminaltest.Checkpoint{ID: "startup", Document: document}},
		{Kind: terminaltest.ResultOperation, Value: document},
		{Kind: terminaltest.CloseOperation},
	}
	if got := experience.Run.Operations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("operations = %#v, want %#v", got, want)
	}
}
