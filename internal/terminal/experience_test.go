package terminal_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestExperienceRoutesPresentationAndInteractionToSeparateStreams(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("project\n"),
		Output:       &stdout,
		Diagnostics:  &stderr,
	})
	run := experience.Open(context.Background())

	if err := run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "context"}}}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	answer, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "Project"})
	if err != nil || answer.Value != "project" {
		t.Fatalf("Ask() = (%#v, %v)", answer, err)
	}
	if _, err := io.WriteString(experience.DiagnosticWriter(), "diagnostic\n"); err != nil {
		t.Fatalf("diagnostic write error = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "result"}}}); err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got, want := stdout.String(), "result\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "context\nProject: diagnostic\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestPlainExperienceTrackDoesNotBufferDiagnostics(t *testing.T) {
	stderr := newPromptBuffer("Scanning")
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Diagnostics:  stderr,
	})
	run := experience.Open(context.Background())
	updates := make(chan terminal.OperationPhase)
	trackDone := make(chan error, 1)
	go func() {
		trackDone <- run.Track(terminal.TrackedOperation{Updates: updates})
	}()
	updates <- terminal.OperationPhase{Name: "Scanning", State: terminal.PhaseActive}
	select {
	case <-stderr.prompt:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for tracked phase: %q", stderr.String())
	}
	if _, err := io.WriteString(experience.DiagnosticWriter(), "deferred diagnostic\n"); err != nil {
		t.Fatalf("diagnostic write error = %v", err)
	}
	if got, want := stderr.String(), "Scanning\ndeferred diagnostic\n"; got != want {
		t.Fatalf("stderr during track = %q, want %q", got, want)
	}
	close(updates)
	select {
	case err := <-trackDone:
		if err != nil {
			t.Fatalf("Track() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Track() did not return after updates closed")
	}
	if got, want := stderr.String(), "Scanning\ndeferred diagnostic\n"; got != want {
		t.Fatalf("stderr after track = %q, want %q", got, want)
	}
}

func TestPlainExperienceAskDoesNotBufferDiagnostics(t *testing.T) {
	input := newBlockingReader("project\n")
	stderr := newPromptBuffer("Project:")
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        input,
		Diagnostics:  stderr,
	})
	run := experience.Open(context.Background())
	answerDone := make(chan struct {
		answer terminal.InteractionAnswer
		err    error
	}, 1)
	go func() {
		answer, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "Project"})
		answerDone <- struct {
			answer terminal.InteractionAnswer
			err    error
		}{answer: answer, err: err}
	}()
	select {
	case <-input.started:
	case <-time.After(time.Second):
		t.Fatal("Ask() did not begin reading input")
	}
	if _, err := io.WriteString(experience.DiagnosticWriter(), "deferred diagnostic\n"); err != nil {
		t.Fatalf("diagnostic write error = %v", err)
	}
	if got, want := stderr.String(), "Project: deferred diagnostic\n"; got != want {
		t.Fatalf("stderr during ask = %q, want %q", got, want)
	}
	close(input.release)
	select {
	case result := <-answerDone:
		if result.err != nil || result.answer.Value != "project" {
			t.Fatalf("Ask() = (%#v, %v)", result.answer, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Ask() did not return after input became available")
	}
	if got, want := stderr.String(), "Project: deferred diagnostic\n"; got != want {
		t.Fatalf("stderr after ask = %q, want %q", got, want)
	}
}

func TestExperienceRunRejectsUseAfterClose(t *testing.T) {
	experience := terminal.NewExperience(terminal.ExperienceOptions{Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive}})
	run := experience.Open(context.Background())
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{}); !errors.Is(err, terminal.ErrExperienceRunClosed) {
		t.Fatalf("Result() error = %v, want ErrExperienceRunClosed", err)
	}
	if _, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionText}); !errors.Is(err, terminal.ErrExperienceRunClosed) {
		t.Fatalf("Ask() error = %v, want ErrExperienceRunClosed", err)
	}
	if err := run.Track(terminal.TrackedOperation{}); !errors.Is(err, terminal.ErrExperienceRunClosed) {
		t.Fatalf("Track() error = %v, want ErrExperienceRunClosed", err)
	}
}

