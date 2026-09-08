package list

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

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunCMListRichPTYUsesBConsoleAndRestoresPrimaryScreen(t *testing.T) {
	const helperEnvironment = "YCY_CONFIG_CM_LIST_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runCMListRichPTYHelper(t)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunCMListRichPTYUsesBConsoleAndRestoresPrimaryScreen$")
			command.Env = cmListEnvironmentWith(map[string]string{
				"NO_COLOR":                     map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                         "xterm-256color",
				helperEnvironment:              "1",
				"YCY_CONFIG_CM_LIST_PTY_START": "1",
			})
			output := runCMListPTYProcess(t, command, testCase.width, testCase.height)
			assertCMListRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runCMListRichPTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_CONFIG_CM_LIST_PTY_START") == "1" {
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
			time.Sleep(100 * time.Millisecond)
			return fakeReader{profiles: appconfig.CMProfileList{
				DefaultProfile: "personal",
				Profiles: []appconfig.CMProfile{
					{Name: "work", Model: "gpt-4.1-mini", BaseURL: "https://work.example/v1"},
					{Name: "personal", Model: "deepseek-chat", BaseURL: "https://personal.example/v1"},
				},
			}}, nil
		},
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}
}

func runCMListPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
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

	var output lockedCMListPTYBuffer
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

func assertCMListRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	enter := strings.Index(output, "\x1b[?1049h")
	leave := strings.LastIndex(output, "\x1b[?1049l")
	if strings.Count(output, "\x1b[?1049h") != 1 || strings.Count(output, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}

	live := cmListPTYText(output[enter:leave])
	expected := []string{
		"YCY / config cm list",
		"scope commit message configuration",
		"Load CM profiles",
		"Loading CM profiles",
		"DONE",
		"SUCCEEDED",
		"Default profile: personal",
	}
	if wide {
		expected = append(expected, "profile inventory", "Loaded 2 CM profiles")
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
	if wide && !strings.Contains(live, "✓ DONE") {
		t.Fatalf("Rich PTY wide phase row omitted completed marker: %q", output)
	}

	postLive := output[leave:]
	resultStart := strings.Index(postLive, "YCY / config cm list")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result did not start after the Transcript: %q", output)
	}
	transcript := cmListPTYText(postLive[:resultStart])
	result := cmListPTYText(postLive[resultStart:])
	for _, needle := range []string{
		"Load CM profiles (completed): Loaded 2 CM profiles",
		"succeeded: Loaded 2 CM profiles Default profile: personal",
	} {
		if !strings.Contains(transcript, needle) {
			t.Fatalf("Rich PTY Transcript omitted %q: %q", needle, output)
		}
	}
	for _, forbidden := range []string{"work", "gpt-4.1-mini", "deepseek-chat", "https://work.example", "https://personal.example", "sk-secret"} {
		if strings.Contains(transcript, forbidden) {
			t.Fatalf("Rich PTY Transcript leaked %q: %q", forbidden, output)
		}
	}
	for _, needle := range []string{"Commit message profiles", "work", "personal", "gpt-4.1-mini", "deepseek-chat", "https://work.example/v1", "https://personal.example/v1"} {
		if !strings.Contains(result, needle) {
			t.Fatalf("Rich PTY durable result omitted %q: %q", needle, output)
		}
	}
	if wide {
		for _, needle := range []string{"DEFAULT", "PROFILE", "MODEL", "BASE URL"} {
			if !strings.Contains(result, needle) {
				t.Fatalf("Rich PTY wide result table omitted %q: %q", needle, output)
			}
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

func TestRunCMListStreamsPreservePlainAutomationAndFailureBoundaries(t *testing.T) {
	resultReader := func() (Reader, error) {
		return fakeReader{profiles: appconfig.CMProfileList{
			DefaultProfile: "work",
			Profiles:       []appconfig.CMProfile{{Name: "work", Model: "model", BaseURL: "https://example.test/v1"}},
		}}, nil
	}
	for _, testCase := range []struct {
		name        string
		capability  terminalexperience.InteractionMode
		wantOutput  string
		wantDiag    string
		wantControl bool
	}{
		{name: "plain", capability: terminalexperience.PlainInteractive, wantOutput: "Commit message profiles\nPROFILE  MODEL  BASE URL\n* work model https://example.test/v1\n", wantDiag: "Loading CM profiles...\n"},
		{name: "automation", capability: terminalexperience.Automation, wantOutput: "Commit message profiles\nPROFILE  MODEL  BASE URL\n* work model https://example.test/v1\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: testCase.capability},
				Output:       &output,
				Diagnostics:  &diagnostics,
			})
			err := runList(&Options{Context: context.Background(), Store: resultReader, Terminal: experience})
			if err != nil {
				t.Fatalf("runList() error = %v", err)
			}
			if output.String() != testCase.wantOutput || diagnostics.String() != testCase.wantDiag {
				t.Fatalf("streams = output %q, diagnostics %q", output.String(), diagnostics.String())
			}
			if testCase.wantControl && terminaltest.ContainsTerminalControl(output.Bytes()) {
				t.Fatalf("output contains terminal control: %q", output.String())
			}
			if terminaltest.ContainsTerminalControl(output.Bytes()) || terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
				t.Fatalf("non-rich streams contain terminal control: output=%q diagnostics=%q", output.String(), diagnostics.String())
			}
		})
	}

	readFailure := errors.New("configuration read failed")
	var output, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       &output,
		Diagnostics:  &diagnostics,
	})
	if err := runList(&Options{Context: context.Background(), Store: func() (Reader, error) {
		return fakeReader{err: readFailure}, nil
	}, Terminal: experience}); !errors.Is(err, readFailure) {
		t.Fatalf("runList() failure = %v, want %v", err, readFailure)
	}
	if output.Len() != 0 || diagnostics.Len() != 0 {
		t.Fatalf("failed Automation streams = output %q, diagnostics %q", output.String(), diagnostics.String())
	}
}

func cmListPTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

func cmListEnvironmentWith(overrides map[string]string) []string {
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

type lockedCMListPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedCMListPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedCMListPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
