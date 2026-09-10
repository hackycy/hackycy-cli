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
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestAutomationAskFailsBeforeReadingOrWriting(t *testing.T) {
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
		Input:        panicReader{},
		Diagnostics:  panicWriter{},
	})

	_, err := handler.Ask(context.Background(), terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "Name"})
	if !errors.Is(err, terminal.ErrAutomationInteraction) {
		t.Fatalf("Ask() error = %v, want ErrAutomationInteraction", err)
	}
}

func TestInteractionValidationRejectsMalformedRequestsBeforeIO(t *testing.T) {
	tests := []struct {
		name    string
		request terminal.InteractionRequest
	}{
		{name: "unknown kind", request: terminal.InteractionRequest{Kind: terminal.InteractionKind(99), Message: "Choose"}},
		{name: "missing message", request: terminal.InteractionRequest{Kind: terminal.InteractionText}},
		{name: "message is only terminal control", request: terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "\x1b[2K"}},
		{name: "duplicate option value", request: terminal.InteractionRequest{Kind: terminal.InteractionSelect, Message: "Choose", Options: []terminal.InteractionOption{{Label: "One", Value: "same"}, {Label: "Two", Value: "same"}}}},
		{name: "default outside options", request: terminal.InteractionRequest{Kind: terminal.InteractionSelect, Message: "Choose", Options: []terminal.InteractionOption{{Label: "One", Value: "one"}}, HasDefault: true, Default: terminal.InteractionAnswer{Value: "missing"}}},
		{name: "select default has multi-select fields", request: terminal.InteractionRequest{Kind: terminal.InteractionSelect, Message: "Choose", Options: []terminal.InteractionOption{{Label: "One", Value: "one"}}, HasDefault: true, Default: terminal.InteractionAnswer{Value: "one", Values: []string{"one"}}}},
		{name: "multi-select default has scalar field", request: terminal.InteractionRequest{Kind: terminal.InteractionMultiSelect, Message: "Choose", Options: []terminal.InteractionOption{{Label: "One", Value: "one"}}, HasDefault: true, Default: terminal.InteractionAnswer{Value: "one"}}},
		{name: "multi-select default repeats a value", request: terminal.InteractionRequest{Kind: terminal.InteractionMultiSelect, Message: "Choose", Options: []terminal.InteractionOption{{Label: "One", Value: "one"}}, HasDefault: true, Default: terminal.InteractionAnswer{Values: []string{"one", "one"}}}},
		{name: "text default has confirmation field", request: terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "Name", HasDefault: true, Default: terminal.InteractionAnswer{Value: "name", Confirmed: true}}},
		{name: "confirmation default has scalar field", request: terminal.InteractionRequest{Kind: terminal.InteractionConfirm, Message: "Continue", HasDefault: true, Default: terminal.InteractionAnswer{Value: "yes"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
				Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
				Input:        panicReader{},
				Diagnostics:  panicWriter{},
			})
			_, err := handler.Ask(context.Background(), test.request)
			if !errors.Is(err, terminal.ErrInvalidInteractionRequest) {
				t.Fatalf("Ask() error = %v, want ErrInvalidInteractionRequest", err)
			}
		})
	}
}

func TestPlainInteractionErrorsAreControlFree(t *testing.T) {
	var diagnostics bytes.Buffer
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("\nvalid\n"),
		Diagnostics:  &diagnostics,
	})
	_, err := handler.Ask(context.Background(), terminal.InteractionRequest{
		Kind:    terminal.InteractionText,
		Message: "Name",
		Validate: func(answer terminal.InteractionAnswer) error {
			if answer.Value == "" {
				return errors.New("invalid\x1b[31m input")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}
	if terminaltest.ContainsTerminalControl(diagnostics.Bytes()) || strings.ContainsRune(diagnostics.String(), '\x01') {
		t.Fatalf("diagnostics contain terminal control: %q", diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "invalid input") {
		t.Fatalf("diagnostics = %q, want sanitized validation error", diagnostics.String())
	}
}

func TestInteractionTranscriptProjectionRedactsAndUsesOptionLabels(t *testing.T) {
	var diagnostics bytes.Buffer
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("secret\n2\n1 2\ny\n"),
		Diagnostics:  &diagnostics,
		Transcript:   terminal.TranscriptOptions{MaxEvents: 16, MaxBytes: 4096},
	})
	run := experience.Open(context.Background())
	if _, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "Token", TranscriptLabel: "Access token", Sensitive: true}); err != nil {
		t.Fatalf("sensitive Ask() error = %v", err)
	}
	if _, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionSelect, Message: "Choose", TranscriptLabel: "Environment", Options: []terminal.InteractionOption{{Label: "Production", Value: "prod"}, {Label: "Development", Value: "dev"}}}); err != nil {
		t.Fatalf("select Ask() error = %v", err)
	}
	if _, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionMultiSelect, Message: "Choose many", TranscriptLabel: "Targets", Options: []terminal.InteractionOption{{Label: "One", Value: "one"}, {Label: "Two", Value: "two"}}}); err != nil {
		t.Fatalf("multi-select Ask() error = %v", err)
	}
	if _, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionConfirm, Message: "Continue", TranscriptLabel: "Confirmation"}); err != nil {
		t.Fatalf("confirm Ask() error = %v", err)
	}
	if err := run.Finish(terminal.Succeeded, nil); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if got := diagnostics.String(); strings.Contains(got, "secret") {
		t.Fatalf("plain prompts unexpectedly expose secret value: %q", got)
	}
}