func TestExperienceFirstResultEndsInteractiveOperationsButAllowsMoreResults(t *testing.T) {
	var stdout bytes.Buffer
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Output:       &stdout,
	})
	run := experience.Open(context.Background())
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "first"}}}); err != nil {
		t.Fatalf("first Result() error = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "second"}}}); err != nil {
		t.Fatalf("second Result() error = %v", err)
	}
	if err := run.Notice(terminal.PresentationDocument{}); !errors.Is(err, terminal.ErrExperienceRunFinished) {
		t.Fatalf("Notice() error = %v, want ErrExperienceRunFinished", err)
	}
	if _, err := run.Ask(terminal.InteractionRequest{}); !errors.Is(err, terminal.ErrExperienceRunFinished) {
		t.Fatalf("Ask() error = %v, want ErrExperienceRunFinished", err)
	}
	if err := run.Track(terminal.TrackedOperation{}); !errors.Is(err, terminal.ErrExperienceRunFinished) {
		t.Fatalf("Track() error = %v, want ErrExperienceRunFinished", err)
	}
	if got, want := stdout.String(), "first\nsecond\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRichPreflightSizeFailureFallsBackToPlainWithoutChangingCapabilities(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatalf("create input: %v", err)
	}
	defer input.Close()
	if _, err := input.WriteString("project\n"); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind input: %v", err)
	}
	diagnostics, err := os.CreateTemp(t.TempDir(), "diagnostics")
	if err != nil {
		t.Fatalf("create diagnostics: %v", err)
	}
	defer diagnostics.Close()

	capabilities := terminal.Capabilities{
		Interaction: terminal.RichInteractive,
		Stdin:       terminal.StreamCapability{Terminal: true},
		Stderr:      terminal.StreamCapability{Terminal: true, Color: true},
	}
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: capabilities,
		Input:        input,
		Diagnostics:  diagnostics,
	})
	run := experience.Open(context.Background())
	answer, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "Project"})
	if err != nil || answer.Value != "project" {
		t.Fatalf("Ask() = (%#v, %v)", answer, err)
	}
	if err := run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "plain notice"}}}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	contents, err := os.ReadFile(diagnostics.Name())
	if err != nil {
		t.Fatalf("read diagnostics: %v", err)
	}
	if got, want := string(contents), "Project: plain notice\n"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
	if got := experience.Capabilities(); got != capabilities {
		t.Fatalf("Capabilities() = %#v, want %#v", got, capabilities)
	}
}

func TestResultWriteFailureStillEndsInteractionAndLaterResultsAreAttempted(t *testing.T) {
	first := errors.New("first result write")
	second := errors.New("second result write")
	output := &sequenceErrorWriter{errors: []error{first, second}}
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Output:       output,
	})
	run := experience.Open(context.Background())
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "first"}}}); !errors.Is(err, first) {
		t.Fatalf("first Result() error = %v", err)
	}
	if err := run.Notice(terminal.PresentationDocument{}); !errors.Is(err, terminal.ErrExperienceRunFinished) {
		t.Fatalf("Notice() error = %v, want ErrExperienceRunFinished", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "second"}}}); !errors.Is(err, second) {
		t.Fatalf("second Result() error = %v", err)
	}
	if got, want := output.writes, []string{"first\n", "second\n"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %#v, want %#v", got, want)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestExperienceFinishCommitsOneOutcomeAndNeverSynthesizesAnother(t *testing.T) {
	t.Run("writes a non-nil result once", func(t *testing.T) {
		var stdout bytes.Buffer
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Output:       &stdout,
		})
		run := experience.Open(context.Background())
		document := &terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "complete"}}}

		if err := run.Finish(terminal.Succeeded, document); err != nil {
			t.Fatalf("first Finish() error = %v", err)
		}
		if err := run.Finish(terminal.Failed, &terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "retry"}}}); !errors.Is(err, terminal.ErrExperienceRunFinished) {
			t.Fatalf("second Finish() error = %v, want ErrExperienceRunFinished", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got, want := stdout.String(), "complete\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("nil document has no stdout result", func(t *testing.T) {
		var stdout bytes.Buffer
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Output:       &stdout,
		})
		run := experience.Open(context.Background())

		if err := run.Finish(terminal.Cancelled, nil); err != nil {
			t.Fatalf("Finish() error = %v", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got := stdout.String(); got != "" {
			t.Fatalf("stdout = %q, want no result", got)
		}
	})

	t.Run("write failure cannot be retried", func(t *testing.T) {
		writeFailure := errors.New("result write")
		output := &sequenceErrorWriter{errors: []error{writeFailure}}
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Output:       output,
		})
		run := experience.Open(context.Background())
		document := &terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "complete"}}}

		if err := run.Finish(terminal.Failed, document); !errors.Is(err, writeFailure) {
			t.Fatalf("first Finish() error = %v, want result write failure", err)
		}
		if err := run.Finish(terminal.Failed, document); !errors.Is(err, terminal.ErrExperienceRunFinished) {
			t.Fatalf("second Finish() error = %v, want ErrExperienceRunFinished", err)
		}
		if got, want := output.writes, []string{"complete\n"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("writes = %#v, want %#v", got, want)
		}
	})

	t.Run("rejects invalid outcomes without finishing", func(t *testing.T) {
		var stdout bytes.Buffer
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Output:       &stdout,
		})
		run := experience.Open(context.Background())
		document := &terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "complete"}}}

		if err := run.Finish(terminal.FinishOutcome(99), document); !errors.Is(err, terminal.ErrInvalidFinishOutcome) {
			t.Fatalf("invalid Finish() error = %v, want ErrInvalidFinishOutcome", err)
		}
		if err := run.Finish(terminal.Succeeded, document); err != nil {
			t.Fatalf("Finish() after invalid outcome error = %v", err)
		}
		if got, want := stdout.String(), "complete\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("close does not emit a synthetic outcome", func(t *testing.T) {
		var stdout bytes.Buffer
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Output:       &stdout,
		})
		run := experience.Open(context.Background())

		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got := stdout.String(); got != "" {
			t.Fatalf("stdout = %q, want no synthetic result", got)
		}
	})

	t.Run("compatibility result cannot follow Finish", func(t *testing.T) {
		var stdout bytes.Buffer
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Output:       &stdout,
		})
		run := experience.Open(context.Background())

		if err := run.Finish(terminal.Succeeded, nil); err != nil {
			t.Fatalf("Finish() error = %v", err)
		}
		if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "late"}}}); !errors.Is(err, terminal.ErrExperienceRunFinished) {
			t.Fatalf("Result() after Finish error = %v, want ErrExperienceRunFinished", err)
		}
		if got := stdout.String(); got != "" {
			t.Fatalf("stdout = %q, want no result", got)
		}
	})
}

