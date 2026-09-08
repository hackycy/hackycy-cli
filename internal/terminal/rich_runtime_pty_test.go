package terminal_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRichRuntimeLongListsStayVisibleAcrossNavigationAndResize(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_LONG_LIST_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRichLongListHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRichRuntimeLongListsStayVisibleAcrossNavigationAndResize$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, output, readDone := startRichPTYTest(t, command, "Choose one")
	defer process.Close()
	respondToHuhTerminalQueries(t, process, output)
	waitForTrackedPrompt(t, output, "Choose one")

	writeRichPTYInput(t, process, "G")
	waitForTrackedPrompt(t, output, "item-199")
	for _, size := range [][2]uint16{{28, 7}, {44, 12}, {100, 30}, {80, 24}} {
		if err := process.Resize(size[0], size[1]); err != nil {
			t.Fatalf("resize PTY to %dx%d: %v", size[0], size[1], err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	writeRichPTYInput(t, process, "\x15")
	time.Sleep(50 * time.Millisecond)
	writeRichPTYInput(t, process, "\x04")
	time.Sleep(50 * time.Millisecond)
	writeRichPTYInput(t, process, "/")
	time.Sleep(100 * time.Millisecond)
	writeRichPTYInput(t, process, "item-173")
	// Huh v2 redraws the filter input with cursor and style updates between
	// typed runes. Give the child model one render cycle before committing it.
	time.Sleep(150 * time.Millisecond)
	writeRichPTYInput(t, process, "\r")
	time.Sleep(50 * time.Millisecond)
	writeRichPTYInput(t, process, "\r")
	waitForRichPromptReplacement(t, output, "Choose one", "Choose many")

	writeRichPTYInput(t, process, "\x01")
	time.Sleep(50 * time.Millisecond)
	for _, input := range []string{"G", "\x15", "\x04", "\r"} {
		writeRichPTYInput(t, process, input)
		time.Sleep(20 * time.Millisecond)
	}
	waitForTrackedPrompt(t, output, "Working")
	finishRichPTYTest(t, process, readDone, output)

	text := output.String()
	for _, expected := range []string{"item-199", "item-173", "select=item-173", "multi=200"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("PTY output missing %q: %q", expected, text)
		}
	}
	if countAlternateScreen(text, "h") != 1 || countAlternateScreen(text, "l") != 1 {
		t.Fatalf("runtime did not own exactly one alternate-screen session: %q", text)
	}
	assertTrackedPTYCleanup(t, text, "select=item-173")
}

func TestRichRuntimeEscAndContextCancellationRestoreTerminal(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		mode  string
		input string
		want  string
	}{
		{name: "Esc", mode: "esc", input: "\x1b", want: "cancel=interaction"},
		{name: "context", mode: "context", want: "cancel=context"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			const helperEnvironment = "YCY_TERMINAL_CANCEL_HELPER"
			if mode := os.Getenv(helperEnvironment); mode != "" {
				if mode == testCase.mode {
					runRichCancellationHelper(t, mode)
				}
				return
			}

			command := exec.Command(os.Args[0], "-test.run=^TestRichRuntimeEscAndContextCancellationRestoreTerminal$/^"+testCase.name+"$")
			command.Env = append(richPTYEnvironment(), helperEnvironment+"="+testCase.mode, "TERM=xterm-256color")
			process, output, readDone := startRichPTYTest(t, command, "Cancel list")
			defer process.Close()
			respondToHuhTerminalQueries(t, process, output)
			waitForTrackedPrompt(t, output, "Cancel list")
			if testCase.input != "" {
				writeRichPTYInput(t, process, testCase.input)
			}
			finishRichPTYTest(t, process, readDone, output)

			text := output.String()
			assertTrackedPTYCleanup(t, text, testCase.want)
			if countAlternateScreen(text, "h") != 1 || countAlternateScreen(text, "l") != 1 {
				t.Fatalf("cancel flow did not own exactly one alternate-screen session: %q", text)
			}
		})
	}
}

