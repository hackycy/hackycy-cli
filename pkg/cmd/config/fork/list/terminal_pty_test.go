package list

import (
	"context"
	"errors"
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

func TestRunForkListRichPTYUsesBConsoleAndRestoresPrimaryScreen(t *testing.T) {
	const helperEnvironment = "YCY_CONFIG_FORK_LIST_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runForkListRichPTYHelper(t)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunForkListRichPTYUsesBConsoleAndRestoresPrimaryScreen$")
			command.Env = environmentWith(map[string]string{
				"NO_COLOR":                       map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                           "xterm-256color",
				helperEnvironment:                "1",
				"YCY_CONFIG_FORK_LIST_PTY_START": "1",
			})
			output := runForkListPTYProcess(t, command, testCase.width, testCase.height)
			assertForkListRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runForkListRichPTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_CONFIG_FORK_LIST_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}

	color := os.Getenv("NO_COLOR") == ""
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: color},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: color},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	err := runList(&Options{
		Context: context.Background(),
		Store: func() (Reader, error) {
			// Keep the active phase on screen long enough for the PTY capture to
			// observe both the active and completed B projections.
			time.Sleep(100 * time.Millisecond)
			return fakeReader{instances: []appconfig.ForkInstance{{
				Name:         "work",
				Host:         "gitlab.example",
				Scheme:       "https",
				Type:         "gitlab",
				TokenPreview: "MDEy***",
			}}}, nil
		},
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}
}

func runForkListPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
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

	var output lockedForkListPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("x\n")); err != nil {
		t.Fatalf("release PTY helper after sizing: %v", err)
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

func assertForkListRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	enter := strings.Index(output, "\x1b[?1049h")
	leave := strings.LastIndex(output, "\x1b[?1049l")
	if strings.Count(output, "\x1b[?1049h") != 1 || strings.Count(output, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}

	live := forkListPTYText(output[enter:leave])
	expected := []string{
		"YCY / config fork list",
		"scope git fork configuration",
		"Load fork provider instances",
		"Loading fork provider instances",
		"DONE",
		"SUCCEEDED",
		"Loaded 1 fork provider instance",
	}
	if wide {
		expected = append(expected, "provider inventory")
	} else {
		// The compact bar is intentionally width-bounded; the target remains
		// identifiable without forcing a second header or horizontal scroll.
		expected = append(expected, "provider")
	}
	for _, expected := range expected {
		if !strings.Contains(live, expected) {
			t.Fatalf("Rich PTY live Console omitted %q: %q", expected, output)
		}
	}
	state := strings.Index(live, "STATE")
	phase := strings.Index(live, "PHASE")
	detail := strings.Index(live, "DETAIL")
	if state < 0 || phase < state || detail < phase {
		t.Fatalf("Rich PTY B table heading order = %q", output)
	}
	if strings.Contains(live, "FLOW") || strings.Contains(live, "[done]") || strings.Contains(live, "[active]") {
		t.Fatalf("Rich PTY live Console retained a non-B hierarchy: %q", output)
	}

	postLive := output[leave:]
	resultStart := strings.Index(postLive, "YCY / config fork list")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result did not start after the Transcript: %q", output)
	}
	transcript := forkListPTYText(postLive[:resultStart])
	result := forkListPTYText(postLive[resultStart:])
	for _, expected := range []string{
		"Load fork provider instances (completed): Loaded 1 fork provider instances",
		"succeeded: Loaded 1 fork provider instance",
	} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Rich PTY transcript/result omitted %q: %q", expected, output)
		}
	}
	for _, forbidden := range []string{"work", "gitlab", "MDEy***", "NAME", "Fork provider instances"} {
		if strings.Contains(transcript, forbidden) {
			t.Fatalf("Rich PTY Transcript leaked %q: %q", forbidden, output)
		}
	}
	for _, expected := range []string{"Fork provider instances", "MDEy***", "1 instance configured"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("Rich PTY durable result omitted %q: %q", expected, output)
		}
	}
	if strings.Index(transcript, "succeeded") < 0 {
		t.Fatalf("Rich PTY Transcript omitted the outcome before the result: %q", output)
	}
	if color {
		if !strings.Contains(output, "\x1b[38") {
			t.Fatalf("color Rich PTY omitted B styling: %q", output)
		}
		return
	}
	for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
		if strings.Contains(output, prefix) {
			t.Fatalf("NO_COLOR Rich PTY contains %q: %q", prefix, output)
		}
	}
}

func forkListPTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

type lockedForkListPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedForkListPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedForkListPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
