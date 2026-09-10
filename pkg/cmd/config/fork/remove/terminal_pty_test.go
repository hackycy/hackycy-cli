package remove

import (
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

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

const forkRemoveRichScenarioEnvironment = "YCY_CONFIG_FORK_REMOVE_RICH_SCENARIO"

type forkRemovePTYStep struct {
	needle string
	input  string
}

func TestRunForkRemoveRichPTYRestoresScreenAndProjectsTranscript(t *testing.T) {
	const helperEnvironment = "YCY_CONFIG_FORK_REMOVE_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runForkRemoveRichPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name          string
		width, height uint16
		color         bool
	}{
		{name: "wide color", width: 120, height: 40, color: true},
		{name: "wide no color", width: 120, height: 40, color: false},
		{name: "compact color", width: 40, height: 15, color: true},
		{name: "compact no color", width: 40, height: 15, color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunForkRemoveRichPTYRestoresScreenAndProjectsTranscript$")
			command.Env = append(forkRemovePTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
			if !testCase.color {
				command.Env = append(command.Env, "NO_COLOR=1")
			}
			output := runForkRemovePTYProcess(t, command, testCase.width, testCase.height)
			assertForkRemoveRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func TestRunForkRemoveRichPTYSafelyCancelsAndFails(t *testing.T) {
	if scenario := os.Getenv(forkRemoveRichScenarioEnvironment); scenario != "" {
		runForkRemoveRichPTYScenarioHelper(t, scenario)
		return
	}

	for _, testCase := range []struct {
		name          string
		scenario      string
		width, height uint16
		color         bool
		steps         []forkRemovePTYStep
	}{
		{name: "empty configuration", scenario: "empty", width: 120, height: 40, color: false},
		{name: "selection cancellation", scenario: "selection-cancel", width: 40, height: 15, color: false, steps: []forkRemovePTYStep{{needle: "single selection", input: "\x03"}}},
		{name: "confirmation cancellation", scenario: "confirmation-cancel", width: 120, height: 40, color: false, steps: []forkRemovePTYStep{{needle: "Select instance to remove", input: "\r"}, {needle: `Remove instance "work"?`, input: "\x03"}}},
		{name: "confirmation decline", scenario: "declined", width: 120, height: 40, color: true, steps: []forkRemovePTYStep{{needle: "Select instance to remove", input: "\r"}, {needle: `Remove instance "work"?`, input: "\r"}}},
		{name: "load failure", scenario: "load-failure", width: 120, height: 40, color: false},
		{name: "removal failure", scenario: "remove-failure", width: 120, height: 40, color: true, steps: []forkRemovePTYStep{{needle: "Select instance to remove", input: "\r"}, {needle: `Remove instance "work"?`, input: "y\r"}}},
		{name: "load context cancellation", scenario: "load-context-cancel", width: 120, height: 40, color: false},
		{name: "removal context cancellation", scenario: "remove-context-cancel", width: 120, height: 40, color: false, steps: []forkRemovePTYStep{{needle: "Select instance to remove", input: "\r"}, {needle: `Remove instance "work"?`, input: "y\r"}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := newForkRemovePTYCommand("TestRunForkRemoveRichPTYSafelyCancelsAndFails", testCase.scenario, testCase.color)
			output := runForkRemovePTYScenarioProcess(t, command, testCase.width, testCase.height, testCase.steps)
			assertForkRemoveRichPTYScenario(t, output, testCase.scenario, testCase.color)
		})
	}
}

func runForkRemoveRichPTYHelper(t *testing.T) {
	t.Helper()
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	err := runRemove(&Options{
		Context:  context.Background(),
		Terminal: experience,
		Store: func() (RemoveReader, RemoveWriter, error) {
			return forkRemoveReaderFunc(func() ([]appconfig.ForkInstance, error) {
					return []appconfig.ForkInstance{{
						Name: "work",
						Host: "https://user:password@gitlab.example/v1?token=hidden#fragment",
					}}, nil
				}), forkRemoveWriterFunc(func(name string) (bool, error) {
					if name != "work" {
						return false, fmt.Errorf("unexpected instance %q", name)
					}
					_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_WRITE_OK")
					return true, nil
				}), nil
		},
	})
	if err != nil {
		t.Fatalf("runRemove() error = %v", err)
	}
}

func runForkRemoveRichPTYScenarioHelper(t *testing.T, scenario string) {
	t.Helper()
	ctx := context.Background()
	cancel := func() {}
	if scenario == "load-context-cancel" || scenario == "remove-context-cancel" {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	loadFailure := errors.New("read provider configuration")
	removeFailure := errors.New("write provider configuration")
	var writes int
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	err := runRemove(&Options{
		Context:  ctx,
		Terminal: experience,
		Store: func() (RemoveReader, RemoveWriter, error) {
			reader := forkRemoveReaderFunc(func() ([]appconfig.ForkInstance, error) {
				switch scenario {
				case "empty":
					return nil, nil
				case "load-failure":
					return nil, loadFailure
				case "load-context-cancel":
					cancel()
				}
				return []appconfig.ForkInstance{{Name: "work", Host: "https://user:password@gitlab.example/v1?token=hidden#fragment"}}, nil
			})
			writer := forkRemoveWriterFunc(func(name string) (bool, error) {
				writes++
				if name != "work" {
					t.Fatalf("writer received unexpected instance %q", name)
				}
				switch scenario {
				case "remove-failure":
					_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_WRITE_ATTEMPT")
					return false, removeFailure
				case "remove-context-cancel":
					_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_WRITE_ATTEMPT")
					cancel()
					return false, context.Canceled
				default:
					return true, nil
				}
			})
			return reader, writer, nil
		},
	})

	switch scenario {
	case "empty":
		if err != nil || writes != 0 {
			t.Fatalf("runRemove() = (%v, writes=%d), want empty success without mutation", err, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_EMPTY_OK")
	case "selection-cancel":
		if err != nil || writes != 0 {
			t.Fatalf("runRemove() = (%v, writes=%d), want selection cancellation", err, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_SELECTION_CANCEL_OK")
	case "confirmation-cancel":
		if err != nil || writes != 0 {
			t.Fatalf("runRemove() = (%v, writes=%d), want confirmation cancellation", err, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_CONFIRMATION_CANCEL_OK")
	case "declined":
		if err != nil || writes != 0 {
			t.Fatalf("runRemove() = (%v, writes=%d), want confirmation decline", err, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_DECLINED_OK")
	case "load-failure":
		if !errors.Is(err, loadFailure) || writes != 0 {
			t.Fatalf("runRemove() = (%v, writes=%d), want load failure without mutation", err, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_LOAD_FAILURE_OK")
	case "remove-failure":
		if !errors.Is(err, removeFailure) || writes != 1 {
			t.Fatalf("runRemove() = (%v, writes=%d), want one removal failure", err, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_FAILURE_OK")
	case "load-context-cancel":
		if !errors.Is(err, context.Canceled) || writes != 0 {
			t.Fatalf("runRemove() = (%v, writes=%d), want load context cancellation", err, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_LOAD_CONTEXT_CANCEL_OK")
	case "remove-context-cancel":
		if !errors.Is(err, context.Canceled) || writes != 1 {
			t.Fatalf("runRemove() = (%v, writes=%d), want writer context cancellation", err, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_REMOVE_REMOVE_CONTEXT_CANCEL_OK")
	default:
		t.Fatalf("unexpected Rich PTY scenario %q", scenario)
	}
}

func runForkRemovePTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()
	if err := process.Resize(width, height); err != nil {
		t.Fatalf("resize PTY to %dx%d: %v", width, height, err)
	}
	var output lockedForkRemovePTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	firstNeedle := "Select instance to remove"
	secondNeedle := `Remove instance "work"?`
	if width < 70 {
		// The compact live view bounds the prompt label; wait for its stable
		// semantic context before submitting the default selection.
		firstNeedle = "single selection"
		secondNeedle = `"work"?`
	}
	for _, step := range []struct {
		needle string
		input  string
	}{
		{needle: firstNeedle, input: "\r"},
		{needle: secondNeedle, input: "y\r"},
	} {
		waitForForkRemovePTYText(t, &output, step.needle)
		if _, err := process.Terminal().Write([]byte(step.input)); err != nil {
			t.Fatalf("write PTY input for %q: %v", step.needle, err)
		}
	}
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

func newForkRemovePTYCommand(testName, scenario string, color bool) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	command.Env = append(forkRemovePTYEnvironment(), forkRemoveRichScenarioEnvironment+"="+scenario, "TERM=xterm-256color")
	if !color {
		command.Env = append(command.Env, "NO_COLOR=1")
	}
	return command
}

func runForkRemovePTYScenarioProcess(t *testing.T, command *exec.Cmd, width, height uint16, steps []forkRemovePTYStep) string {
	t.Helper()
	process, err := terminaltest.StartPTYWithSize(command, width, height)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()
	var output lockedForkRemovePTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	for _, step := range steps {
		waitForForkRemovePTYText(t, &output, step.needle)
		if _, err := process.Terminal().Write([]byte(step.input)); err != nil {
			t.Fatalf("write PTY input for %q: %v", step.needle, err)
		}
	}
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

func assertForkRemoveRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	expected := []string{
		"YCY / config fork remove",
		"Remove fork provider instance",
		"Load fork provider instances",
		"Remove provider instance",
		"FORK_REMOVE_WRITE_OK",
		"Instance work removed",
	}
	if wide {
		expected = append(expected,
			"Choose a configured provider connection to remove",
			"Select instance to remove",
			`Remove instance "work"?`,
			"Host: gitlab.example/v1",
			`Remove instance "work": confirmed`,
		)
	} else {
		expected = append(expected, "STATE", "PHASE", "DETAIL", "single selection", "Remove instance", `"work"?`)
	}
	for _, expected := range expected {
		if !forkRemovePTYContains(visible, expected) {
			t.Fatalf("Rich PTY output missing %q: %q", expected, output)
		}
	}
	for _, secret := range []string{"user", "password", "token=hidden", "fragment"} {
		if strings.Contains(output, secret) {
			t.Fatalf("Rich PTY output leaked %q: %q", secret, output)
		}
	}
	leave := assertForkRemoveRichPTYRestored(t, output)
	transcript := terminaltest.StripANSI(visible[leave:])
	ordered := []string{
		"ANSWERS",
		"Selected instance: work",
		"WORK",
		"Load fork provider instances (completed)",
		"Host: gitlab.example/v1",
		`Remove instance "work": confirmed`,
		"Remove provider instance (completed)",
		"OUTCOME  succeeded",
		"Instance work removed",
	}
	last := 0
	for _, expected := range ordered {
		next := strings.Index(transcript[last:], expected)
		if next < 0 {
			t.Fatalf("Rich PTY transcript missing ordered event %q: %q", expected, output)
		}
		last += next + len(expected)
	}
	if !wide {
		compact := strings.Join(strings.Fields(terminaltest.StripANSI(visible)), " ")
		if !strings.Contains(compact, "Remove instance \"work\"?") && !strings.Contains(compact, "confirmation") {
			t.Fatalf("compact fork remove confirmation missing: %q", output)
		}
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func assertForkRemoveRichPTYScenario(t *testing.T, output, scenario string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	leave := assertForkRemoveRichPTYRestored(t, output)
	transcript := terminaltest.StripANSI(visible[leave:])
	ordered := map[string][]string{
		"empty": {
			"WORK",
			"Load fork provider instances (completed)",
			"No instances configured",
			"OUTCOME  succeeded",
			"Nothing to remove",
			"FORK_REMOVE_EMPTY_OK",
		},
		"selection-cancel": {
			"WORK",
			"Load fork provider instances (completed)",
			"Selection cancelled",
			"OUTCOME  cancelled",
			"Cancelled",
			"FORK_REMOVE_SELECTION_CANCEL_OK",
		},
		"confirmation-cancel": {
			"ANSWERS",
			"Selected instance: work",
			"WORK",
			"Load fork provider instances (completed)",
			"Host: gitlab.example/v1",
			"Confirmation cancelled",
			"OUTCOME  cancelled",
			"Cancelled",
			"FORK_REMOVE_CONFIRMATION_CANCEL_OK",
		},
		"declined": {
			"ANSWERS",
			"Selected instance: work",
			"WORK",
			"Load fork provider instances (completed)",
			"Host: gitlab.example/v1",
			"Removal declined",
			"OUTCOME  cancelled",
			"Cancelled",
			"FORK_REMOVE_DECLINED_OK",
		},
		"load-failure": {
			"Load fork provider instances (failed)",
			"OUTCOME  failed",
			"Unable to load fork provider instances",
			"FORK_REMOVE_LOAD_FAILURE_OK",
		},
		"remove-failure": {
			"ANSWERS",
			"Selected instance: work",
			"WORK",
			"Load fork provider instances (completed)",
			"Host: gitlab.example/v1",
			`Remove instance "work": confirmed`,
			"Remove provider instance (failed)",
			"OUTCOME  failed",
			"Unable to remove provider instance",
			"FORK_REMOVE_FAILURE_OK",
		},
		"load-context-cancel": {
			"WORK",
			"Load fork provider instances (cancelled)",
			"OUTCOME  cancelled",
			"Provider removal cancelled",
			"FORK_REMOVE_LOAD_CONTEXT_CANCEL_OK",
		},
		"remove-context-cancel": {
			"ANSWERS",
			"Selected instance: work",
			"WORK",
			"Load fork provider instances (completed)",
			"Host: gitlab.example/v1",
			`Remove instance "work": confirmed`,
			"Remove provider instance (failed)",
			"OUTCOME  failed",
			"Unable to remove provider instance",
			"FORK_REMOVE_REMOVE_CONTEXT_CANCEL_OK",
		},
	}[scenario]
	if len(ordered) == 0 {
		t.Fatalf("unexpected Rich PTY scenario %q", scenario)
	}
	last := 0
	for _, expected := range ordered {
		next := strings.Index(transcript[last:], expected)
		if next < 0 {
			t.Fatalf("Rich PTY scenario %q missing ordered event %q: %q", scenario, expected, output)
		}
		last += next + len(expected)
	}
	for _, forbidden := range []string{"user", "password", "token=hidden", "fragment", "read provider configuration", "write provider configuration", "Instance work removed"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("Rich PTY scenario %q contains forbidden %q: %q", scenario, forbidden, output)
		}
	}
	if scenario == "empty" || strings.Contains(scenario, "cancel") || scenario == "declined" || strings.Contains(scenario, "failure") {
		if strings.Contains(output, "FORK_REMOVE_WRITE_OK") || strings.Contains(output, "Instance work removed") {
			t.Fatalf("Rich PTY scenario %q emitted synthetic success: %q", scenario, output)
		}
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY scenario %q contains %q: %q", scenario, prefix, output)
			}
		}
	}
}

func assertForkRemoveRichPTYRestored(t *testing.T, output string) int {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.LastIndex(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	return leave
}

func forkRemovePTYEnvironment() []string {
	ignored := map[string]struct{}{"CI": {}, "CLICOLOR": {}, "CLICOLOR_FORCE": {}, "COLORTERM": {}, "NO_COLOR": {}, "TERM": {}}
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := ignored[key]; !skip {
			environment = append(environment, entry)
		}
	}
	return environment
}

type lockedForkRemovePTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedForkRemovePTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedForkRemovePTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func waitForForkRemovePTYText(t *testing.T, output *lockedForkRemovePTYBuffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if forkRemovePTYContains(output.String(), needle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PTY text %q: %q", needle, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func forkRemovePTYContains(value, needle string) bool {
	if strings.Contains(value, needle) {
		return true
	}
	return strings.Contains(
		strings.Join(strings.Fields(terminaltest.StripANSI(value)), " "),
		strings.Join(strings.Fields(needle), " "),
	)
}
