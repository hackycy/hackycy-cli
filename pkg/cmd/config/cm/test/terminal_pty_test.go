package test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunTestRichPTYRestoresPrimaryScreenAndReplaysSafeTranscript(t *testing.T) {
	const helperEnvironment = "YCY_CONFIG_CM_TEST_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runCMTestRichPTYHelper(t)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunTestRichPTYRestoresPrimaryScreenAndReplaysSafeTranscript$")
			command.Env = cmTestPTYEnvironmentWith(map[string]string{
				"NO_COLOR":                     map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                         "xterm-256color",
				helperEnvironment:              "1",
				"YCY_CONFIG_CM_TEST_PTY_START": "1",
			})
			output := runCMTestPTYProcess(t, command, testCase.width, testCase.height)
			assertCMTestRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runCMTestRichPTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_CONFIG_CM_TEST_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
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
	err := runTest(&Options{
		Context: context.Background(),
		Store: func() (TestProfileResolver, error) {
			return &recordingCMTestResolver{profile: cmTestProfile()}, nil
		},
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)),
			}, nil
		}),
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("runTest() error = %v", err)
	}
}

func runCMTestPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
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
	var output lockedCMTestPTYBuffer
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

func assertCMTestRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	live := cmTestPTYText(visible[enter:leave])
	for _, expected := range []string{"YCY / config cm test", "Resolve CM test profile", "Test CM provider", "STATE", "PHASE", "DETAIL", "SUCCEEDED"} {
		if !strings.Contains(live, expected) {
			t.Fatalf("Rich PTY live Console missing %q: %q", expected, output)
		}
	}
	if wide {
		if !strings.Contains(live, "provider connection") || !strings.Contains(live, "non-mutating provider check") {
			t.Fatalf("wide Rich PTY omitted complete context metadata: %q", output)
		}
	} else if !strings.Contains(live, "provider") {
		t.Fatalf("compact Rich PTY omitted bounded provider context: %q", output)
	}
	if strings.Contains(live, "FLOW") || strings.Contains(live, "[done]") || strings.Contains(live, "[active]") {
		t.Fatalf("Rich PTY live Console retained a non-B hierarchy: %q", output)
	}
	postLive := visible[leave:]
	resultStart := strings.LastIndex(postLive, "YCY / config cm test")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result missing after primary-screen restoration: %q", output)
	}
	transcript := cmTestPTYText(postLive[:resultStart])
	result := cmTestPTYText(postLive[resultStart:])
	for _, expected := range []string{"Resolve CM test profile (completed)", "Test CM provider (completed): Response received", "succeeded: Response received Prompt tokens: 3 Completion tokens: 2 Total tokens: 5"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Rich PTY Transcript missing %q: %q", expected, output)
		}
	}
	for _, expected := range []string{"Test commit message provider", "Response:", "ok", "Done"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("Rich PTY result missing %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "test-api-key") {
		t.Fatalf("Rich PTY output leaked API key: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func cmTestPTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

func cmTestPTYEnvironmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := overrides[key]; !replaced {
				environment = append(environment, entry)
			}
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

type lockedCMTestPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedCMTestPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedCMTestPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
