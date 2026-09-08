package use

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

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunCMUseRichPTYUsesBConsoleAndRestoresPrimaryScreen(t *testing.T) {
	const helperEnvironment = "YCY_CONFIG_CM_USE_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runCMUseRichPTYHelper(t)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunCMUseRichPTYUsesBConsoleAndRestoresPrimaryScreen$")
			command.Env = cmUseEnvironmentWith(map[string]string{
				"NO_COLOR":                    map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                        "xterm-256color",
				helperEnvironment:             "1",
				"YCY_CONFIG_CM_USE_PTY_START": "1",
			})
			output := runCMUsePTYProcess(t, command, testCase.width, testCase.height)
			assertCMUseRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runCMUseRichPTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_CONFIG_CM_USE_PTY_START") == "1" {
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
	writer := &delayedCMUseWriter{}
	err := executeUse(&Options{
		Context: context.Background(),
		Profile: "work",
		Store: func() (UseWriter, error) {
			time.Sleep(100 * time.Millisecond)
			return writer, nil
		},
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("executeUse() error = %v", err)
	}
	if got := writer.names(); len(got) != 1 || got[0] != "work" {
		t.Fatalf("writer calls = %#v", got)
	}
}

func runCMUsePTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
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

	var output lockedCMUsePTYBuffer
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

func assertCMUseRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	enter := strings.Index(output, "\x1b[?1049h")
	leave := strings.LastIndex(output, "\x1b[?1049l")
	if strings.Count(output, "\x1b[?1049h") != 1 || strings.Count(output, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}

	live := cmUsePTYText(output[enter:leave])
	expected := []string{
		"YCY / config cm use",
		"scope commit message configuration",
		"Set default CM profile",
		"Checking profile and saving selection",
		"Profile: work",
		"DONE",
		"SUCCEEDED",
	}
	if wide {
		expected = append(expected, "profile selection", "Default CM profile set")
	} else {
		expected = append(expected, "profile")
	}
	for _, needle := range expected {
		if !strings.Contains(live, needle) {
			t.Fatalf("Rich PTY live Console omitted %q: %q", needle, output)
		}
	}
	state := strings.Index(live, "STATE")
	phase := strings.Index(live, "PHASE")
	detail := strings.Index(live, "DETAIL")
	if state < 0 || phase < state || detail < phase {
		t.Fatalf("Rich PTY B table heading order = %q", output)
	}

	postLive := output[leave:]
	resultStart := strings.Index(postLive, "YCY / config cm use")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result did not start after the Transcript: %q", output)
	}
	transcript := cmUsePTYText(postLive[:resultStart])
	result := cmUsePTYText(postLive[resultStart:])
	for _, needle := range []string{"Set default CM profile (completed): Profile: work", "succeeded: Default CM profile set"} {
		if !strings.Contains(transcript, needle) {
			t.Fatalf("Rich PTY Transcript omitted %q: %q", needle, output)
		}
	}
	for _, forbidden := range []string{"api", "secret", "baseURL", "model"} {
		if strings.Contains(transcript, forbidden) {
			t.Fatalf("Rich PTY Transcript leaked %q: %q", forbidden, output)
		}
	}
	for _, needle := range []string{"Set default CM profile", "Default CM profile set to work"} {
		if !strings.Contains(result, needle) {
			t.Fatalf("Rich PTY durable result omitted %q: %q", needle, output)
		}
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

func TestRunCMUseStreamsPreservePlainAutomationAndFailureBoundaries(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		mode       terminalexperience.InteractionMode
		wantOutput string
		wantDiag   string
	}{
		{name: "plain", mode: terminalexperience.PlainInteractive, wantOutput: "Default CM profile set to work\n", wantDiag: "Setting default CM profile...\n"},
		{name: "automation", mode: terminalexperience.Automation, wantOutput: "Default CM profile set to work\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: testCase.mode},
				Output:       &output,
				Diagnostics:  &diagnostics,
			})
			writer := &delayedCMUseWriter{}
			if err := executeUse(&Options{Context: context.Background(), Profile: "work", Store: func() (UseWriter, error) { return writer, nil }, Terminal: experience}); err != nil {
				t.Fatalf("executeUse() error = %v", err)
			}
			if output.String() != testCase.wantOutput || diagnostics.String() != testCase.wantDiag {
				t.Fatalf("streams = output %q, diagnostics %q", output.String(), diagnostics.String())
			}
			if terminaltest.ContainsTerminalControl(output.Bytes()) || terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
				t.Fatalf("non-rich streams contain terminal control: output=%q diagnostics=%q", output.String(), diagnostics.String())
			}
		})
	}

	failure := errors.New("CM profile not found: missing")
	var output, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       &output,
		Diagnostics:  &diagnostics,
	})
	if err := executeUse(&Options{Context: context.Background(), Profile: "missing", Store: func() (UseWriter, error) {
		return &delayedCMUseWriter{err: failure}, nil
	}, Terminal: experience}); !errors.Is(err, failure) {
		t.Fatalf("executeUse() failure = %v, want %v", err, failure)
	}
	if output.Len() != 0 || diagnostics.Len() != 0 {
		t.Fatalf("failed Automation streams = output %q, diagnostics %q", output.String(), diagnostics.String())
	}
}

func cmUsePTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

func cmUseEnvironmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

type lockedCMUsePTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedCMUsePTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedCMUsePTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

type delayedCMUseWriter struct {
	mu     sync.Mutex
	called []string
	err    error
}

func (writer *delayedCMUseWriter) SetDefaultCMProfile(name string) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.called = append(writer.called, name)
	return writer.err
}

func (writer *delayedCMUseWriter) names() []string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return append([]string(nil), writer.called...)
}
