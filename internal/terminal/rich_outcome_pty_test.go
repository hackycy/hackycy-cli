package terminal_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRichRuntimeConsoleDimensionsAndColorPTY(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_CONSOLE_MATRIX_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRichConsoleLifecyclePTYHelper(t, terminal.Succeeded)
		return
	}

	for _, testCase := range []struct {
		name          string
		width, height uint16
		color         bool
	}{
		{name: "120x40-colored", width: 120, height: 40, color: true},
		{name: "120x40-no-color", width: 120, height: 40, color: false},
		{name: "70x20-colored", width: 70, height: 20, color: true},
		{name: "70x20-no-color", width: 70, height: 20, color: false},
		{name: "40x15-colored", width: 40, height: 15, color: true},
		{name: "40x15-no-color", width: 40, height: 15, color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			extraEnvironment := ""
			if !testCase.color {
				extraEnvironment = "NO_COLOR=1"
			}
			output := runRichConsoleLifecyclePTY(t, "TestRichRuntimeConsoleDimensionsAndColorPTY", helperEnvironment, "1", extraEnvironment, testCase.width, testCase.height)
			assertRichConsoleLifecyclePTY(t, output, terminal.Succeeded, "safe target", "matrix safe summary", "matrix-durable-result", testCase.color)
		})
	}
}

func TestRichRuntimeFailedAndCancelledOutcomesPTY(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_CONSOLE_OUTCOME_HELPER"
	if value := os.Getenv(helperEnvironment); value != "" {
		switch value {
		case "failed":
			runRichConsoleLifecyclePTYHelper(t, terminal.Failed)
		case "cancelled":
			runRichConsoleLifecyclePTYHelper(t, terminal.Cancelled)
		default:
			t.Fatalf("unknown outcome helper mode %q", value)
		}
		return
	}

	for _, testCase := range []struct {
		name     string
		mode     string
		outcome  terminal.FinishOutcome
		location string
		summary  string
		color    bool
	}{
		{name: "failed", mode: "failed", outcome: terminal.Failed, location: "write projection", summary: "matrix failure summary", color: true},
		{name: "cancelled", mode: "cancelled", outcome: terminal.Cancelled, location: "write projection", summary: "matrix cancellation summary", color: true},
		{name: "cancelled-no-color", mode: "cancelled", outcome: terminal.Cancelled, location: "write projection", summary: "matrix cancellation summary", color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			extraEnvironment := ""
			if !testCase.color {
				extraEnvironment = "NO_COLOR=1"
			}
			output := runRichConsoleLifecyclePTY(t, "TestRichRuntimeFailedAndCancelledOutcomesPTY", helperEnvironment, testCase.mode, extraEnvironment, 70, 20)
			assertRichConsoleLifecyclePTY(t, output, testCase.outcome, testCase.location, testCase.summary, "matrix-durable-result", testCase.color)
		})
	}
}

func TestRichRuntimeContextInterruptsOutcomeDwellPTY(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_OUTCOME_CONTEXT_INTERRUPT_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRichOutcomeContextInterruptPTYHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRichRuntimeContextInterruptsOutcomeDwellPTY$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, output, readDone := startRichPTYTest(t, command, "context-interrupted-result")
	defer process.Close()
	respondToHuhTerminalQueries(t, process, output)
	finishRichPTYTest(t, process, readDone, output)

	text := output.String()
	assertTrackedPTYCleanup(t, text, "context-interrupted-result")
	exit := strings.LastIndex(text, "\x1b[?1049l")
	transcript := transcriptAfterPrimaryScreen(t, text, exit)
	plainTranscript := ansi.Strip(transcript)
	if !strings.Contains(plainTranscript, "AT       cancellation requested") || !strings.Contains(plainTranscript, "OUTCOME  cancelled: context interruption") {
		t.Fatalf("context interruption transcript = %q", transcript)
	}
	if outcome := strings.Index(plainTranscript, "OUTCOME  cancelled: context interruption"); outcome < 0 || strings.LastIndex(plainTranscript, "context-interrupted-result") < outcome {
		t.Fatalf("context interruption did not restore/replay before stdout result: %q", text)
	}
}

func runRichConsoleLifecyclePTY(t *testing.T, testName, helperEnvironment, helperValue, extraEnvironment string, width, height uint16) string {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"="+helperValue, "TERM=xterm-256color")
	for _, entry := range strings.Fields(extraEnvironment) {
		command.Env = append(command.Env, entry)
	}
	process, err := terminaltest.StartPTYWithSize(command, width, height)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start %dx%d PTY helper: %v", width, height, err)
	}
	defer process.Close()

	output := newPromptBuffer("Configure source")
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, process.Terminal())
		close(readDone)
	}()
	respondToHuhTerminalQueries(t, process, output)
	waitForTrackedPrompt(t, output, "Configure source")
	writeRichPTYInput(t, process, "\r")
	waitForTrackedPrompt(t, output, "Confirm execution")
	writeRichPTYInput(t, process, "\r")
	finishRichPTYTest(t, process, readDone, output)
	return output.String()
}

