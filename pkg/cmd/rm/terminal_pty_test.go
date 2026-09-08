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
	"sync"
	"testing"
	"time"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunRMExplicitRichPTYRestoresScreenAndRedactsPaths(t *testing.T) {
	const helperEnvironment = "YCY_RM_EXPLICIT_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRMExplicitRichPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name  string
		extra string
		color bool
	}{
		{name: "color", color: true},
		{name: "no color", extra: "NO_COLOR=1", color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunRMExplicitRichPTYRestoresScreenAndRedactsPaths$")
			command.Env = append(rmPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
			if testCase.extra != "" {
				command.Env = append(command.Env, testCase.extra)
			}
			output := runRMPTYProcess(t, command, []rmPTYStep{{needle: "Delete 1 item?", input: "y\r"}})
			assertRMExplicitRichPTYOutput(t, output, testCase.color)
		})
	}
}

func runRMExplicitRichPTYHelper(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	name := "unsafe\nname.txt"
	target := filepath.Join(root, name)
	if err := os.WriteFile(target, []byte("contents"), 0o600); err != nil {
		t.Fatalf("write explicit target: %v", err)
	}
	experience := newRMRichPTYExperience()
	err := runRM(&Options{
		Context: context.Background(),
		Paths:   []string{name},
		WorkingDirectory: func() (string, error) {
			return root, nil
		},
		Terminal: experience,
		Remover:  osRMRemover{},
	})
	if err != nil {
		t.Fatalf("runRM() error = %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit target = %v, want missing", err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "RM_EXPLICIT_WRITE_OK")
}

func assertRMExplicitRichPTYOutput(t *testing.T, output string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	for _, expected := range []string{
		"YCY / rm",
		"Remove selected files or clean project artifacts",
		"Resolve explicit targets",
		"Targets: unsafe\\nname.txt",
		"Recursive deletion removes all contents.",
		"Delete 1 item?",
		"Delete selected paths",
		"Deleted 1 item",
		"RM_EXPLICIT_WRITE_OK",
		"Done!",
	} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("Rich PTY output missing %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "unsafe\nname.txt") {
		t.Fatalf("Rich PTY output leaked a raw newline path: %q", output)
	}
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	transcript := visible[leave:]
	ordered := []string{
		"ANSWERS",
		"Deletion confirmation: yes",
		"WORK",
		"Resolve explicit targets (completed)",
		"Targets: unsafe\\nname.txt Recursive deletion removes all contents.",
		"Delete selected paths (completed)",
		"Deleted 1 item",
		"OUTCOME  succeeded",
		"Done!",
	}
	last := 0
	for _, expected := range ordered {
		next := strings.Index(transcript[last:], expected)
		if next < 0 {
			t.Fatalf("Rich PTY transcript missing ordered event %q: %q", expected, output)
		}
		last += next + len(expected)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func TestRunRMSmartRichPTYRestoresScreenAndProjectsTranscript(t *testing.T) {
	const helperEnvironment = "YCY_RM_SMART_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRMSmartRichPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name  string
		extra string
		color bool
	}{
		{name: "color", color: true},
		{name: "no color", extra: "NO_COLOR=1", color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunRMSmartRichPTYRestoresScreenAndProjectsTranscript$")
			command.Env = append(rmPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
			if testCase.extra != "" {
				command.Env = append(command.Env, testCase.extra)
			}
			output := runRMPTYProcess(t, command, []rmPTYStep{
				{needle: "Select a clean action", input: "\r"},
				{needle: "Select items to delete", input: "\r"},
			})
			assertRMSmartRichPTYOutput(t, output, testCase.color)
		})
	}
}

func TestRunRMExplicitRichPTYCancellationRestoresScreenWithoutMutation(t *testing.T) {
	const helperEnvironment = "YCY_RM_CANCEL_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runRMExplicitCancellationRichPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name  string
		extra string
		color bool
	}{
		{name: "color", color: true},
		{name: "no color", extra: "NO_COLOR=1", color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunRMExplicitRichPTYCancellationRestoresScreenWithoutMutation$")
			command.Env = append(rmPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
			if testCase.extra != "" {
				command.Env = append(command.Env, testCase.extra)
			}
			output := runRMPTYProcess(t, command, []rmPTYStep{{needle: "Delete 1 item?", input: "\x1b"}})
			assertRMCancellationRichPTYOutput(t, output, testCase.color)
		})
	}
}

func runRMExplicitCancellationRichPTYHelper(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "cancelled.txt")
	if err := os.WriteFile(target, []byte("contents"), 0o600); err != nil {
		t.Fatalf("write cancellation target: %v", err)
	}
	experience := newRMRichPTYExperience()
	err := runRM(&Options{
		Context: context.Background(),
		Paths:   []string{"cancelled.txt"},
		WorkingDirectory: func() (string, error) {
			return root, nil
		},
		Terminal: experience,
		Remover:  osRMRemover{},
	})
	if err != nil {
		t.Fatalf("runRM() error = %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("cancelled target = %v, want retained", err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "RM_CANCEL_NO_WRITE_OK")
}

func assertRMCancellationRichPTYOutput(t *testing.T, output string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	for _, expected := range []string{
		"YCY / rm",
		"Delete 1 item?",
		"Deletion confirmation: cancelled",
		"cancelled",
		"Cancelled.",
		"RM_CANCEL_NO_WRITE_OK",
	} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("Rich PTY cancellation output missing %q: %q", expected, output)
		}
	}
	if strings.Contains(visible, "Delete selected paths") || strings.Contains(visible, "Done!") {
		t.Fatalf("Rich PTY cancellation entered mutation/result path: %q", output)
	}
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY cancellation did not restore the primary screen: %q", output)
	}
	if strings.LastIndex(visible, "Deletion confirmation: cancelled") < leave || strings.LastIndex(visible, "Cancelled.") < leave {
		t.Fatalf("Rich PTY cancellation transcript was not replayed after screen restore: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func runRMSmartRichPTYHelper(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "dist")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("create smart target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "bundle.js"), []byte("contents"), 0o600); err != nil {
		t.Fatalf("write smart target: %v", err)
	}
	experience := newRMRichPTYExperience()
	err := runRM(&Options{
		Context: context.Background(),
		WorkingDirectory: func() (string, error) {
			return root, nil
		},
		Terminal: experience,
		Remover:  osRMRemover{},
	})
	if err != nil {
		t.Fatalf("runRM() error = %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("smart target = %v, want missing", err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "RM_SMART_WRITE_OK")
}

func assertRMSmartRichPTYOutput(t *testing.T, output string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	for _, expected := range []string{
		"YCY / rm",
		"Remove selected files or clean project artifacts",
		"Select a clean action",
		"Node project - delete ./dist",
		"Scan cleanup targets",
		"Select items to delete",
		"Delete selected paths",
		"Deleted 1 item",
		"RM_SMART_WRITE_OK",
		"Done!",
	} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("Rich PTY output missing %q: %q", expected, output)
		}
	}
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	transcript := visible[leave:]
	ordered := []string{
		"ANSWERS",
		"Cleanup action: Node project - delete ./dist",
		"Selected targets: dist",
		"WORK",
		"Scan cleanup targets (completed)",
		"Delete selected paths (completed)",
		"Deleted 1 item",
		"OUTCOME  succeeded",
		"Done!",
	}
	last := 0
	for _, expected := range ordered {
		next := strings.Index(transcript[last:], expected)
		if next < 0 {
			t.Fatalf("Rich PTY transcript missing ordered event %q: %q", expected, output)
		}
		last += next + len(expected)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func TestRMExplicitRiskWarningsAndFailureCategories(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "workspace", "project")
	warnings := rmExplicitRiskWarnings(root, []string{root, filepath.Join(root, "child"), filepath.Join(string(filepath.Separator), "outside", "target"), root})
	if !containsString(warnings, "current directory or a parent scope") || !containsString(warnings, "outside the current directory") || !containsString(warnings, "duplicate target") {
		t.Fatalf("risk warnings = %#v", warnings)
	}
	if got := rmPathSummary(root, []string{filepath.Join(root, "unsafe\nname")}); got != "unsafe\\nname" {
		t.Fatalf("safe path summary = %q", got)
	}
	if got := rmFailureCategories([]error{errors.New("permission denied"), errors.New("no such file or directory"), errors.New("bad path"), errors.New("unexpected filesystem failure"), errors.New("permission denied")}); !sameStrings(got, []string{"permission", "not-found", "path", "filesystem"}) {
		t.Fatalf("failure categories = %#v", got)
	}

	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	if err := presentRMDeletion(terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive}, run, root, deletionResult{
		succeeded: 1,
		failures:  []error{errors.New("permission denied"), errors.New("no such file or directory")},
	}); err != nil {
		t.Fatalf("presentRMDeletion() error = %v", err)
	}
	operations := experience.Run.Operations()
	if len(operations) != 1 || operations[0].Kind != terminaltest.MilestoneOperation {
		t.Fatalf("failure presentation operations = %#v", operations)
	}
	document := operations[0].Value.(terminalexperience.PresentationDocument)
	presentation := terminalexperience.RenderPlain(document)
	for _, expected := range []string{"Deleted 1 item", "skipped (permission)", "skipped (not-found)"} {
		if !strings.Contains(presentation, expected) {
			t.Fatalf("failure presentation = %q, missing %q", presentation, expected)
		}
	}
	if strings.Contains(presentation, "permission denied") || strings.Contains(presentation, "no such file") {
		t.Fatalf("failure presentation leaked raw error: %q", presentation)
	}
}

