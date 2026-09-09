package add

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

const (
	forkAddRichPTYHelperEnvironment  = "YCY_CONFIG_FORK_ADD_RICH_HELPER"
	forkAddPlainPTYHelperEnvironment = "YCY_CONFIG_FORK_ADD_PLAIN_HELPER"
)

type forkAddPTYStep struct {
	needle string
	input  string
}

var forkAddCompleteFormPTYSteps = []forkAddPTYStep{
	{needle: "Instance name (alias)", input: "work\r"},
	{needle: "Host", input: "gitlab.example\r"},
	{needle: "Provider type", input: "\r"},
	{needle: "Protocol", input: "\r"},
	{needle: "Access token", input: "secret-token\r"},
}

func TestRunForkAddRichPTYRestoresScreenAndRedactsTranscript(t *testing.T) {
	if scenario := os.Getenv(forkAddRichPTYHelperEnvironment); scenario != "" {
		runForkAddRichPTYHelper(t, scenario)
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
			command := newForkAddPTYCommand("TestRunForkAddRichPTYRestoresScreenAndRedactsTranscript", forkAddRichPTYHelperEnvironment, "success", testCase.color)
			output := runForkAddPTYProcess(t, command, testCase.width, testCase.height, forkAddCompleteFormPTYSteps)
			assertForkAddRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runForkAddRichPTYHelper(t *testing.T, scenario string) {
	t.Helper()
	ctx := context.Background()
	cancel := func() {}
	if scenario == "save-context-cancel" {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	var storeCalls, writes int
	failure := errors.New("write provider configuration")
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
	err := runAdd(&Options{
		Context:  ctx,
		Terminal: experience,
		Store: func() (AddWriter, error) {
			storeCalls++
			return forkAddWriterFunc(func(name string, input appconfig.ForkInput) error {
				writes++
				if name != "work" || input.Host != "gitlab.example" || input.Type != "gitlab" || input.Scheme != "https" || input.Token != "secret-token" {
					t.Fatal("writer received an unexpected validated provider input")
				}
				switch scenario {
				case "success":
					_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_WRITE_OK")
					return nil
				case "save-failure":
					_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_WRITE_ATTEMPT")
					return failure
				case "save-context-cancel":
					_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_WRITE_ATTEMPT")
					cancel()
					return context.Canceled
				default:
					t.Fatalf("unexpected Rich PTY writer scenario %q", scenario)
					return nil
				}
			}), nil
		},
	})
	switch scenario {
	case "success":
		if err != nil || storeCalls != 1 || writes != 1 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want successful one-write execution", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_SUCCESS_OK")
	case "save-failure":
		if !errors.Is(err, failure) || storeCalls != 1 || writes != 1 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want one writer failure", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_SAVE_FAILURE_OK")
	case "save-context-cancel":
		if !errors.Is(err, context.Canceled) || storeCalls != 1 || writes != 1 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want one writer context cancellation", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_SAVE_CONTEXT_CANCEL_OK")
	case "form-cancel":
		if err != nil || storeCalls != 0 || writes != 0 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want form cancellation before storage", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_FORM_CANCEL_OK")
	default:
		t.Fatalf("unknown Rich PTY scenario %q", scenario)
	}
}

func newForkAddPTYCommand(testName, helperEnvironment, scenario string, color bool) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	command.Env = append(forkAddPTYEnvironment(), helperEnvironment+"="+scenario, "TERM=xterm-256color")
	if !color {
		command.Env = append(command.Env, "NO_COLOR=1")
	}
	return command
}

func runForkAddPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, steps []forkAddPTYStep) string {
	t.Helper()
	process, err := terminaltest.StartPTYWithSize(command, width, height)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()
	var output lockedForkAddPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	after := 0
	for _, step := range steps {
		waitForForkAddPTYTextAfter(t, &output, after, step.needle)
		// A Catalog row can render before its Huh field has accepted input. Let
		// the form transition settle so a following value is not truncated.
		time.Sleep(75 * time.Millisecond)
		after = output.Len()
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

func TestRunForkAddRichPTYSafelyCancelsAndFails(t *testing.T) {
	if scenario := os.Getenv(forkAddRichPTYHelperEnvironment); scenario != "" {
		runForkAddRichPTYHelper(t, scenario)
		return
	}

	for _, testCase := range []struct {
		name          string
		scenario      string
		width, height uint16
		color         bool
		steps         []forkAddPTYStep
	}{
		{name: "save failure", scenario: "save-failure", width: 120, height: 40, color: true, steps: forkAddCompleteFormPTYSteps},
		{name: "writer context cancellation", scenario: "save-context-cancel", width: 120, height: 40, color: false, steps: forkAddCompleteFormPTYSteps},
		{name: "form cancellation", scenario: "form-cancel", width: 40, height: 15, color: false, steps: []forkAddPTYStep{{needle: "Instance name (alias)", input: "\x03"}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := newForkAddPTYCommand("TestRunForkAddRichPTYSafelyCancelsAndFails", forkAddRichPTYHelperEnvironment, testCase.scenario, testCase.color)
			output := runForkAddPTYProcess(t, command, testCase.width, testCase.height, testCase.steps)
			switch testCase.scenario {
			case "save-failure":
				assertForkAddRichPTYSaveFailure(t, output, "FORK_ADD_SAVE_FAILURE_OK", testCase.color)
			case "save-context-cancel":
				assertForkAddRichPTYSaveFailure(t, output, "FORK_ADD_SAVE_CONTEXT_CANCEL_OK", testCase.color)
			case "form-cancel":
				assertForkAddRichPTYFormCancellation(t, output, testCase.color)
			}
		})
	}
}

func TestRunForkAddPlainPTYPreservesLifecycleAndRedirectedOutput(t *testing.T) {
	if scenario := os.Getenv(forkAddPlainPTYHelperEnvironment); scenario != "" {
		runForkAddPlainPTYHelper(t, scenario)
		return
	}

	for _, testCase := range []struct {
		name     string
		scenario string
	}{
		{name: "success", scenario: "success"},
		{name: "save failure", scenario: "save-failure"},
		{name: "redirected output", scenario: "redirected-success"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := newForkAddPTYCommand("TestRunForkAddPlainPTYPreservesLifecycleAndRedirectedOutput", forkAddPlainPTYHelperEnvironment, testCase.scenario, false)
			output := runForkAddPTYProcess(t, command, 120, 40, forkAddCompleteFormPTYSteps)
			assertForkAddPlainPTYOutput(t, output, testCase.scenario)
		})
	}
}

func runForkAddPlainPTYHelper(t *testing.T, scenario string) {
	t.Helper()
	failure := errors.New("write provider configuration")
	var storeCalls, writes int
	redirected := scenario == "redirected-success"
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.PlainInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: !redirected},
			Stderr:      terminalexperience.StreamCapability{Terminal: !redirected},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	err := runAdd(&Options{
		Context:  context.Background(),
		Terminal: experience,
		Store: func() (AddWriter, error) {
			storeCalls++
			return forkAddWriterFunc(func(name string, input appconfig.ForkInput) error {
				writes++
				if name != "work" || input.Host != "gitlab.example" || input.Type != "gitlab" || input.Scheme != "https" || input.Token != "secret-token" {
					t.Fatal("writer received an unexpected validated provider input")
				}
				switch scenario {
				case "success", "redirected-success":
					_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_WRITE_OK")
					return nil
				case "save-failure":
					_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_WRITE_ATTEMPT")
					return failure
				default:
					t.Fatalf("unexpected Plain PTY writer scenario %q", scenario)
					return nil
				}
			}), nil
		},
	})
	switch scenario {
	case "success", "redirected-success":
		if err != nil || storeCalls != 1 || writes != 1 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want successful one-write execution", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_PLAIN_SUCCESS_OK")
	case "save-failure":
		if !errors.Is(err, failure) || storeCalls != 1 || writes != 1 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want one writer failure", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FORK_ADD_PLAIN_SAVE_FAILURE_OK")
	default:
		t.Fatalf("unknown Plain PTY scenario %q", scenario)
	}
}

func assertForkAddRichPTYSaveFailure(t *testing.T, output, marker string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	for _, expected := range []string{"Save provider instance", "Save provider instance (failed)", "Unable to save provider instance", "FORK_ADD_WRITE_ATTEMPT", marker} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("Rich PTY save failure missing %q: %q", expected, output)
		}
	}
	assertForkAddRichPTYRestored(t, output)
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Index(visible[leave:], "Save provider instance (failed)") < 0 || strings.Index(visible[leave:], "Unable to save provider instance") < 0 {
		t.Fatalf("Rich PTY save failure Transcript/outcome ordering = %q", output)
	}
	for _, forbidden := range []string{"Instance work (gitlab.example) added successfully", "Provider setup cancelled", "secret-token"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("Rich PTY save failure contains %q: %q", forbidden, output)
		}
	}
	assertForkAddPTYNoColor(t, output, color)
}