func runRichConsoleLifecyclePTYHelper(t *testing.T, outcome terminal.FinishOutcome) {
	t.Helper()
	color := os.Getenv("NO_COLOR") == ""
	location, summary := richConsoleOutcomeProjection(outcome)
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: richTestCapabilities(color),
		Input:        os.Stdin,
		Output:       os.Stdout,
		Diagnostics:  os.Stderr,
	})
	run, err := experience.OpenConsole(context.Background(), terminal.ConsoleDescriptor{
		Command: "YCY / G0 matrix",
		Target:  "safe target",
		Metadata: []terminal.ConsoleMetadata{{
			Label: "scope",
			Value: "shared lifecycle",
		}},
		FormCatalog: []terminal.ConsoleFormStep{
			{ID: "source", Name: "Source catalog", Detail: "choose source"},
			{ID: "confirm", Name: "Approval catalog", Detail: "confirm execution"},
		},
	})
	if err != nil {
		t.Fatalf("OpenConsole() error = %v", err)
	}
	defer run.Close()
	for _, request := range []terminal.InteractionRequest{
		{
			Kind:            terminal.InteractionConfirm,
			Message:         "Configure source",
			ConsoleStepID:   "source",
			TranscriptLabel: "Source selection",
			HasDefault:      true,
			Default:         terminal.InteractionAnswer{Confirmed: true},
		},
		{
			Kind:            terminal.InteractionConfirm,
			Message:         "Confirm execution",
			ConsoleStepID:   "confirm",
			TranscriptLabel: "Execution confirmation",
			HasDefault:      true,
			Default:         terminal.InteractionAnswer{Confirmed: true},
		},
	} {
		if _, err := run.Ask(request); err != nil {
			t.Fatalf("Ask(%q) error = %v", request.Message, err)
		}
	}

	finalState := terminal.PhaseCompleted
	finalDetail := "projection written"
	if outcome == terminal.Failed {
		finalState = terminal.PhaseFailed
		finalDetail = "projection failed"
	}
	if outcome == terminal.Cancelled {
		finalState = terminal.PhaseCancelled
		finalDetail = "execution cancelled"
	}
	updates := make(chan terminal.OperationPhase)
	go func() {
		updates <- terminal.OperationPhase{ID: "validate", State: terminal.PhaseActive, Detail: "validate request"}
		// Keep real work active for one Meter cycle so the PTY captures a live
		// frame rather than a scheduler-coalesced final screen.
		time.Sleep(spinner.Meter.FPS + 50*time.Millisecond)
		updates <- terminal.OperationPhase{ID: "validate", State: terminal.PhaseCompleted, Detail: "request validated"}
		updates <- terminal.OperationPhase{ID: "write", State: terminal.PhaseActive, Detail: "write projection"}
		updates <- terminal.OperationPhase{ID: "write", State: finalState, Detail: finalDetail}
		close(updates)
	}()
	if err := run.Track(terminal.TrackedOperation{
		ID:    "g0-matrix",
		Label: "Execute request",
		Phases: []terminal.PhaseDefinition{
			{ID: "validate", Name: "Validate request"},
			{ID: "write", Name: "Write projection"},
		},
		Updates: updates,
	}); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if err := run.Finish(terminal.FinishRequest{
		Outcome:  outcome,
		Location: location,
		Summary:  terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: summary}}},
	}, &terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "matrix-durable-result"}}}); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
}

func richConsoleOutcomeProjection(outcome terminal.FinishOutcome) (string, string) {
	switch outcome {
	case terminal.Failed:
		return "write projection", "matrix failure summary"
	case terminal.Cancelled:
		return "write projection", "matrix cancellation summary"
	default:
		return "safe target", "matrix safe summary"
	}
}

func runRichOutcomeContextInterruptPTYHelper(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: richTestCapabilities(true),
		Input:        os.Stdin,
		Output:       os.Stdout,
		Diagnostics:  os.Stderr,
	})
	run, err := experience.OpenConsole(ctx, terminal.ConsoleDescriptor{
		Command: "YCY / G0 dwell",
		Target:  "cancellation requested",
		FormCatalog: []terminal.ConsoleFormStep{{
			ID:     "prepare",
			Name:   "Prepare outcome",
			Detail: "await cancellation",
		}},
	})
	if err != nil {
		t.Fatalf("OpenConsole() error = %v", err)
	}
	defer run.Close()

	cancel()
	if err := run.Finish(terminal.FinishRequest{
		Outcome:  terminal.Cancelled,
		Location: "cancellation requested",
		Summary:  terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "context interruption"}}},
	}, &terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "context-interrupted-result"}}}); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
}