func TestRMRichPhaseSinkPublishesActiveAndCompletedStates(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	sink := newRMPhaseSink(run, terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive})
	if err := sink.begin("scan", "Scan cleanup targets", "Scanning cleanup targets"); err != nil {
		t.Fatalf("begin() error = %v", err)
	}
	if err := sink.end(terminalexperience.PhaseCompleted, "Found 1 target"); err != nil {
		t.Fatalf("end() error = %v", err)
	}
	operations := experience.Run.Operations()
	if len(operations) != 1 || operations[0].Kind != terminaltest.TrackOperation {
		t.Fatalf("track operations = %#v", operations)
	}
	tracked := operations[0].Value.(terminalexperience.TrackedOperation)
	if len(tracked.Phases) != 1 || tracked.Phases[0].ID != "scan" || tracked.Phases[0].Name != "Scan cleanup targets" {
		t.Fatalf("phase catalog = %#v", tracked.Phases)
	}
	updates := make([]terminalexperience.OperationPhase, 0, 2)
	for update := range tracked.Updates {
		updates = append(updates, update)
	}
	if len(updates) != 2 || updates[0].State != terminalexperience.PhaseActive || updates[0].ID != "scan" || updates[1].State != terminalexperience.PhaseCompleted || updates[1].Detail != "Found 1 target" {
		t.Fatalf("phase updates = %#v", updates)
	}
}

