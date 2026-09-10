package pulse

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	"golang.org/x/term"
)

func TestRunPulseRichPTYRestoresPrimaryScreenAndReplaysFourPhaseTranscript(t *testing.T) {
	const helperEnvironment = "YCY_GIT_PULSE_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runPulseRichPTYHelper(t)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunPulseRichPTYRestoresPrimaryScreenAndReplaysFourPhaseTranscript$")
			command.Env = pulsePTYEnvironmentWith(map[string]string{
				"NO_COLOR":                map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                    "xterm-256color",
				helperEnvironment:         "1",
				"YCY_GIT_PULSE_PTY_START": "1",
			})
			output := runPulsePTYProcess(t, command, testCase.width, testCase.height, "x\n")
			assertPulseRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func TestRunPulseRichPTYAlternatesOneWorkCatalogWithDeclaredForms(t *testing.T) {
	const helperEnvironment = "YCY_GIT_PULSE_RICH_FORMS_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runPulseRichFormsPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name          string
		width, height uint16
		color         bool
	}{
		{name: "wide color", width: 120, height: 40, color: true},
		{name: "compact no color", width: 40, height: 15, color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunPulseRichPTYAlternatesOneWorkCatalogWithDeclaredForms$")
			command.Env = pulsePTYEnvironmentWith(map[string]string{
				"NO_COLOR":                map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                    "xterm-256color",
				helperEnvironment:         "1",
				"YCY_GIT_PULSE_PTY_START": "1",
			})
			output := runPulseFormsPTYProcess(t, command, testCase.width, testCase.height)
			assertPulseRichFormsPTYOutput(t, output, testCase.color)
		})
	}
}

