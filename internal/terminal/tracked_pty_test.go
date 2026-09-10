package terminal_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRichTrackLeavesInlineFinalPhaseBeforeDeferredDiagnosticsAndResult(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_TRACK_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runTrackedPTYHelper(t)
		return
	}

	output := runTrackedPTYHelperProcess(t, "TestRichTrackLeavesInlineFinalPhaseBeforeDeferredDiagnosticsAndResult", helperEnvironment, "", func(process *terminaltest.PTYProcess, output *promptBuffer) {
		waitForTrackedPrompt(t, output, "Scanning repositories")
		compactStart := len(output.String())
		if err := process.Resize(40, 15); err != nil {
			t.Fatalf("resize narrow PTY: %v", err)
		}
		waitForTrackedPromptAfter(t, output, compactStart, "STATE / PHASE / DETAIL")
		if err := process.Resize(100, 30); err != nil {
			t.Fatalf("resize wide PTY: %v", err)
		}
	})

	assertTrackedPTYCleanup(t, output, "durable-result")
	if countAlternateScreen(output, "h") != 1 || countAlternateScreen(output, "l") != 1 {
		t.Fatalf("tracked renderer did not own exactly one alternate-screen session: %q", output)
	}
	assertBTrackedConsole(t, output, true)
	final := strings.LastIndex(output, "Fetching commits")
	deferred := strings.LastIndex(output, "deferred diagnostic")
	result := strings.LastIndex(output, "durable-result")
	if final < 0 || deferred < 0 || result < 0 || final > deferred || deferred > result {
		t.Fatalf("tracked output order = %q", output)
	}
}

func TestRichTrackHonorsNoColor(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_TRACK_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runTrackedPTYHelper(t)
		return
	}
	output := runTrackedPTYHelperProcess(t, "TestRichTrackHonorsNoColor", helperEnvironment, "NO_COLOR=1", func(_ *terminaltest.PTYProcess, output *promptBuffer) {
		waitForTrackedPrompt(t, output, "Scanning repositories")
	})

	assertTrackedPTYCleanup(t, output, "durable-result")
	assertBTrackedConsole(t, output, false)
	for _, colorPrefix := range []string{"\x1b[3m", "\x1b[9m", "\x1b[38;"} {
		if strings.Contains(output, colorPrefix) {
			t.Fatalf("NO_COLOR tracked output contains %q: %q", colorPrefix, output)
		}
	}
}

func TestRichTrackCtrlCAndEscRequestCooperativeCancellationAndRestoreTerminal(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mode string
		keys string
	}{
		{name: "Ctrl-C", mode: "ctrl-c", keys: "\x03"},
		{name: "Esc confirmation", mode: "esc", keys: "\x1b\x1b"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			const helperEnvironment = "YCY_TERMINAL_TRACK_CANCEL_HELPER"
			const helperModeEnvironment = "YCY_TERMINAL_TRACK_CANCEL_MODE"
			if helperMode := os.Getenv(helperModeEnvironment); helperMode != "" && helperMode != testCase.mode {
				return
			}
			if os.Getenv(helperEnvironment) == "1" {
				runTrackedCancellationPTYHelper(t)
				return
			}

			output := runTrackedPTYHelperProcess(t, "TestRichTrackCtrlCAndEscRequestCooperativeCancellationAndRestoreTerminal", helperEnvironment, helperModeEnvironment+"="+testCase.mode, func(process *terminaltest.PTYProcess, output *promptBuffer) {
				waitForTrackedPrompt(t, output, "Scanning repositories")
				if testCase.keys == "\x1b\x1b" {
					if _, err := io.WriteString(process.Terminal(), "\x1b"); err != nil {
						t.Fatalf("write first Esc: %v", err)
					}
					waitForTrackedPrompt(t, output, "Press Esc again to cancel")
					if _, err := io.WriteString(process.Terminal(), "\x1b"); err != nil {
						t.Fatalf("write second Esc: %v", err)
					}
					return
				}
				if _, err := io.WriteString(process.Terminal(), testCase.keys); err != nil {
					t.Fatalf("write Ctrl-C: %v", err)
				}
			})

			assertTrackedPTYCleanup(t, output, "cancelled-result")
			if !strings.Contains(output, "Cancelled") {
				t.Fatalf("cancelled phase missing from PTY output: %q", output)
			}
		})
	}
}