func assertForkAddRichPTYFormCancellation(t *testing.T, output string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	for _, expected := range []string{"Collect provider details (cancelled)", "Provider setup cancelled", "Cancelled", "FORK_ADD_FORM_CANCEL_OK"} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("Rich PTY form cancellation missing %q: %q", expected, output)
		}
	}
	assertForkAddRichPTYRestored(t, output)
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Index(visible[leave:], "Collect provider details (cancelled)") < 0 || strings.Index(visible[leave:], "Provider setup cancelled") < 0 {
		t.Fatalf("Rich PTY form cancellation Transcript/outcome ordering = %q", output)
	}
	for _, forbidden := range []string{"FORK_ADD_WRITE_", "Instance work (gitlab.example) added successfully", "secret-token"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("Rich PTY form cancellation contains %q: %q", forbidden, output)
		}
	}
	assertForkAddPTYNoColor(t, output, color)
}

func assertForkAddPlainPTYOutput(t *testing.T, output, scenario string) {
	t.Helper()
	assertForkAddOrderedOutput(t, output, []string{
		"Collecting provider details...",
		"Instance name (alias)",
		"Host",
		"Provider type",
		"Protocol",
		"Access token",
		"Saving provider instance...",
	})
	if strings.Contains(output, "secret-token") || terminaltest.ContainsTerminalControl([]byte(output)) {
		t.Fatalf("Plain PTY output leaked a credential or terminal control: %q", output)
	}
	if strings.Contains(output, "YCY / config fork add") {
		t.Fatalf("Plain PTY output entered the Rich console: %q", output)
	}
	switch scenario {
	case "success", "redirected-success":
		assertForkAddOrderedOutput(t, output, []string{"Saving provider instance...", "Saved provider instance", "Instance work (gitlab.example) added successfully", "FORK_ADD_PLAIN_SUCCESS_OK"})
		if strings.Count(output, "Instance work (gitlab.example) added successfully") != 1 {
			t.Fatalf("Plain PTY result was not emitted exactly once: %q", output)
		}
	case "save-failure":
		assertForkAddOrderedOutput(t, output, []string{"Saving provider instance...", "FORK_ADD_PLAIN_SAVE_FAILURE_OK"})
		for _, forbidden := range []string{"Saved provider instance", "Unable to save provider instance", "Instance work (gitlab.example) added successfully", "FORK_ADD_WRITE_OK"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("Plain PTY save failure contains %q: %q", forbidden, output)
			}
		}
	default:
		t.Fatalf("unknown Plain PTY scenario %q", scenario)
	}
}

func assertForkAddOrderedOutput(t *testing.T, output string, expected []string) {
	t.Helper()
	after := 0
	for _, needle := range expected {
		index := strings.Index(output[after:], needle)
		if index < 0 {
			t.Fatalf("output missing %q: %q", needle, output)
		}
		after += index + len(needle)
	}
}

func assertForkAddRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	expected := []string{
		"YCY / config fork add",
		"Add fork provider instance",
		"Collect provider details",
		"Save provider instance",
		"Instance name (alias): work",
		"Host: gitlab.example",
		"Provider type: GitLab",
		"Protocol: HTTPS",
		"Access token: [redacted]",
		"Instance work (gitlab.example) added successfully",
		"FORK_ADD_WRITE_OK",
	}
	if !wide {
		expected = []string{"YCY / config fork add", "Collect provider details", "Save provider instance", "Access token: [redacted]", "FORK_ADD_WRITE_OK"}
	}
	for _, expected := range expected {
		if !strings.Contains(visible, expected) {
			t.Fatalf("Rich PTY output missing %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "secret-token") {
		t.Fatalf("Rich PTY output leaked access token: %q", output)
	}
	assertForkAddRichPTYRestored(t, output)
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	transcript := strings.Index(visible[leave:], "Collect provider details (completed)")
	resultNeedle := "Instance work (gitlab.example) added successfully"
	if !wide {
		resultNeedle = "Instance work (gitlab.example) added"
	}
	result := strings.LastIndex(visible, resultNeedle)
	if transcript < 0 || result < 0 || leave+transcript > result {
		t.Fatalf("Rich PTY transcript/result ordering = %q", output)
	}
	assertForkAddPTYNoColor(t, output, color)
}

func assertForkAddRichPTYRestored(t *testing.T, output string) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.LastIndex(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
}

func assertForkAddPTYNoColor(t *testing.T, output string, color bool) {
	t.Helper()
	if color {
		return
	}
	for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
		if strings.Contains(output, prefix) {
			t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
		}
	}
}

func forkAddPTYEnvironment() []string {
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

type lockedForkAddPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedForkAddPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedForkAddPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func (buffer *lockedForkAddPTYBuffer) Len() int {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Len()
}

func waitForForkAddPTYText(t *testing.T, output *lockedForkAddPTYBuffer, needle string) {
	waitForForkAddPTYTextAfter(t, output, 0, needle)
}

func waitForForkAddPTYTextAfter(t *testing.T, output *lockedForkAddPTYBuffer, after int, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		value := output.String()
		if after <= len(value) && strings.Contains(value[after:], needle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PTY text %q: %q", needle, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