func TestRMContextCancellationBeforePlanningPreservesErrorAndSkipsMutation(t *testing.T) {
	root := t.TempDir()
	target := writeStandaloneRMFile(t, root, "target.txt")
	ctx, cancel := context.WithCancel(context.Background())
	workingDirectory := func() (string, error) {
		cancel()
		return root, nil
	}
	stdout, diagnostics := &strings.Builder{}, &strings.Builder{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  diagnostics,
	})
	err := runRM(&Options{Context: ctx, Paths: []string{"target.txt"}, WorkingDirectory: workingDirectory, Terminal: experience, Remover: osRMRemover{}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run error = %v", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("cancelled run changed target: %v", statErr)
	}
	if strings.Contains(stdout.String()+diagnostics.String(), "Done!") {
		t.Fatalf("cancelled run emitted success: (%q, %q)", stdout.String(), diagnostics.String())
	}
}

type rmPTYStep struct {
	needle string
	input  string
}

func newRMRichPTYExperience() *terminalexperience.Runtime {
	return terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
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
}

func runRMPTYProcess(t *testing.T, command *exec.Cmd, steps []rmPTYStep) string {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()
	var output rmPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	for _, step := range steps {
		waitForRMPTYText(t, &output, step.needle)
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

func rmPTYEnvironment() []string {
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

type rmPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *rmPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *rmPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

func waitForRMPTYText(t *testing.T, output *rmPTYBuffer, needle string) {
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

func containsString(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