func runPulseRichPTYHelper(t *testing.T) {
	t.Helper()
	workspace := t.TempDir()
	initializeStandalonePulseRepository(t, workspace, "Ada", "ada@example.test", "safe commit")
	if os.Getenv("YCY_GIT_PULSE_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	width := 80
	if value, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && value > 0 {
		width = value
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
	err := runPulse(&Options{
		Context:          context.Background(),
		Directory:        workspace,
		Days:             pulseInt(1),
		WorkingDirectory: func() (string, error) { return workspace, nil },
		Terminal:         experience,
		Git:              &gitprocess.Runner{},
		Now:              time.Now,
		Width:            width,
	})
	if err != nil {
		t.Fatalf("runPulse() error = %v", err)
	}
}

func runPulseRichFormsPTYHelper(t *testing.T) {
	t.Helper()
	workspace := t.TempDir()
	initializeStandalonePulseRepository(t, filepath.Join(workspace, "alpha"), "Ada", "ada@example.test", "alpha commit")
	initializeStandalonePulseRepository(t, filepath.Join(workspace, "beta"), "Ben", "ben@example.test", "beta commit")
	if os.Getenv("YCY_GIT_PULSE_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	width := 80
	if value, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && value > 0 {
		width = value
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
	err := runPulse(&Options{
		Context:          context.Background(),
		Directory:        workspace,
		WorkingDirectory: func() (string, error) { return workspace, nil },
		Terminal:         experience,
		Git:              &gitprocess.Runner{},
		Now:              time.Now,
		Width:            width,
	})
	if err != nil {
		t.Fatalf("runPulse() error = %v", err)
	}
}

func runPulsePTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, input string) string {
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
	var output lockedPulsePTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte(input)); err != nil {
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

func runPulseFormsPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
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
	var output lockedPulsePTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("x\n")); err != nil {
		t.Fatalf("release PTY helper after sizing: %v", err)
	}
	waitForPulsePTYText(t, &output, "Select date range:")
	if _, err := process.Terminal().Write([]byte("\r")); err != nil {
		t.Fatalf("submit date selection: %v", err)
	}
	waitForPulsePTYText(t, &output, "multiple selection")
	if _, err := process.Terminal().Write([]byte("\r")); err != nil {
		t.Fatalf("submit author selection: %v", err)
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

func waitForPulsePTYText(t *testing.T, output *lockedPulsePTYBuffer, expected string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if strings.Contains(pulsePTYText(output.String()), expected) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %q in PTY output: %q", expected, output.String())
		case <-tick.C:
		}
	}
}

func assertPulseRichFormsPTYOutput(t *testing.T, output string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter {
		t.Fatalf("Rich PTY did not restore the primary screen: %q", output)
	}
	live := pulsePTYText(visible[enter:leave])
	for _, expected := range []string{"Prepare workspace", "Scan repositories", "Select date range:", "Fetch commits", "Filter by authors", "Build commit tree"} {
		if !strings.Contains(live, expected) {
			t.Fatalf("Rich PTY journey omitted %q: %q", expected, output)
		}
	}
	postLive := visible[leave:]
	resultStart := strings.LastIndex(postLive, "YCY / git pulse")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result did not follow the Transcript: %q", output)
	}
	transcript := pulsePTYText(postLive[:resultStart])
	result := pulsePTYText(postLive[resultStart:])
	for _, expected := range []string{"Prepare workspace (completed)", "Scan repositories (completed)", "Date range: Today", "Fetch commits (completed)", "Author filter: Ada, Ben", "Build commit tree (completed)", "succeeded: Found 2 commits in 2 repositories"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Rich PTY Transcript omitted %q: %q", expected, output)
		}
	}
	for _, expected := range []string{"alpha commit", "beta commit"} {
		if !strings.Contains(result, expected) || strings.Contains(transcript, expected) {
			t.Fatalf("report/transcript separation for %q failed: %q", expected, output)
		}
	}
	if color && !strings.Contains(output, "\x1b[38") {
		t.Fatalf("colored Rich PTY omitted B styling: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func assertPulseRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	live := pulsePTYText(visible[enter:leave])
	for _, expected := range []string{"YCY / git pulse", "Prepare workspace", "Scan repositories", "Fetch commits", "Build commit tree", "STATE", "PHASE", "DETAIL"} {
		if !strings.Contains(live, expected) {
			t.Fatalf("Rich PTY live Console missing %q: %q", expected, output)
		}
	}
	if wide && !strings.Contains(live, "workspace commit activity") {
		t.Fatalf("wide Rich PTY omitted complete target context: %q", output)
	}
	if !wide && !strings.Contains(live, "workspace") {
		t.Fatalf("compact Rich PTY omitted bounded target context: %q", output)
	}
	if strings.Contains(live, "FLOW") || strings.Contains(live, "[done]") || strings.Contains(live, "[active]") {
		t.Fatalf("Rich PTY live Console retained a non-B hierarchy: %q", output)
	}
	postLive := visible[leave:]
	resultStart := strings.LastIndex(postLive, "YCY / git pulse")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result did not start after the Transcript: %q", output)
	}
	transcript := pulsePTYText(postLive[:resultStart])
	result := pulsePTYText(postLive[resultStart:])
	for _, expected := range []string{"Prepare workspace (completed)", "Scan repositories (completed)", "Fetch commits (completed)", "Build commit tree (completed)", "succeeded"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Rich PTY Transcript omitted %q: %q", expected, output)
		}
	}
	if !strings.Contains(result, "safe commit") || !strings.Contains(result, "Workspace commit activity") {
		t.Fatalf("Rich PTY durable report omitted expected commit/result: %q", output)
	}
	if strings.Contains(transcript, "safe commit") {
		t.Fatalf("Rich PTY Transcript leaked report subject: %q", output)
	}
	if color && !strings.Contains(output, "\x1b[38") {
		t.Fatalf("color Rich PTY omitted B styling: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func pulsePTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

func pulsePTYEnvironmentWith(overrides map[string]string) []string {
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

type lockedPulsePTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedPulsePTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedPulsePTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
