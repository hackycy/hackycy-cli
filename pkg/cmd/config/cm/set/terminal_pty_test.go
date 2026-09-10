package set

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunCMSetRichPTYRestoresPrimaryScreenAndRedactsValue(t *testing.T) {
	const helperEnvironment = "YCY_CONFIG_CM_SET_RICH_HELPER"
	if scenario := os.Getenv(helperEnvironment); scenario != "" {
		runCMSetRichPTYHelper(t, scenario)
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
			releaseDirectory := t.TempDir()
			releasePath := filepath.Join(releaseDirectory, "release")
			command := exec.Command(os.Args[0], "-test.run=^TestRunCMSetRichPTYRestoresPrimaryScreenAndRedactsValue$")
			command.Env = cmSetPTYEnvironment(map[string]string{
				"NO_COLOR":                    map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                        "xterm-256color",
				helperEnvironment:             "success",
				"YCY_CONFIG_CM_SET_PTY_START": "1",
				"YCY_CONFIG_CM_SET_RELEASE":   releasePath,
			})
			output := runCMSetPTYProcess(t, command, testCase.width, testCase.height, releasePath)
			assertCMSetRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func TestRunCMSetRichPTYFailureProjection(t *testing.T) {
	const helperEnvironment = "YCY_CONFIG_CM_SET_RICH_FAILURE_HELPER"
	if scenario := os.Getenv(helperEnvironment); scenario != "" {
		runCMSetRichPTYHelper(t, scenario)
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
			releaseDirectory := t.TempDir()
			releasePath := filepath.Join(releaseDirectory, "release")
			command := exec.Command(os.Args[0], "-test.run=^TestRunCMSetRichPTYFailureProjection$")
			command.Env = cmSetPTYEnvironment(map[string]string{
				"NO_COLOR":                    map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                        "xterm-256color",
				helperEnvironment:             "failure",
				"YCY_CONFIG_CM_SET_PTY_START": "1",
				"YCY_CONFIG_CM_SET_RELEASE":   releasePath,
			})
			output := runCMSetPTYProcess(t, command, testCase.width, testCase.height, releasePath)
			assertCMSetRichPTYFailureOutput(t, output, testCase.color)
		})
	}
}

func runCMSetRichPTYHelper(t *testing.T, scenario string) {
	t.Helper()
	if os.Getenv("YCY_CONFIG_CM_SET_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	releasePath := os.Getenv("YCY_CONFIG_CM_SET_RELEASE")
	if releasePath == "" {
		t.Fatal("missing CM set release path")
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
	err := runSet(&Options{
		Context:  context.Background(),
		Profile:  "work",
		Key:      "apiKey",
		Value:    "secret-api-key",
		Terminal: experience,
		Store: func() (SetWriter, error) {
			return setWriterFunc(func(_, _, _ string) error {
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, statErr := os.Stat(releasePath); statErr == nil {
						if scenario == "failure" {
							_, _ = fmt.Fprintln(os.Stderr, "CM_SET_WRITE_ATTEMPT")
							return errors.New("storage failed: secret-api-key")
						}
						_, _ = fmt.Fprintln(os.Stderr, "CM_SET_WRITE_OK")
						return nil
					}
					if time.Now().After(deadline) {
						return errors.New("CM set PTY release timed out")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}), nil
		},
	})
	switch scenario {
	case "success":
		if err != nil {
			t.Fatalf("runSet() error = %v", err)
		}
	case "failure":
		if err == nil || !strings.Contains(err.Error(), "storage failed") {
			t.Fatalf("runSet() error = %v, want storage failure", err)
		}
		_, _ = fmt.Fprintln(os.Stderr, "CM_SET_FAILURE_OK")
	default:
		t.Fatalf("unknown CM set Rich PTY scenario %q", scenario)
	}
}

func runCMSetPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, releasePath string) string {
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
	var output lockedCMSetPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("go\n")); err != nil {
		t.Fatalf("release PTY helper after sizing: %v", err)
	}
	waitForCMSetPTYText(t, &output, "Validating setting and saving")
	if err := os.WriteFile(releasePath, []byte("ok"), 0o600); err != nil {
		t.Fatalf("release CM set writer: %v", err)
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

func assertCMSetRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	live := cmSetPTYText(visible[enter:leave])
	expected := []string{"YCY / config cm set", "work", "apiKey", "STATE", "PHASE", "DETAIL", "Update CM profile", "Validating setting and saving", "CM_SET_WRITE_OK"}
	if wide {
		expected = append(expected, "commit message profile update")
	} else {
		expected = append(expected, "commit")
	}
	for _, expected := range expected {
		if !strings.Contains(live, expected) {
			t.Fatalf("Rich PTY live Console missing %q: %q", expected, output)
		}
	}
	if wide && !strings.Contains(live, "commit message configuration") {
		t.Fatalf("wide Rich PTY omitted configuration scope: %q", output)
	}
	if strings.Contains(output, "secret-api-key") {
		t.Fatalf("Rich PTY output leaked API key: %q", output)
	}
	postLive := visible[leave:]
	resultStart := strings.LastIndex(postLive, "Profile work updated")
	if resultStart < 0 {
		t.Fatalf("Rich PTY durable result missing: %q", output)
	}
	transcript := cmSetPTYText(postLive[:resultStart])
	result := cmSetPTYText(postLive[resultStart:])
	for _, expected := range []string{"Update CM profile (completed)", "API key: [redacted]", "succeeded"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Rich PTY Transcript missing %q: %q", expected, output)
		}
	}
	if !strings.Contains(result, "Profile work updated") {
		t.Fatalf("Rich PTY result missing profile update: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func assertCMSetRichPTYFailureOutput(t *testing.T, output string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY failure output did not restore the primary screen: %q", output)
	}
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if enter < 0 || leave < enter {
		t.Fatalf("Rich PTY failure screen bounds are invalid: %q", output)
	}
	live := cmSetPTYText(visible[enter:leave])
	for _, expected := range []string{"Update CM profile", "Validating setting and saving", "CM_SET_WRITE_ATTEMPT"} {
		if !strings.Contains(live, expected) {
			t.Fatalf("Rich PTY failure Live View missing %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "secret-api-key") || strings.Contains(output, "storage failed") {
		t.Fatalf("Rich PTY failure output leaked sensitive/raw error data: %q", output)
	}
	postLive := visible[leave:]
	transcript := cmSetPTYText(postLive)
	for _, expected := range []string{"Update CM profile (failed)", "Unable to update CM profile", "failed", "CM_SET_FAILURE_OK"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Rich PTY failure Transcript missing %q: %q", expected, output)
		}
	}
	if strings.Contains(transcript, "Profile work updated") {
		t.Fatalf("Rich PTY failure emitted a success result: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR Rich PTY failure output contains %q: %q", prefix, output)
			}
		}
	}
}

func cmSetPTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

func cmSetPTYEnvironment(overrides map[string]string) []string {
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

type lockedCMSetPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedCMSetPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedCMSetPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func waitForCMSetPTYText(t *testing.T, output *lockedCMSetPTYBuffer, needle string) {
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