func TestPlainSecretRejectsNonTerminalInputWithoutReadingOrWriting(t *testing.T) {
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        panicReader{},
		Diagnostics:  panicWriter{},
	})

	_, err := handler.Ask(context.Background(), terminal.InteractionRequest{Kind: terminal.InteractionSecret, Message: "Access token"})
	if !errors.Is(err, terminal.ErrAutomationInteraction) {
		t.Fatalf("Ask() error = %v, want ErrAutomationInteraction", err)
	}
}

func TestPlainAskUsesDiagnosticStreamAndRetriesValidation(t *testing.T) {
	var diagnostics bytes.Buffer
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("\nproject\n"),
		Diagnostics:  &diagnostics,
	})

	answer, err := handler.Ask(context.Background(), terminal.InteractionRequest{
		Kind:        terminal.InteractionText,
		Message:     "Project name",
		Description: "Choose a local project.",
		Placeholder: "example",
		Validate: func(answer terminal.InteractionAnswer) error {
			if answer.Value == "" {
				return errors.New("project name is required")
			}
			return nil
		},
	})
	if err != nil || answer.Value != "project" {
		t.Fatalf("Ask() = (%#v, %v)", answer, err)
	}
	if got := diagnostics.String(); !strings.Contains(got, "Choose a local project.") || !strings.Contains(got, "project name is required") {
		t.Fatalf("diagnostics = %q", got)
	}
	if terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
		t.Fatalf("plain diagnostics contain terminal control: %q", diagnostics.String())
	}
}

func TestPlainAskSelectsExistingOptionsWithoutImplicitDefault(t *testing.T) {
	var diagnostics bytes.Buffer
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("wrong\n2\n"),
		Diagnostics:  &diagnostics,
	})

	answer, err := handler.Ask(context.Background(), terminal.InteractionRequest{
		Kind:    terminal.InteractionSelect,
		Message: "Choose an environment",
		Options: []terminal.InteractionOption{
			{Label: "Development", Value: "dev"},
			{Label: "Production", Value: "prod"},
		},
	})
	if err != nil || answer.Value != "prod" {
		t.Fatalf("Ask() = (%#v, %v)", answer, err)
	}
	if !strings.Contains(diagnostics.String(), "invalid selection") {
		t.Fatalf("diagnostics = %q, want invalid-selection feedback", diagnostics.String())
	}
}

func TestPlainAskHonorsCommandOwnedCancelValues(t *testing.T) {
	var diagnostics bytes.Buffer
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("quit\n"),
		Diagnostics:  &diagnostics,
	})

	_, err := handler.Ask(context.Background(), terminal.InteractionRequest{
		Kind:         terminal.InteractionSelect,
		Message:      "Select environment",
		Options:      []terminal.InteractionOption{{Label: "production", Value: ".env.production"}},
		CancelValues: []string{"", "q", "quit", "cancel"},
	})

	if !errors.Is(err, terminal.ErrInteractionCancelled) {
		t.Fatalf("Ask() error = %v, want ErrInteractionCancelled", err)
	}
	if strings.Contains(diagnostics.String(), "invalid selection") {
		t.Fatalf("diagnostics = %q, want direct cancellation", diagnostics.String())
	}
}

