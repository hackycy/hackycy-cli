package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRuntimeRecoversStoppedRichRendererAndBlocksReplay(t *testing.T) {
	rendererErr := errors.New("renderer failed")
	var diagnostics bytes.Buffer
	var output bytes.Buffer
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: RichInteractive},
		Diagnostics:  &diagnostics,
		Input:        strings.NewReader("unexpected input\n"),
		Output:       &output,
	})
	run := runtime.Open(context.Background()).(*runtimeRun)
	run.ledger.Append(TranscriptEvent{Kind: TranscriptMilestone, Text: "safe checkpoint"})

	controller := &richController{
		runtime: runtime,
		done:    make(chan struct{}),
		err:     rendererErr,
		lease:   runtime.diagnostics.AcquireRendererLease(),
	}
	close(controller.done)
	run.controller = controller

	err := run.Notice(PresentationDocument{Blocks: []PresentationBlock{{Text: "unused"}}})
	if !errors.Is(err, rendererErr) {
		t.Fatalf("Notice() error = %v, want renderer failure", err)
	}
	if got, want := diagnostics.String(), "WORK\nsafe checkpoint\n"; got != want {
		t.Fatalf("replayed diagnostics = %q, want %q", got, want)
	}
	if run.controller != nil || !run.richDisabled || run.richFailure == nil {
		t.Fatalf("renderer recovery state = controller=%v disabled=%v failure=%v", run.controller, run.richDisabled, run.richFailure)
	}
	if _, err := io.WriteString(runtime.DiagnosticWriter(), "after recovery\n"); err != nil {
		t.Fatalf("diagnostic after recovery = %v", err)
	}
	if got, want := diagnostics.String(), "WORK\nsafe checkpoint\nafter recovery\n"; got != want {
		t.Fatalf("diagnostics after lease release = %q, want %q", got, want)
	}

	if _, err := run.Ask(InteractionRequest{Kind: InteractionText, Message: "Retry?"}); !errors.Is(err, rendererErr) {
		t.Fatalf("Ask() after renderer failure = %v, want original failure", err)
	}
	if err := run.Finish(Succeeded, &PresentationDocument{Blocks: []PresentationBlock{{Text: "result"}}}); !errors.Is(err, rendererErr) {
		t.Fatalf("Finish() after renderer failure = %v, want original failure", err)
	}
	if got := diagnostics.String(); strings.Contains(got, "unused") || strings.Contains(got, "result") {
		t.Fatalf("recovery emitted repeated semantic work: %q", got)
	}
	if got, want := output.String(), "result\n"; got != want {
		t.Fatalf("Finish() fallback result = %q, want %q", got, want)
	}
	if err := run.Finish(Failed, &PresentationDocument{Blocks: []PresentationBlock{{Text: "retry"}}}); !errors.Is(err, ErrExperienceRunFinished) {
		t.Fatalf("second Finish() after renderer failure = %v, want finished run", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() after recovery = %v", err)
	}
}

func TestRuntimeRecoversRendererTerminationDuringOutcomeDwell(t *testing.T) {
	rendererErr := errors.New("renderer stopped during outcome")
	var diagnostics bytes.Buffer
	var output bytes.Buffer
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: RichInteractive},
		Diagnostics:  &diagnostics,
		Input:        strings.NewReader("unexpected input\n"),
		Output:       &output,
	})
	run := runtime.Open(context.Background()).(*runtimeRun)
	controller := &richController{
		runtime: runtime,
		done:    make(chan struct{}),
		err:     rendererErr,
		lease:   runtime.diagnostics.AcquireRendererLease(),
	}
	close(controller.done)
	run.controller = controller

	err := run.Finish(FinishRequest{
		Outcome:  Failed,
		Location: "write profile",
		Summary:  PresentationDocument{Blocks: []PresentationBlock{{Text: "safe failure summary"}}},
	}, &PresentationDocument{Blocks: []PresentationBlock{{Text: "durable failure result"}}})
	if !errors.Is(err, rendererErr) {
		t.Fatalf("Finish() error = %v, want renderer failure", err)
	}
	if got, want := diagnostics.String(), "AT       write profile\nOUTCOME  failed: safe failure summary\n"; got != want {
		t.Fatalf("recovered outcome transcript = %q, want %q", got, want)
	}
	if got, want := output.String(), "durable failure result\n"; got != want {
		t.Fatalf("recovered durable Result = %q, want %q", got, want)
	}
	if run.controller != nil || !run.richDisabled || !errors.Is(run.richFailure, rendererErr) {
		t.Fatalf("outcome recovery state = controller=%v disabled=%v failure=%v", run.controller, run.richDisabled, run.richFailure)
	}
	if err := run.Finish(Failed, nil); !errors.Is(err, ErrExperienceRunFinished) {
		t.Fatalf("second Finish() error = %v, want ErrExperienceRunFinished", err)
	}
}