func TestExperienceMilestoneRoutesByInteractionCapability(t *testing.T) {
	t.Run("plain writes one control-free diagnostic line", func(t *testing.T) {
		var diagnostics bytes.Buffer
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Diagnostics:  &diagnostics,
		})
		run := experience.Open(context.Background())
		document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "checkpoint\x1b[2K"}}}

		if err := run.Milestone(document); err != nil {
			t.Fatalf("Milestone() error = %v", err)
		}
		if got, want := diagnostics.String(), "checkpoint\n"; got != want {
			t.Fatalf("diagnostics = %q, want %q", got, want)
		}
	})

	t.Run("automation drops the checkpoint without touching streams", func(t *testing.T) {
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
			Diagnostics:  panicWriter{},
		})
		run := experience.Open(context.Background())
		if err := run.Milestone(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "checkpoint"}}}); err != nil {
			t.Fatalf("Milestone() error = %v", err)
		}
	})

	t.Run("finished and closed runs reject checkpoints", func(t *testing.T) {
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		})
		run := experience.Open(context.Background())
		if err := run.Finish(terminal.Succeeded, nil); err != nil {
			t.Fatalf("Finish() error = %v", err)
		}
		if err := run.Milestone(terminal.PresentationDocument{}); !errors.Is(err, terminal.ErrExperienceRunFinished) {
			t.Fatalf("Milestone() after Finish error = %v", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if err := run.Milestone(terminal.PresentationDocument{}); !errors.Is(err, terminal.ErrExperienceRunClosed) {
			t.Fatalf("Milestone() after Close error = %v", err)
		}
	})

	t.Run("empty checkpoint is a no-op", func(t *testing.T) {
		var diagnostics bytes.Buffer
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Diagnostics:  &diagnostics,
		})
		run := experience.Open(context.Background())
		if err := run.Milestone(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "\x1b[2K"}}}); err != nil {
			t.Fatalf("Milestone() error = %v", err)
		}
		if got := diagnostics.String(); got != "" {
			t.Fatalf("empty milestone wrote diagnostics: %q", got)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
}

func TestExperienceNonRichResultsAreAlwaysControlFree(t *testing.T) {
	for _, mode := range []terminal.InteractionMode{terminal.PlainInteractive, terminal.Automation} {
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			var output bytes.Buffer
			experience := terminal.NewExperience(terminal.ExperienceOptions{
				Capabilities: terminal.Capabilities{
					Interaction: mode,
					Stdout:      terminal.StreamCapability{Terminal: true, Color: true},
				},
				Output: &output,
			})
			run := experience.Open(context.Background())
			if err := run.Finish(terminal.Succeeded, &terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleTitle, Text: "done"}}}); err != nil {
				t.Fatalf("Finish() error = %v", err)
			}
			if got, want := output.String(), "done\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if terminaltest.ContainsTerminalControl(output.Bytes()) {
				t.Fatalf("stdout contains terminal controls: %q", output.String())
			}
		})
	}
}

