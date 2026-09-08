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
		if !strings.Contains(visible, expected) {
			t.Fatalf("Rich PTY output missing %q: %q", expected, output)
		}
	}
	for _, secret := range []string{"user", "password", "token=hidden", "fragment"} {
		if strings.Contains(output, secret) {
			t.Fatalf("Rich PTY output leaked %q: %q", secret, output)
		}
	}
	enter := strings.LastIndex(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	transcript := visible[leave:]
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
		if strings.Contains(output.String(), needle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for PTY text %q: %q", needle, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