func TestRichTrackKeepsDurableResultsOnStdout(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.PlainInteractive},
		Input:        strings.NewReader(""),
		Output:       &stdout,
		Diagnostics:  &stderr,
	})
	run := experience.Open(context.Background())
	updates := make(chan terminal.OperationPhase, 1)
	updates <- terminal.OperationPhase{Name: "Scanning repositories", State: terminal.PhaseCompleted}
	close(updates)
	if err := run.Track(terminal.TrackedOperation{Label: "Git Pulse", Updates: updates}); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "durable-result"}}}); err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got, want := stdout.String(), "durable-result\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "Scanning repositories") || strings.Contains(stderr.String(), "durable-result") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func runTrackedPTYHelper(t *testing.T) {
	t.Helper()
	color := os.Getenv("NO_COLOR") == ""
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: richTestCapabilities(color),
		Input:        os.Stdin,
		Output:       os.Stdout,
		Diagnostics:  os.Stderr,
	})
	run := experience.Open(context.Background())
	defer run.Close()
	updates := make(chan terminal.OperationPhase, 4)
	go func() {
		updates <- terminal.OperationPhase{Name: "Scanning repositories", Detail: "workspace/project", State: terminal.PhaseActive}
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(experience.DiagnosticWriter(), "deferred diagnostic\n")
		updates <- terminal.OperationPhase{Name: "Scanning repositories", Detail: "workspace/project", State: terminal.PhaseCompleted}
		updates <- terminal.OperationPhase{Name: "Fetching commits", Detail: "workspace/project", State: terminal.PhaseCompleted}
		close(updates)
	}()
	if err := run.Track(terminal.TrackedOperation{Label: "Git Pulse", Updates: updates}); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "durable-result"}}}); err != nil {
		t.Fatalf("Result() error = %v", err)
	}
}

func runTrackedCancellationPTYHelper(t *testing.T) {
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
	updates := make(chan terminal.OperationPhase, 2)
	updates <- terminal.OperationPhase{Name: "Scanning repositories", State: terminal.PhaseActive}
	var once sync.Once
	if err := run.Track(terminal.TrackedOperation{
		Label:   "Git Pulse",
		Updates: updates,
		RequestCancel: func() {
			once.Do(func() {
				cancel()
				updates <- terminal.OperationPhase{Name: "Cancelled", State: terminal.PhaseCancelled}
				close(updates)
			})
		},
	}); err != nil {
		t.Fatalf("Track() error = %v", err)
	}
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "cancelled-result"}}}); err != nil {
		t.Fatalf("Result() error = %v", err)
	}
}

func runTrackedPTYHelperProcess(t *testing.T, testName, helperEnvironment, extraEnvironment string, during func(*terminaltest.PTYProcess, *promptBuffer)) string {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	command.Env = append(trackedPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	if extraEnvironment != "" {
		command.Env = append(command.Env, extraEnvironment)
	}
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()

	output := newPromptBuffer("Scanning repositories")
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, process.Terminal())
		close(readDone)
	}()
	respondToHuhTerminalQueries(t, process, output)
	during(process, output)
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
	return output.String()
}

func waitForTrackedPrompt(t *testing.T, output *promptBuffer, needle string) {
	waitForTrackedPromptAfter(t, output, 0, needle)
}

func waitForTrackedPromptAfter(t *testing.T, output *promptBuffer, start int, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		text := output.String()
		if start <= len(text) && strings.Contains(text[start:], needle) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("PTY output did not contain %q: %q", needle, output.String())
}

func assertBTrackedConsole(t *testing.T, output string, color bool) {
	t.Helper()
	plain := strings.Join(strings.Fields(ansi.Strip(output)), " ")
	for _, needle := range []string{
		"YCY", "terminal session", "mode interactive", "STATE", "PHASE", "DETAIL",
		"Scanning repositories", "workspace/project", "◆ ACTIVE", "✓ DONE",
	} {
		if !strings.Contains(plain, needle) {
			t.Fatalf("B Track PTY output missing %q: %q", needle, plain)
		}
	}
	for _, generic := range []string{"[active]", "[done]", "│"} {
		if strings.Contains(plain, generic) {
			t.Fatalf("B Track PTY output retained %q: %q", generic, plain)
		}
	}
	if color && !strings.Contains(output, "\x1b[38;") {
		t.Fatalf("colored B Track PTY output has no color style: %q", output)
	}
	if !color && strings.Contains(output, "\x1b[38;") {
		t.Fatalf("NO_COLOR B Track PTY output contains a color style: %q", output)
	}
}

func assertTrackedPTYCleanup(t *testing.T, output, marker string) {
	t.Helper()
	if !strings.Contains(output, marker) {
		t.Fatalf("PTY output missing %q: %q", marker, output)
	}
	if !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("tracked renderer did not restore the cursor: %q", output)
	}
}

func countAlternateScreen(output, suffix string) int {
	count := 0
	for _, code := range []string{"\x1b[?1049" + suffix, "\x1b[?1047" + suffix, "\x1b[?47" + suffix} {
		count += strings.Count(output, code)
	}
	return count
}

func trackedPTYEnvironment() []string {
	ignored := map[string]struct{}{
		"CI":             {},
		"CLICOLOR":       {},
		"CLICOLOR_FORCE": {},
		"COLORTERM":      {},
		"NO_COLOR":       {},
		"TERM":           {},
	}
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := ignored[key]; !skip {
			environment = append(environment, entry)
		}
	}
	return environment
}
