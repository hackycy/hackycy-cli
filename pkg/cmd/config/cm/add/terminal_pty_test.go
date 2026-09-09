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

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

const cmAddRichPTYHelperEnvironment = "YCY_CONFIG_CM_ADD_RICH_HELPER"

type cmAddPTYStep struct {
	needle string
	prompt string
	input  string
}

var cmAddCompleteFormPTYSteps = []cmAddPTYStep{
	{needle: "Profile name", prompt: "Profile name", input: "work\r"},
	{needle: "OpenAI-compatible base URL", prompt: "URL", input: "https://provider.example/v1\r"},
	{needle: "Model", prompt: "Model", input: "gpt-4.1-mini\r"},
	{needle: "API key", prompt: "API key", input: "secret-api-key\r"},
}

func TestRunCMAddRichPTYRestoresScreenAndRedactsTranscript(t *testing.T) {
	if scenario := os.Getenv(cmAddRichPTYHelperEnvironment); scenario != "" {
		runCMAddRichPTYHelper(t, scenario)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunCMAddRichPTYRestoresScreenAndRedactsTranscript$")
			command.Env = append(cmAddPTYEnvironment(), cmAddRichPTYHelperEnvironment+"=success", "TERM=xterm-256color")
			if !testCase.color {
				command.Env = append(command.Env, "NO_COLOR=1")
			}
			output := runCMAddPTYProcess(t, command, testCase.width, testCase.height, cmAddCompleteFormPTYSteps)
			assertCMAddRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func TestRunCMAddRichPTYFailureBranches(t *testing.T) {
	if scenario := os.Getenv(cmAddRichPTYHelperEnvironment); scenario != "" {
		runCMAddRichPTYHelper(t, scenario)
		return
	}

	for _, testCase := range []struct {
		name          string
		scenario      string
		width, height uint16
		color         bool
		steps         []cmAddPTYStep
	}{
		{
			name:     "store failure",
			scenario: "store-failure",
			width:    120,
			height:   40,
			color:    true,
		},
		{
			name:     "save failure",
			scenario: "save-failure",
			width:    120,
			height:   40,
			color:    false,
			steps:    cmAddCompleteFormPTYSteps,
		},
		{
			name:     "form cancellation",
			scenario: "form-cancel",
			width:    40,
			height:   15,
			color:    false,
			steps:    []cmAddPTYStep{{needle: "Profile name", prompt: "Profile name", input: "\x03"}},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunCMAddRichPTYFailureBranches$")
			command.Env = append(cmAddPTYEnvironment(), cmAddRichPTYHelperEnvironment+"="+testCase.scenario, "TERM=xterm-256color")
			if !testCase.color {
				command.Env = append(command.Env, "NO_COLOR=1")
			}
			output := runCMAddPTYProcess(t, command, testCase.width, testCase.height, testCase.steps)
			assertCMAddRichFailurePTYOutput(t, output, testCase.scenario, testCase.color)
		})
	}
}

func runCMAddRichPTYHelper(t *testing.T, scenario string) {
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
	storeFailure := errors.New("load CM configuration")
	writerFailure := errors.New("save CM profile")
	storeCalls, writes := 0, 0
	err := runAdd(&Options{
		Context:  context.Background(),
		Terminal: experience,
		Store: func() (AddWriter, error) {
			storeCalls++
			if scenario == "store-failure" {
				return nil, storeFailure
			}
			return cmAddWriterFunc(func(_, _, _, _ string) error {
				writes++
				if scenario == "save-failure" {
					_, _ = fmt.Fprintln(os.Stderr, "CM_ADD_WRITE_ATTEMPT")
					return writerFailure
				}
				_, _ = fmt.Fprintln(os.Stderr, "CM_ADD_WRITE_OK")
				return nil
			}), nil
		},
	})
	switch scenario {
	case "success":
		if err != nil || storeCalls != 1 || writes != 1 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want one successful write", err, storeCalls, writes)
		}
	case "store-failure":
		if !errors.Is(err, storeFailure) || storeCalls != 1 || writes != 0 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want store failure before write", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "CM_ADD_STORE_FAILURE_OK")
	case "save-failure":
		if !errors.Is(err, writerFailure) || storeCalls != 1 || writes != 1 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want one writer failure", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "CM_ADD_SAVE_FAILURE_OK")
	case "form-cancel":
		if err != nil || storeCalls != 1 || writes != 0 {
			t.Fatalf("runAdd() = (%v, store=%d, writes=%d), want cancellation before write", err, storeCalls, writes)
		}
		_, _ = fmt.Fprintln(os.Stderr, "CM_ADD_FORM_CANCEL_OK")
	default:
		t.Fatalf("unknown CM add Rich PTY scenario %q", scenario)
	}
}

func runCMAddPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, steps []cmAddPTYStep) string {
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
	var output lockedCMAddPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	steps = append([]cmAddPTYStep(nil), steps...)
	if width < 70 && len(steps) > 1 {
		// Compact B activity rows can wrap the endpoint label before its full text
		// reaches the PTY buffer.
		steps[1].needle = "base"
	}
	after := 0
	for _, step := range steps {
		waitForCMAddPTYTextAfter(t, &output, after, step.needle)
		waitForCMAddPTYPromptAfter(t, &output, after, step.prompt)
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

func assertCMAddRichFailurePTYOutput(t *testing.T, output, scenario string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	var expected []string
	switch scenario {
	case "store-failure":
		expected = []string{"Location: Collect CM profile details", "Unable to collect CM profile details", "CM_ADD_STORE_FAILURE_OK"}
	case "save-failure":
		expected = []string{"Location: Save CM profile", "Unable to save CM profile", "CM_ADD_WRITE_ATTEMPT", "CM_ADD_SAVE_FAILURE_OK"}
	case "form-cancel":
		expected = []string{"Location: Collect CM profile details", "Profile setup cancelled", "CM_ADD_FORM_CANCEL_OK"}
	default:
		t.Fatalf("unknown failure scenario %q", scenario)
	}
	compact := strings.Join(strings.Fields(strings.ReplaceAll(terminaltest.StripANSI(output), "\r", "")), " ")
	for _, needle := range expected {
		if !strings.Contains(compact, needle) {
			t.Fatalf("Rich PTY %s output missing %q: %q", scenario, needle, output)
		}
	}
	if strings.Contains(visible, "Profile work added") || strings.Contains(visible, "secret-api-key") {
		t.Fatalf("Rich PTY %s output contains success or secret data: %q", scenario, output)
	}
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY %s output did not restore primary screen: %q", scenario, output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY %s output contains %q: %q", scenario, prefix, output)
			}
		}
	}
}

func assertCMAddRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	assertCMAddFormCatalogPrecedesFirstPrompt(t, visible)
	expected := []string{
		"YCY / config cm add",
		"Add commit message profile",
		"Configure an OpenAI-compatible provider",
		"Collect CM profile details",
		"Save CM profile",
		"Profile name: work",
		"OpenAI-compatible base URL: https://provider.example/v1",
		"Model: gpt-4.1-mini",
		"API key: [redacted]",
		"Profile work added",
		"CM_ADD_WRITE_OK",
	}
	if !wide {
		expected = []string{"YCY / config cm add", "Collect CM profile details", "Save CM profile", "API key: [redacted]", "CM_ADD_WRITE_OK"}
	}
	for _, expected := range expected {
		if !strings.Contains(visible, expected) {
			t.Fatalf("Rich PTY output missing %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "secret-api-key") {
		t.Fatalf("Rich PTY output leaked API key: %q", output)
	}
	enter := strings.LastIndex(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	transcript := strings.Index(visible[leave:], "Collect CM profile details (completed)")
	result := strings.LastIndex(visible, "Profile work added")
	if transcript < 0 || result < 0 || leave+transcript > result {
		t.Fatalf("Rich PTY transcript/result ordering = %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func cmAddPTYEnvironment() []string {
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

type lockedCMAddPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedCMAddPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedCMAddPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func (buffer *lockedCMAddPTYBuffer) Len() int {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Len()
}

func waitForCMAddPTYText(t *testing.T, output *lockedCMAddPTYBuffer, needle string) {
	waitForCMAddPTYTextAfter(t, output, 0, needle)
}

func waitForCMAddPTYTextAfter(t *testing.T, output *lockedCMAddPTYBuffer, after int, needle string) {
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

func waitForCMAddPTYPromptAfter(t *testing.T, output *lockedCMAddPTYBuffer, after int, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		value := output.String()
		if after <= len(value) {
			visible := strings.ReplaceAll(terminaltest.StripANSI(value[after:]), "\r\n", "\n")
			if prompt := strings.LastIndex(visible, needle); prompt >= 0 && cmAddPTYPromptInputReady(visible[prompt+len(needle):]) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for ready PTY prompt %q: %q", needle, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func cmAddPTYPromptInputReady(value string) bool {
	if !strings.HasPrefix(value, "\n") {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(value[1:], " \t"), "> ")
}

func assertCMAddFormCatalogPrecedesFirstPrompt(t *testing.T, output string) {
	t.Helper()
	firstPrompt := strings.Index(output, "Profile name")
	if firstPrompt < 0 {
		t.Fatalf("Rich PTY output does not contain the first prompt: %q", output)
	}
	for _, row := range []string{"Identity", "Endpoint", "Model", "Credential", "[redacted]"} {
		if index := strings.Index(output, row); index < 0 || index >= firstPrompt {
			t.Fatalf("Rich PTY Form Catalog row %q does not precede the first prompt: %q", row, output)
		}
	}
}