func TestRichRuntimeKeepsUIOnStderrWhenStdoutIsRedirected(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_REDIRECT_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRichRedirectHelper(t)
		return
	}

	var durable bytes.Buffer
	command := exec.Command(os.Args[0], "-test.run=^TestRichRuntimeKeepsUIOnStderrWhenStdoutIsRedirected$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	command.Stdout = &durable
	process, output, readDone := startRichPTYTest(t, command, "Continue?")
	defer process.Close()
	respondToHuhTerminalQueries(t, process, output)
	waitForTrackedPrompt(t, output, "Continue?")
	writeRichPTYInput(t, process, "\r")
	finishRichPTYTest(t, process, readDone, output)

	if got := durable.String(); !strings.HasPrefix(got, "redirected-result\n") || strings.Contains(got, "stderr notice") || strings.Contains(got, "Continue?") {
		t.Fatalf("redirected stdout = %q", got)
	}
	if terminaltest.ContainsTerminalControl(durable.Bytes()) {
		t.Fatalf("redirected stdout contains terminal control: %q", durable.String())
	}
	text := output.String()
	if !strings.Contains(text, "stderr notice") || !strings.Contains(text, "deferred diagnostic") {
		t.Fatalf("Rich stderr omitted UI or deferred diagnostics: %q", text)
	}
	if strings.Contains(text, "redirected-result") {
		t.Fatalf("durable result leaked to stderr: %q", text)
	}
	if countAlternateScreen(text, "h") != 1 || countAlternateScreen(text, "l") != 1 {
		t.Fatalf("redirected flow did not own exactly one alternate-screen session: %q", text)
	}
}

func TestRichRuntimeFinishRestoresThenReplaysBeforeDiagnosticsAndResult(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_FINISH_ORDER_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRichFinishOrderHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRichRuntimeFinishRestoresThenReplaysBeforeDiagnosticsAndResult$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, output, readDone := startRichPTYTest(t, command, "checkpoint")
	defer process.Close()
	respondToHuhTerminalQueries(t, process, output)
	finishRichPTYTest(t, process, readDone, output)

	text := output.String()
	exit := strings.LastIndex(text, "\x1b[?1049l")
	liveOutcome := strings.Index(text, "SUCCEEDED")
	liveSummary := strings.Index(text, "safe-finish-summary")
	transcriptCheckpoint := strings.LastIndex(text, "checkpoint")
	transcriptOutcome := strings.LastIndex(text, "succeeded")
	transcriptSummary := -1
	if exit >= 0 {
		afterExit := exit + len("\x1b[?1049l")
		if afterExit <= len(text) {
			transcriptSummary = strings.Index(text[afterExit:], "safe-finish-summary")
			if transcriptSummary >= 0 {
				transcriptSummary += afterExit
			}
		}
	}
	firstDiagnostic := strings.LastIndex(text, "first deferred diagnostic")
	secondDiagnostic := strings.LastIndex(text, "second deferred diagnostic")
	result := strings.LastIndex(text, "finished-result")
	cursor := strings.LastIndex(text, "\x1b[?25h")
	if exit < 0 || liveOutcome < 0 || liveSummary < 0 || transcriptCheckpoint < 0 || transcriptOutcome < 0 || transcriptSummary < 0 ||
		firstDiagnostic < 0 || secondDiagnostic < 0 || result < 0 ||
		liveOutcome > exit || liveSummary > exit || exit > cursor || cursor > transcriptCheckpoint ||
		transcriptCheckpoint > transcriptOutcome || transcriptOutcome > transcriptSummary ||
		transcriptSummary > firstDiagnostic || firstDiagnostic > secondDiagnostic ||
		secondDiagnostic > result || liveSummary == transcriptSummary || transcriptSummary == result {
		t.Fatalf("Rich finish ordering = %q", text)
	}
	if countAlternateScreen(text, "h") != 1 || countAlternateScreen(text, "l") != 1 {
		t.Fatalf("finish flow did not own exactly one alternate-screen session: %q", text)
	}
}

func TestRichRuntimeCloseHandsTerminalToInheritedChildProcess(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_CHILD_HANDOFF_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRichChildHandoffHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRichRuntimeCloseHandsTerminalToInheritedChildProcess$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, output, readDone := startRichPTYTest(t, command, "Preparing child")
	defer process.Close()
	respondToHuhTerminalQueries(t, process, output)
	waitForTrackedPrompt(t, output, "\x1b[?1049l")
	writeRichPTYInput(t, process, "hello child\r")
	finishRichPTYTest(t, process, readDone, output)

	text := output.String()
	assertTrackedPTYCleanup(t, text, "child=hello child")
	exit := strings.LastIndex(text, "\x1b[?1049l")
	child := strings.LastIndex(text, "child=hello child")
	if exit < 0 || child < 0 || exit > child {
		t.Fatalf("terminal handoff order = %q", text)
	}
}

