package terminal

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestResultCheckpointWritesDistinctIDsWithoutFinishingRun(t *testing.T) {
	var output bytes.Buffer
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: PlainInteractive},
		Output:       &output,
	})
	run := runtime.Open(context.Background())
	document := PresentationDocument{Blocks: []PresentationBlock{{Text: "checkpoint"}}}

	if err := run.ResultCheckpoint("fs-startup", document); err != nil {
		t.Fatalf("first ResultCheckpoint() error = %v", err)
	}
	if err := run.ResultCheckpoint("fs-stopped", document); err != nil {
		t.Fatalf("second ResultCheckpoint() error = %v", err)
	}
	if err := run.Notice(PresentationDocument{Blocks: []PresentationBlock{{Text: "still active"}}}); err != nil {
		t.Fatalf("Notice() after checkpoints = %v, want active run", err)
	}
	if got, want := output.String(), "checkpoint\ncheckpoint\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestResultCheckpointRejectsBlankAndDuplicateIDs(t *testing.T) {
	var output bytes.Buffer
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: PlainInteractive},
		Output:       &output,
	})
	run := runtime.Open(context.Background())
	document := PresentationDocument{Blocks: []PresentationBlock{{Text: "checkpoint"}}}

	for _, id := range []string{"", "   "} {
		if err := run.ResultCheckpoint(id, document); !errors.Is(err, ErrInvalidResultCheckpoint) {
			t.Fatalf("ResultCheckpoint(%q) error = %v, want ErrInvalidResultCheckpoint", id, err)
		}
	}
	if err := run.ResultCheckpoint("startup", document); err != nil {
		t.Fatalf("initial ResultCheckpoint() error = %v", err)
	}
	if err := run.ResultCheckpoint(" startup ", document); err != nil {
		t.Fatalf("distinct whitespace-bearing ID error = %v", err)
	}
	if err := run.ResultCheckpoint("startup", document); !errors.Is(err, ErrResultCheckpointEmitted) {
		t.Fatalf("duplicate ResultCheckpoint() error = %v, want ErrResultCheckpointEmitted", err)
	}
	if got, want := output.String(), "checkpoint\ncheckpoint\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestResultCheckpointConsumesIDAfterWriteFailureAndLeavesRunActive(t *testing.T) {
	writeFailure := errors.New("checkpoint output failed")
	output := &checkpointErrorWriter{err: writeFailure}
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: PlainInteractive},
		Output:       output,
	})
	run := runtime.Open(context.Background())
	document := PresentationDocument{Blocks: []PresentationBlock{{Text: "checkpoint"}}}

	if err := run.ResultCheckpoint("startup", document); !errors.Is(err, writeFailure) {
		t.Fatalf("failed ResultCheckpoint() error = %v, want write failure", err)
	}
	if err := run.ResultCheckpoint("startup", document); !errors.Is(err, ErrResultCheckpointEmitted) {
		t.Fatalf("retry ResultCheckpoint() error = %v, want ErrResultCheckpointEmitted", err)
	}
	if err := run.Notice(PresentationDocument{}); err != nil {
		t.Fatalf("Notice() after failed checkpoint = %v, want active run", err)
	}
	if output.calls != 1 {
		t.Fatalf("checkpoint writes = %d, want 1", output.calls)
	}
}

func TestResultCheckpointDoesNotEnterTranscriptOrRichRenderer(t *testing.T) {
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{
			Interaction: RichInteractive,
			Stdout:      StreamCapability{Terminal: true, Color: true},
		},
		Output: &bytes.Buffer{},
	})
	run := runtime.Open(context.Background()).(*runtimeRun)
	if err := run.ResultCheckpoint("startup", PresentationDocument{Blocks: []PresentationBlock{{Text: "checkpoint"}}}); err != nil {
		t.Fatalf("ResultCheckpoint() error = %v", err)
	}
	if run.controller != nil {
		t.Fatal("ResultCheckpoint() started a Rich renderer")
	}
	if events := run.ledger.Events(); len(events) != 0 {
		t.Fatalf("checkpoint entered transcript: %#v", events)
	}
}

func TestResultCheckpointRejectsFinishedAndClosedRuns(t *testing.T) {
	runtime := NewExperience(ExperienceOptions{Capabilities: Capabilities{Interaction: PlainInteractive}})
	run := runtime.Open(context.Background())
	if err := run.Finish(Succeeded, nil); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if err := run.ResultCheckpoint("late", PresentationDocument{}); !errors.Is(err, ErrExperienceRunFinished) {
		t.Fatalf("checkpoint after Finish() error = %v, want ErrExperienceRunFinished", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := run.ResultCheckpoint("closed", PresentationDocument{}); !errors.Is(err, ErrExperienceRunClosed) {
		t.Fatalf("checkpoint after Close() error = %v, want ErrExperienceRunClosed", err)
	}
}

type checkpointErrorWriter struct {
	err   error
	calls int
}

func (writer *checkpointErrorWriter) Write([]byte) (int, error) {
	writer.calls++
	return 0, writer.err
}