func TestExperienceTrackValidatesPhaseProtocolAndDrainsUpdates(t *testing.T) {
	newRun := func(ctx context.Context, diagnostics io.Writer) terminal.ExperienceRun {
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
			Diagnostics:  diagnostics,
		})
		return experience.Open(ctx)
	}
	operation := func(updates <-chan terminal.OperationPhase) terminal.TrackedOperation {
		return terminal.TrackedOperation{
			ID:    "refresh",
			Label: "Refresh projects",
			Phases: []terminal.PhaseDefinition{
				{ID: "discover", Name: "Discover projects"},
				{ID: "fetch", Name: "Fetch projects"},
			},
			Updates: updates,
		}
	}

	t.Run("accepts ordered state changes by stable ID", func(t *testing.T) {
		updates := make(chan terminal.OperationPhase, 4)
		updates <- terminal.OperationPhase{ID: "discover", State: terminal.PhaseActive}
		updates <- terminal.OperationPhase{ID: "discover", State: terminal.PhaseCompleted}
		updates <- terminal.OperationPhase{ID: "fetch", State: terminal.PhaseActive}
		updates <- terminal.OperationPhase{ID: "fetch", State: terminal.PhaseCompleted}
		close(updates)
		var diagnostics bytes.Buffer

		if err := newRun(context.Background(), &diagnostics).Track(operation(updates)); err != nil {
			t.Fatalf("Track() error = %v", err)
		}
		if got, want := diagnostics.String(), "Discover projects\nDiscover projects\nFetch projects\nFetch projects\n"; got != want {
			t.Fatalf("diagnostics = %q, want %q", got, want)
		}
	})

	t.Run("returns a protocol error only after draining a bad stream", func(t *testing.T) {
		updates := make(chan terminal.OperationPhase)
		producerDone := make(chan struct{})
		go func() {
			updates <- terminal.OperationPhase{ID: "unknown", State: terminal.PhaseActive}
			updates <- terminal.OperationPhase{ID: "discover", State: terminal.PhaseActive}
			close(updates)
			close(producerDone)
		}()

		err := newRun(context.Background(), io.Discard).Track(operation(updates))
		if !errors.Is(err, terminal.ErrInvalidPhaseProtocol) {
			t.Fatalf("Track() error = %v, want ErrInvalidPhaseProtocol", err)
		}
		select {
		case <-producerDone:
		case <-time.After(time.Second):
			t.Fatal("Track() did not drain updates after the protocol error")
		}
	})

	for _, testCase := range []struct {
		name    string
		updates []terminal.OperationPhase
	}{
		{
			name: "rejects concurrent active phases",
			updates: []terminal.OperationPhase{
				{ID: "discover", State: terminal.PhaseActive},
				{ID: "fetch", State: terminal.PhaseActive},
			},
		},
		{
			name: "rejects terminal phase rewrites",
			updates: []terminal.OperationPhase{
				{ID: "discover", State: terminal.PhaseCompleted},
				{ID: "discover", State: terminal.PhaseActive},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			updates := make(chan terminal.OperationPhase, len(testCase.updates))
			for _, update := range testCase.updates {
				updates <- update
			}
			close(updates)

			err := newRun(context.Background(), io.Discard).Track(operation(updates))
			if !errors.Is(err, terminal.ErrInvalidPhaseProtocol) {
				t.Fatalf("Track() error = %v, want ErrInvalidPhaseProtocol", err)
			}
		})
	}

	t.Run("requests cancellation once and drains until closure", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		updates := make(chan terminal.OperationPhase, 1)
		calls := 0
		tracked := operation(updates)
		tracked.RequestCancel = func() {
			calls++
			updates <- terminal.OperationPhase{ID: "discover", State: terminal.PhaseCancelled}
			close(updates)
		}

		if err := newRun(ctx, io.Discard).Track(tracked); err != nil {
			t.Fatalf("Track() error = %v", err)
		}
		if calls != 1 {
			t.Fatalf("RequestCancel calls = %d, want 1", calls)
		}
	})

	t.Run("does not deadlock an unbuffered cancellation update", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		updates := make(chan terminal.OperationPhase)
		calls := 0
		tracked := operation(updates)
		tracked.RequestCancel = func() {
			calls++
			updates <- terminal.OperationPhase{ID: "discover", State: terminal.PhaseCancelled}
			close(updates)
		}

		done := make(chan error, 1)
		go func() { done <- newRun(ctx, io.Discard).Track(tracked) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Track() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Track() deadlocked while draining an unbuffered cancellation update")
		}
		if calls != 1 {
			t.Fatalf("RequestCancel calls = %d, want 1", calls)
		}
	})
}

type blockingReader struct {
	payload string
	started chan struct{}
	release chan struct{}
	once    sync.Once
	read    bool
}

func newBlockingReader(payload string) *blockingReader {
	return &blockingReader{
		payload: payload,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (reader *blockingReader) Read(buffer []byte) (int, error) {
	if reader.read {
		return 0, io.EOF
	}
	reader.once.Do(func() { close(reader.started) })
	<-reader.release
	reader.read = true
	return copy(buffer, reader.payload), nil
}