func TestPlainAskUsesCommandOwnedParserWithoutChangingDefaultOrCancellation(t *testing.T) {
	var diagnostics bytes.Buffer
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("bad\nlegacy\n"),
		Diagnostics:  &diagnostics,
	})

	answer, err := handler.Ask(context.Background(), terminal.InteractionRequest{
		Kind:    terminal.InteractionSelect,
		Message: "Choose",
		ParsePlain: func(value string) (terminal.InteractionAnswer, error) {
			if value != "legacy" {
				return terminal.InteractionAnswer{}, errors.New("invalid legacy choice")
			}
			return terminal.InteractionAnswer{Value: "selected"}, nil
		},
	})

	if err != nil || answer.Value != "selected" {
		t.Fatalf("Ask() = (%#v, %v)", answer, err)
	}
	if !strings.Contains(diagnostics.String(), "invalid legacy choice") {
		t.Fatalf("diagnostics = %q", diagnostics.String())
	}
}

func TestPlainAskUsesCommandOwnedPromptLayout(t *testing.T) {
	var diagnostics bytes.Buffer
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("2\n"),
		Diagnostics:  &diagnostics,
	})

	answer, err := handler.Ask(context.Background(), terminal.InteractionRequest{
		Kind:        terminal.InteractionSelect,
		Message:     "ignored by Plain prompt",
		PlainLead:   "Select a clean action",
		PlainPrompt: "> ",
		Options: []terminal.InteractionOption{
			{Label: "One", Value: "one"},
			{Label: "Two", Value: "two"},
		},
	})

	if err != nil || answer.Value != "two" {
		t.Fatalf("Ask() = (%#v, %v)", answer, err)
	}
	if got, want := diagnostics.String(), "Select a clean action\n1) One\n2) Two\n> "; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
}

func TestInteractionPromptProjectionStripsTerminalControlWithoutChangingValues(t *testing.T) {
	var diagnostics bytes.Buffer
	handler := terminal.NewInteractionHandler(terminal.InteractionOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader("2\n"),
		Diagnostics:  &diagnostics,
	})

	answer, err := handler.Ask(context.Background(), terminal.InteractionRequest{
		Kind:        terminal.InteractionSelect,
		Message:     "Choose\x1b[2K\x01",
		Description: "Visible\tcontext\x1b[31m",
		PlainLead:   "Lead\x1b[2J",
		PlainPrompt: "> \x1b[?25l",
		Options: []terminal.InteractionOption{
			{Label: "One\x1b[31m", Value: "internal-one", Description: "first\x01"},
			{Label: "Two\x1b[2K", Value: "internal-two", Description: "second\titem"},
		},
	})
	if err != nil || answer.Value != "internal-two" {
		t.Fatalf("Ask() = (%#v, %v), want internal-two", answer, err)
	}
	if terminaltest.ContainsTerminalControl(diagnostics.Bytes()) || strings.ContainsRune(diagnostics.String(), '\x01') {
		t.Fatalf("prompt projection contains terminal control: %q", diagnostics.String())
	}
	if got, want := diagnostics.String(), "Visible\tcontext\nLead\n1) One - first�\n2) Two - second\titem\n> "; got != want {
		t.Fatalf("prompt projection = %q, want %q", got, want)
	}
}

func TestRichAskUsesExplicitPTYInputAndDiagnosticOutput(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_HUH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: richTestCapabilities(true),
			Input:        os.Stdin,
			Output:       os.Stdout,
			Diagnostics:  os.Stderr,
		})
		run, err := experience.OpenConsole(context.Background(), terminal.ConsoleDescriptor{
			Command: "YCY / terminal test",
			FormCatalog: []terminal.ConsoleFormStep{
				{ID: "project-name", Name: "Project name"},
			},
		})
		if err != nil {
			t.Fatalf("OpenConsole() error = %v", err)
		}
		defer run.Close()
		answer, err := run.Ask(terminal.InteractionRequest{
			Kind:          terminal.InteractionText,
			Message:       "Project name",
			ConsoleStepID: "project-name",
			Validate: func(answer terminal.InteractionAnswer) error {
				if answer.Value == "" {
					return errors.New("project name is required")
				}
				return nil
			},
		})
		if err != nil {
			t.Fatalf("Ask() error = %v", err)
		}
		if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "answer=" + answer.Value}}}); err != nil {
			t.Fatalf("Result() error = %v", err)
		}
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRichAskUsesExplicitPTYInputAndDiagnosticOutput$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()

	output := newPromptBuffer("Project name")
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, process.Terminal())
		close(readDone)
	}()
	respondToHuhTerminalQueries(t, process, output)
	select {
	case <-output.prompt:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for Huh prompt: %q", output.String())
	}
	if _, err := io.WriteString(process.Terminal(), "ycy\r"); err != nil {
		t.Fatalf("write PTY input: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("wait PTY helper: %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reading PTY output")
	}
	assertRichCleanup(t, output.String(), "answer=ycy")
}