func assertRichConsoleLifecyclePTY(t *testing.T, output string, outcome terminal.FinishOutcome, location, summary, result string, color bool) {
	t.Helper()
	assertTrackedPTYCleanup(t, output, result)
	if countAlternateScreen(output, "h") != 1 || countAlternateScreen(output, "l") != 1 {
		t.Fatalf("Console lifecycle did not own exactly one alternate-screen session: %q", output)
	}
	plain := strings.Join(strings.Fields(ansi.Strip(output)), " ")
	for _, needle := range []string{
		"YCY / G0 matrix", "safe target", "Source catalog", "Approval catalog", "Configure source", "Confirm execution",
		"Validate request", "Write projection", "DONE", strings.ToUpper(outcome.String()), summary,
		"ANSWERS", "WORK", "AT " + location, "OUTCOME " + outcome.String() + ": " + summary,
	} {
		if !strings.Contains(plain, needle) {
			t.Fatalf("Console lifecycle missing %q: %q", needle, plain)
		}
	}
	lastFormCatalog := max(strings.LastIndex(output, "Source catalog"), strings.LastIndex(output, "Approval catalog"))
	firstWorkCatalog := strings.Index(output, "Validate request")
	if lastFormCatalog < 0 || firstWorkCatalog < 0 || lastFormCatalog >= firstWorkCatalog {
		t.Fatalf("Form Catalog was not replaced before Work Catalog in PTY output: %q", output)
	}
	if !containsMeterFrame(output) {
		t.Fatalf("Console lifecycle did not render a Meter frame: %q", output)
	}
	if color && !strings.Contains(output, "\x1b[38;") {
		t.Fatalf("colored Console lifecycle lacks a color style: %q", output)
	}
	if !color {
		for _, colorPrefix := range []string{"\x1b[3m", "\x1b[9m", "\x1b[38;"} {
			if strings.Contains(output, colorPrefix) {
				t.Fatalf("NO_COLOR Console lifecycle contains %q: %q", colorPrefix, output)
			}
		}
	}

	exit := strings.LastIndex(output, "\x1b[?1049l")
	transcript := transcriptAfterPrimaryScreen(t, output, exit)
	for _, header := range []string{"ANSWERS", "WORK", "AT", "OUTCOME"} {
		if got := hasTerminalStyleImmediatelyBefore(transcript, header); got != color {
			t.Fatalf("Transcript header %q styled = %t, want %t: %q", header, got, color, transcript)
		}
	}
	plainTranscript := ansi.Strip(transcript)
	if containsMeterFrame(plainTranscript) {
		t.Fatalf("Transcript replay retained a transient Meter frame: %q", transcript)
	}
	answers := strings.Index(plainTranscript, "ANSWERS")
	work := strings.Index(plainTranscript, "WORK")
	at := strings.Index(plainTranscript, "AT       "+location)
	final := strings.Index(plainTranscript, "OUTCOME  "+outcome.String()+": "+summary)
	resultAt := strings.LastIndex(plainTranscript, result)
	if answers < 0 || work < answers || at < work || final < at || resultAt < final {
		t.Fatalf("Console lifecycle restore/transcript/stdout order = %q", output)
	}
}

func containsMeterFrame(value string) bool {
	for _, frame := range []string{"▱▱▱", "▰▱▱", "▰▰▱", "▰▰▰"} {
		if strings.Contains(value, frame) {
			return true
		}
	}
	return false
}

func hasTerminalStyleImmediatelyBefore(value, header string) bool {
	index := strings.Index(value, header)
	if index < 0 {
		return false
	}
	prefix := value[:index]
	styleStart := strings.LastIndex(prefix, "\x1b[")
	if styleStart < 0 {
		return false
	}
	styleEnd := strings.IndexByte(prefix[styleStart:], 'm')
	return styleEnd >= 0 && styleStart+styleEnd+1 == len(prefix)
}

func transcriptAfterPrimaryScreen(t *testing.T, output string, exit int) string {
	t.Helper()
	if exit < 0 {
		t.Fatalf("Rich Console did not restore the primary screen: %q", output)
	}
	start := exit + len("\x1b[?1049l")
	if start > len(output) {
		t.Fatalf("invalid primary-screen exit position %d in %q", exit, output)
	}
	return output[start:]
}
