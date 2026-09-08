package rm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunRMExplicitRichPTYFourWayMutationJourney(t *testing.T) {
	const helperEnvironment = "YCY_RM_EXTENDED_MUTATION_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRMExtendedMutationHelper(t)
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
			releasePath := filepath.Join(t.TempDir(), "release")
			command := exec.Command(os.Args[0], "-test.run=^TestRunRMExplicitRichPTYFourWayMutationJourney$")
			command.Env = rmExtendedPTYEnvironment(map[string]string{
				"NO_COLOR":                  map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                      "xterm-256color",
				helperEnvironment:           "1",
				"YCY_RM_EXTENDED_PTY_START": "1",
				"YCY_RM_EXTENDED_RELEASE":   releasePath,
			})
			output := runRMExtendedPTYProcess(t, command, testCase.width, testCase.height, "y\r", "RM_DELETE_ENTER", releasePath)
			assertRMExtendedMutationOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runRMExtendedMutationHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_RM_EXTENDED_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	releasePath := os.Getenv("YCY_RM_EXTENDED_RELEASE")
	if releasePath == "" {
		t.Fatal("missing rm release path")
	}
	root := t.TempDir()
	name := "unsafe\nname.txt"
	target := filepath.Join(root, name)
	if err := os.WriteFile(target, []byte("contents"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	experience := newRMRichPTYExperience()
	err := runRM(&Options{
		Context: context.Background(),
		Paths:   []string{name},
		WorkingDirectory: func() (string, error) {
			return root, nil
		},
		Terminal: experience,
		Remover: pathRemoverFunc(func(path string) error {
			if _, err := fmt.Fprintln(os.Stderr, "RM_DELETE_ENTER"); err != nil {
				return err
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(releasePath); err == nil {
					break
				}
				if time.Now().After(deadline) {
					return errors.New("rm PTY release timed out")
				}
				time.Sleep(5 * time.Millisecond)
			}
			return os.RemoveAll(path)
		}),
	})
	if err != nil {
		t.Fatalf("runRM() error = %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted target = %v, want missing", err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "RM_EXTENDED_MUTATION_OK")
}

func assertRMExtendedMutationOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("rm Rich PTY did not restore primary screen: %q", output)
	}
	live := rmExtendedPTYText(visible[enter:leave])
	for _, expected := range []string{"YCY / rm", "Resolve explicit targets", "Delete selected paths", "STATE", "PHASE", "DETAIL", "RM_DELETE_ENTER"} {
		if !strings.Contains(live, expected) {
			t.Fatalf("rm Rich PTY live Console missing %q: %q", expected, output)
		}
	}
	if wide {
		for _, expected := range []string{"Remove selected files or clean project artifacts", "explicit path removal", "destructive filesystem mutation"} {
			if !strings.Contains(live, expected) {
				t.Fatalf("wide rm Rich PTY omitted descriptor context %q: %q", expected, output)
			}
		}
	} else if !strings.Contains(live, "explicit") {
		t.Fatalf("compact rm Rich PTY omitted bounded route context: %q", output)
	}
	if !strings.Contains(visible, "Delete 1 item?") && !strings.Contains(live, "confirmation") {
		t.Fatalf("rm Rich PTY omitted confirmation context: %q", output)
	}
	if strings.Contains(output, "unsafe\nname.txt") {
		t.Fatalf("rm Rich PTY leaked raw newline path: %q", output)
	}
	for _, expected := range []string{"RM_EXTENDED_MUTATION_OK", "Deleted 1 item", "Done!"} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("rm Rich PTY output missing %q: %q", expected, output)
		}
	}
	transcript := visible[leave:]
	ordered := []string{
		"ANSWERS",
		"Deletion confirmation: yes",
		"WORK",
		"Resolve explicit targets (completed)",
		"Delete selected paths (completed)",
		"Deleted 1 item",
		"OUTCOME  succeeded",
		"Done!",
	}
	last := 0
	for _, expected := range ordered {
		next := strings.Index(transcript[last:], expected)
		if next < 0 {
			t.Fatalf("rm Rich PTY Transcript missing ordered event %q: %q", expected, output)
		}
		last += next + len(expected)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR rm Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func TestRunRMExplicitRichPTYFourWayCancellationJourney(t *testing.T) {
	const helperEnvironment = "YCY_RM_EXTENDED_CANCEL_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRMExtendedCancellationHelper(t)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunRMExplicitRichPTYFourWayCancellationJourney$")
			command.Env = rmExtendedPTYEnvironment(map[string]string{
				"NO_COLOR":                  map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                      "xterm-256color",
				helperEnvironment:           "1",
				"YCY_RM_EXTENDED_PTY_START": "1",
			})
			output := runRMExtendedPTYProcess(t, command, testCase.width, testCase.height, "\x1b", "RM_CANCEL_NO_WRITE_OK", "")
			assertRMExtendedCancellationOutput(t, output, testCase.color)
		})
	}
}

func runRMExtendedCancellationHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_RM_EXTENDED_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	root := t.TempDir()
	target := filepath.Join(root, "cancelled.txt")
	if err := os.WriteFile(target, []byte("contents"), 0o600); err != nil {
		t.Fatalf("write cancellation target: %v", err)
	}
	experience := newRMRichPTYExperience()
	err := runRM(&Options{
		Context: context.Background(),
		Paths:   []string{filepath.Base(target)},
		WorkingDirectory: func() (string, error) {
			return root, nil
		},
		Terminal: experience,
		Remover: pathRemoverFunc(func(string) error {
			return errors.New("cancellation must not call remover")
		}),
	})
	if err != nil {
		t.Fatalf("runRM() error = %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("cancelled target = %v, want retained", err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "RM_CANCEL_NO_WRITE_OK")
}

func assertRMExtendedCancellationOutput(t *testing.T, output string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	for _, expected := range []string{"YCY / rm", "Resolve explicit targets", "Deletion confirmation: cancelled", "Cancelled.", "RM_CANCEL_NO_WRITE_OK"} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("rm Rich PTY cancellation missing %q: %q", expected, output)
		}
	}
	if strings.Contains(visible, "Delete selected paths") || strings.Contains(visible, "RM_DELETE_ENTER") || strings.Contains(visible, "Done!") {
		t.Fatalf("rm Rich PTY cancellation entered mutation path: %q", output)
	}
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("rm Rich PTY cancellation did not restore primary screen: %q", output)
	}
	if strings.Index(visible[leave:], "Deletion confirmation: cancelled") < 0 || strings.Index(visible[leave:], "Cancelled.") < 0 {
		t.Fatalf("rm Rich PTY cancellation Transcript was not replayed: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR rm Rich PTY cancellation contains %q: %q", prefix, output)
			}
		}
	}
}

func runRMExtendedPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, input, marker, releasePath string) string {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start rm PTY helper: %v", err)
	}
	defer process.Close()
	if err := process.Resize(width, height); err != nil {
		t.Fatalf("resize PTY to %dx%d: %v", width, height, err)
	}
	var output rmPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("go\n")); err != nil {
		t.Fatalf("release PTY helper after sizing: %v", err)
	}
	waitForRMPTYText(t, &output, "confirmation")
	if _, err := process.Terminal().Write([]byte(input)); err != nil {
		t.Fatalf("write rm PTY input: %v", err)
	}
	waitForRMPTYText(t, &output, marker)
	if releasePath != "" {
		if err := os.WriteFile(releasePath, []byte("ok"), 0o600); err != nil {
			t.Fatalf("release rm writer: %v", err)
		}
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("wait rm PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close rm PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading rm PTY output: %q", output.String())
	}
	return output.String()
}

func rmExtendedPTYEnvironment(overrides map[string]string) []string {
	ignored := map[string]struct{}{
		"CI": {}, "CLICOLOR": {}, "CLICOLOR_FORCE": {}, "COLORTERM": {}, "NO_COLOR": {}, "TERM": {},
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, skip := ignored[key]; !skip {
				if _, replaced := overrides[key]; !replaced {
					environment = append(environment, entry)
				}
			}
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func rmExtendedPTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}