func TestRichAskCtrlCRestoresTerminalBeforeReturningCancellation(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_HUH_CANCEL_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: richTestCapabilities(true),
			Input:        os.Stdin,
			Output:       os.Stdout,
			Diagnostics:  os.Stderr,
		})
		run, err := experience.OpenConsole(context.Background(), terminal.ConsoleDescriptor{
			Command: "YCY / terminal test",
			FormCatalog: []terminal.ConsoleFormStep{
				{ID: "project-name", Name: "Project name"},
			},
		})
		if err != nil {
			t.Fatalf("OpenConsole() error = %v", err)
		}
		_, err = run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "Project name", ConsoleStepID: "project-name"})
		if !errors.Is(err, terminal.ErrInteractionCancelled) {
			t.Fatalf("Ask() error = %v, want ErrInteractionCancelled", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		_, _ = fmt.Fprintln(os.Stdout, "cancelled=true")
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRichAskCtrlCRestoresTerminalBeforeReturningCancellation$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()

	output := newPromptBuffer("Project name")
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, process.Terminal())
		close(readDone)
	}()
	respondToHuhTerminalQueries(t, process, output)
	select {
	case <-output.prompt:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for Huh prompt: %q", output.String())
	}
	if _, err := io.WriteString(process.Terminal(), "\x03"); err != nil {
		t.Fatalf("write PTY Ctrl-C: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("wait PTY helper: %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reading PTY output")
	}
	assertRichCleanup(t, output.String(), "cancelled=true")
}

func richTestCapabilities(color bool) terminal.Capabilities {
	return terminal.Capabilities{
		Interaction: terminal.RichInteractive,
		Stdin:       terminal.StreamCapability{Terminal: true},
		Stdout:      terminal.StreamCapability{Terminal: true, Color: color},
		Stderr:      terminal.StreamCapability{Terminal: true, Color: color},
	}
}

type promptBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	needle    string
	prompt    chan struct{}
	query     chan struct{}
	once      sync.Once
	queryOnce sync.Once
}

func newPromptBuffer(needle string) *promptBuffer {
	return &promptBuffer{needle: needle, prompt: make(chan struct{}), query: make(chan struct{})}
}

func respondToHuhTerminalQueries(t *testing.T, process *terminaltest.PTYProcess, output *promptBuffer) {
	t.Helper()
	select {
	case <-output.query:
		if _, err := io.WriteString(process.Terminal(), "\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[1;1R"); err != nil {
			t.Fatalf("write PTY terminal responses: %v", err)
		}
	case <-output.prompt:
		// The terminal may have enough cached capability state to render immediately.
	}
}

func assertRichCleanup(t *testing.T, output, marker string) {
	t.Helper()
	markerAt := strings.LastIndex(output, marker)
	if markerAt < 0 {
		t.Fatalf("PTY output missing %q: %q", marker, output)
	}
	if !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("Rich cleanup did not restore the cursor: %q", output)
	}
	exitAt := -1
	for _, code := range []string{"\x1b[?1049l", "\x1b[?1047l", "\x1b[?47l"} {
		exitAt = max(exitAt, strings.LastIndex(output, code))
	}
	if exitAt < 0 || exitAt > markerAt {
		t.Fatalf("Rich cleanup did not restore the primary screen before %q: %q", marker, output)
	}
}

func (buffer *promptBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	count, err := buffer.buffer.Write(value)
	if strings.Contains(buffer.buffer.String(), buffer.needle) {
		buffer.once.Do(func() { close(buffer.prompt) })
	}
	if strings.Contains(buffer.buffer.String(), "\x1b]11;?") || strings.Contains(buffer.buffer.String(), "\x1b[6n") {
		buffer.queryOnce.Do(func() { close(buffer.query) })
	}
	return count, err
}

func (buffer *promptBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("Automation Session attempted to read implicit input")
}

type panicWriter struct{}

func (panicWriter) Write([]byte) (int, error) {
	panic("Automation Session attempted to write an interaction")
}