func TestRichRuntimeDoesNotLeakLateSynchronizedOutputReport(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_LATE_MODE_REPORT_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: richTestCapabilities(true),
			Input:        os.Stdin,
			Output:       os.Stdout,
			Diagnostics:  os.Stderr,
		})
		run := experience.Open(context.Background())
		if err := run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "Preparing handoff"}}}); err != nil {
			t.Fatalf("Notice() error = %v", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		// The inherited child represents the shell that receives any terminal
		// capability response left behind after the Rich runtime restores input.
		_, _ = fmt.Fprintln(os.Stdout, "RUNTIME_CLOSED")
		child := exec.Command("sh", "-c", `IFS= read -r line; printf 'inherited=%s\n' "$line"`)
		child.Stdin = os.Stdin
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Run(); err != nil {
			t.Fatalf("run inherited child: %v", err)
		}
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRichRuntimeDoesNotLeakLateSynchronizedOutputReport$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color", "TERM_PROGRAM=WezTerm")
	process, output, readDone := startRichPTYTest(t, command, "RUNTIME_CLOSED")
	defer process.Close()

	deadline := time.Now().Add(5 * time.Second)
	sawQuery := false
	for time.Now().Before(deadline) {
		text := output.String()
		if strings.Contains(text, "\x1b[?2026$p") {
			sawQuery = true
		}
		if strings.Contains(text, "RUNTIME_CLOSED") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(output.String(), "RUNTIME_CLOSED") {
		t.Fatalf("runtime did not restore terminal: %q", output.String())
	}

	if sawQuery {
		// Delay until after Close has handed the cooked terminal to the child.
		time.Sleep(100 * time.Millisecond)
		if _, err := process.Terminal().Write([]byte("\x1b[?2026;2$y\r")); err != nil {
			t.Fatalf("write late mode report: %v", err)
		}
	} else if _, err := process.Terminal().Write([]byte("ok\r")); err != nil {
		t.Fatalf("write inherited input: %v", err)
	}

	finishRichPTYTest(t, process, readDone, output)
	text := output.String()
	if strings.Contains(text, "2026;2$y") {
		t.Fatalf("late synchronized-output report leaked into inherited terminal input: %q", text)
	}
	if !strings.Contains(text, "inherited=") {
		t.Fatalf("inherited child did not receive terminal input: %q", text)
	}
}

func runRichLongListHelper(t *testing.T) {
	t.Helper()
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: richTestCapabilities(true),
		Input:        os.Stdin,
		Output:       os.Stdout,
		Diagnostics:  os.Stderr,
	})
	run := experience.Open(context.Background())
	defer run.Close()
	if err := run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "Long-list context"}}}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	options := richListOptions(200)
	selected, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionSelect, Message: "Choose one", Options: options})
	if err != nil {
		t.Fatalf("select Ask() error = %v", err)
	}
	multiple, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionMultiSelect, Message: "Choose many", Options: options})
	if err != nil {
		t.Fatalf("multi-select Ask() error = %v", err)
	}
	updates := make(chan terminal.OperationPhase, 2)
	updates <- terminal.OperationPhase{Name: "Working", Detail: "200 options", State: terminal.PhaseActive}
	go func() {
		time.Sleep(150 * time.Millisecond)
		updates <- terminal.OperationPhase{Name: "Working", Detail: "200 options", State: terminal.PhaseCompleted}
		close(updates)
	}()
	if err := run.Track(terminal.TrackedOperation{Label: "Long list", Updates: updates}); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: fmt.Sprintf("select=%s\nmulti=%d", selected.Value, len(multiple.Values))}}}); err != nil {
		t.Fatalf("Result() error = %v", err)
	}
}

func runRichCancellationHelper(t *testing.T, mode string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: richTestCapabilities(true),
		Input:        os.Stdin,
		Output:       os.Stdout,
		Diagnostics:  os.Stderr,
	})
	run := experience.Open(ctx)
	defer run.Close()
	if mode == "context" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancel()
		}()
	}
	_, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionSelect, Message: "Cancel list", Options: richListOptions(200)})
	marker := ""
	switch {
	case errors.Is(err, terminal.ErrInteractionCancelled):
		marker = "cancel=interaction"
	case errors.Is(err, context.Canceled):
		marker = "cancel=context"
	default:
		t.Fatalf("Ask() error = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: marker}}}); err != nil {
		t.Fatalf("Result() error = %v", err)
	}
}

func runRichRedirectHelper(t *testing.T) {
	t.Helper()
	capabilities := richTestCapabilities(true)
	capabilities.Stdout = terminal.StreamCapability{}
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: capabilities,
		Input:        os.Stdin,
		Output:       os.Stdout,
		Diagnostics:  os.Stderr,
	})
	run := experience.Open(context.Background())
	defer run.Close()
	if err := run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "stderr notice"}}}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if _, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionConfirm, Message: "Continue?", HasDefault: true, Default: terminal.InteractionAnswer{Confirmed: true}}); err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if _, err := io.WriteString(experience.DiagnosticWriter(), "deferred diagnostic\n"); err != nil {
		t.Fatalf("diagnostic write = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "redirected-result"}}}); err != nil {
		t.Fatalf("Result() error = %v", err)
	}
}

func runRichFinishOrderHelper(t *testing.T) {
	t.Helper()
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: richTestCapabilities(true),
		Input:        os.Stdin,
		Output:       os.Stdout,
		Diagnostics:  os.Stderr,
	})
	run := experience.Open(context.Background())
	defer run.Close()
	if err := run.Milestone(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "checkpoint"}}}); err != nil {
		t.Fatalf("Milestone() error = %v", err)
	}
	if _, err := io.WriteString(experience.DiagnosticWriter(), "first deferred diagnostic\n"); err != nil {
		t.Fatalf("first diagnostic write = %v", err)
	}
	if _, err := io.WriteString(experience.DiagnosticWriter(), "second deferred diagnostic\n"); err != nil {
		t.Fatalf("second diagnostic write = %v", err)
	}
	if err := run.Finish(terminal.FinishRequest{
		Outcome:  terminal.Succeeded,
		Location: "workspace/project",
		Summary:  terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "safe-finish-summary"}}},
	}, &terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "finished-result"}}}); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
}

func runRichChildHandoffHelper(t *testing.T) {
	t.Helper()
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: richTestCapabilities(true),
		Input:        os.Stdin,
		Output:       os.Stdout,
		Diagnostics:  os.Stderr,
	})
	run := experience.Open(context.Background())
	if err := run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "Preparing child"}}}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	child := exec.Command("sh", "-c", `IFS= read -r line; printf 'child=%s\n' "$line"`)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Run(); err != nil {
		t.Fatalf("run inherited child: %v", err)
	}
}

func richListOptions(count int) []terminal.InteractionOption {
	options := make([]terminal.InteractionOption, count)
	for index := range options {
		value := fmt.Sprintf("item-%03d", index)
		options[index] = terminal.InteractionOption{Label: value, Value: value}
	}
	return options
}

func startRichPTYTest(t *testing.T, command *exec.Cmd, prompt string) (*terminaltest.PTYProcess, *promptBuffer, <-chan struct{}) {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	output := newPromptBuffer(prompt)
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, process.Terminal())
		close(readDone)
	}()
	return process, output, readDone
}

func finishRichPTYTest(t *testing.T, process *terminaltest.PTYProcess, readDone <-chan struct{}, output *promptBuffer) {
	t.Helper()
	if err := process.Wait(); err != nil {
		t.Fatalf("wait PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading PTY output: %q", output.String())
	}
}

func writeRichPTYInput(t *testing.T, process *terminaltest.PTYProcess, value string) {
	t.Helper()
	if _, err := io.WriteString(process.Terminal(), value); err != nil {
		t.Fatalf("write PTY input: %v", err)
	}
}

func waitForRichPromptReplacement(t *testing.T, output *promptBuffer, previous, update string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		text := output.String()
		if strings.LastIndex(text, previous) >= 0 && strings.LastIndex(text, update) > strings.LastIndex(text, previous) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("PTY output did not render %q through differential update %q: %q", previous, update, output.String())
}
