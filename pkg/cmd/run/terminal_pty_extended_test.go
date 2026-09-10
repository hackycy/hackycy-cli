package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunRichPTYFourWaySelectionAndTranscript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("controlled Unix PTY fixture is unavailable on Windows")
	}
	const helperEnvironment = "YCY_RUN_SELECTION_PTY_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runSelectionPTYHelper(t)
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
			artifact := t.TempDir()
			command := exec.Command(os.Args[0], "-test.run=^TestRunRichPTYFourWaySelectionAndTranscript$")
			command.Env = runPTYEnvironment(map[string]string{
				"NO_COLOR":             map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                 "xterm-256color",
				helperEnvironment:      "1",
				"YCY_RUN_PTY_START":    "1",
				"YCY_RUN_PTY_ARTIFACT": artifact,
			})
			output := runSelectionPTYProcess(t, command, testCase.width, testCase.height, artifact)
			assertRunSelectionPTYOutput(t, output, testCase.color, testCase.width >= 70, artifact)
		})
	}
}

func runSelectionPTYHelper(t *testing.T) {
	t.Helper()
	artifact := os.Getenv("YCY_RUN_PTY_ARTIFACT")
	if artifact == "" {
		t.Fatal("missing Run PTY artifact directory")
	}
	if os.Getenv("YCY_RUN_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	project := filepath.Join(artifact, "project")
	writeRunPTYFile(t, project, "package.json", `{"scripts":{"check":"echo check","build":"echo build"}}`)
	writeRunPTYFile(t, project, "b"+"un"+".lock", "")

	color := os.Getenv("NO_COLOR") == ""
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: color},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: color},
		},
		Input: os.Stdin, Output: os.Stdout, Diagnostics: os.Stderr,
	})
	runner := runChildRunnerFunc(func(_ context.Context, request ChildRequest) (Result, error) {
		contents := fmt.Sprintf("executable=%s\narguments=%s\ndirectory=%s\n", request.Executable, strings.Join(request.Arguments, " "), request.Directory)
		writeRunPTYFile(t, artifact, "selection-request", contents)
		return Result{}, nil
	})
	err := runRun(&Options{
		Context:          context.Background(),
		Directory:        "",
		WorkingDirectory: func() (string, error) { return project, nil },
		Terminal:         experience,
		Reader:           osRunFileReader{},
		Exists:           osRunPathExists,
		Runner:           runner,
	})
	if err != nil {
		t.Fatalf("runRun() error = %v", err)
	}
}

func runSelectionPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, artifact string) string {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start Run selection PTY helper: %v", err)
	}
	defer process.Close()
	if err := process.Resize(width, height); err != nil {
		t.Fatalf("resize PTY to %dx%d: %v", width, height, err)
	}
	var output runPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("x\n")); err != nil {
		t.Fatalf("release Run selection PTY helper after sizing: %v", err)
	}
	waitForRunPTYText(t, &output, "Select a script to run")
	submitRunSelect(t, process, &output, "Select a package", "build", "")
	submitRunSelect(t, process, &output, "", "yarn", filepath.Join(artifact, "selection-request"))
	waitForRunPTYFile(t, filepath.Join(artifact, "selection-request"))
	if err := process.Wait(); err != nil {
		t.Fatalf("wait Run selection PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close Run selection PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading Run selection PTY output: %q", output.String())
	}
	return output.String()
}

func assertRunSelectionPTYOutput(t *testing.T, output string, color, wide bool, artifact string) {
	t.Helper()
	request, err := os.ReadFile(filepath.Join(artifact, "selection-request"))
	if err != nil {
		t.Fatalf("read selection request: %v", err)
	}
	requestText := string(request)
	if !strings.Contains(requestText, "executable=yarn") || !strings.Contains(requestText, "arguments=run build") {
		t.Fatalf("selection request = %q", requestText)
	}

	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Run selection PTY did not restore primary screen: %q", output)
	}
	live := strings.Join(strings.Fields(terminaltest.StripANSI(visible[enter:leave])), " ")
	expected := []string{"YCY / run", "STATE", "PHASE", "DETAIL", "Resolve project", "Resolve package manager"}
	if wide {
		expected = append(expected, "Prepare child command")
	} else {
		expected = append(expected, "Select a package")
	}
	for _, expected := range expected {
		if !strings.Contains(live, expected) {
			t.Fatalf("Run selection live Console missing %q: %q", expected, output)
		}
	}
	if wide && !strings.Contains(live, "Select and hand off a package script") {
		t.Fatalf("wide Run selection live Console omitted bounded target: %q", output)
	}
	transcript := visible[leave:]
	last := 0
	for _, expected := range []string{"Resolve project (completed)", "Resolve package manager (completed)", "Prepare child command (completed)", "Release terminal (completed)", "succeeded"} {
		next := strings.Index(transcript[last:], expected)
		if next < 0 {
			t.Fatalf("Run selection Transcript missing %q: %q", expected, output)
		}
		last += next + len(expected)
	}
	if strings.Contains(transcript, artifact) {
		t.Fatalf("Run selection Transcript leaked an absolute project path: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR Run selection output contains %q: %q", prefix, output)
			}
		}
	}
}

func TestRunRichPTYFourWayHandoffAndChildStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("controlled Unix PTY fixture is unavailable on Windows")
	}
	const helperEnvironment = "YCY_RUN_HANDOFF_PTY_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runHandoffPTYHelper(t)
		return
	}

	for _, outcome := range []string{"normal", "nonzero", "signal"} {
		t.Run(outcome, func(t *testing.T) {
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
					artifact := t.TempDir()
					command := exec.Command(os.Args[0], "-test.run=^TestRunRichPTYFourWayHandoffAndChildStreams$")
					command.Env = runPTYEnvironment(map[string]string{
						"NO_COLOR":                 map[bool]string{true: "", false: "1"}[testCase.color],
						"TERM":                     "xterm-256color",
						helperEnvironment:          "1",
						"YCY_RUN_PTY_START":        "1",
						"YCY_RUN_PTY_ARTIFACT":     artifact,
						"YCY_RUN_HANDOFF_PTY_MODE": outcome,
					})
					output := runHandoffPTYProcess(t, command, testCase.width, testCase.height, artifact)
					assertRunHandoffPTYOutput(t, output, testCase.color, testCase.width >= 70, artifact, outcome)
				})
			}
		})
	}
}

func runHandoffPTYHelper(t *testing.T) {
	t.Helper()
	artifact := os.Getenv("YCY_RUN_PTY_ARTIFACT")
	mode := os.Getenv("YCY_RUN_HANDOFF_PTY_MODE")
	if artifact == "" || mode == "" {
		t.Fatal("missing Run handoff PTY configuration")
	}
	if os.Getenv("YCY_RUN_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	project := filepath.Join(artifact, "project")
	writeRunPTYFile(t, project, "package.json", `{"scripts":{"check":"fixture"}}`)
	writeRunPTYFile(t, project, "b"+"un"+".lock", "")
	bin := filepath.Join(artifact, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("create Run child bin: %v", err)
	}
	child := filepath.Join(bin, string(PackageManagerExternal))
	childScript := "#!/bin/sh\n" +
		"printf 'CHILD_START\\n'\n" +
		"printf '%s\\n' \"$@\" > \"$RUN_PTY_ARGS\"\n" +
		"pwd > \"$RUN_PTY_CWD\"\n" +
		"touch \"$RUN_PTY_STARTED\"\n" +
		"IFS= read -r line\n" +
		"printf '%s\\n' \"$line\" > \"$RUN_PTY_STDIN\"\n" +
		"printf 'CHILD_STDOUT\\n'\n" +
		"printf 'CHILD_STDERR\\n' >&2\n" +
		"case \"$YCY_RUN_HANDOFF_PTY_MODE\" in\n" +
		"normal) exit 0 ;;\n" +
		"nonzero) exit 7 ;;\n" +
		"signal) kill -TERM $$ ;;\n" +
		"*) exit 99 ;;\n" +
		"esac\n"
	writeRunPTYFile(t, child, "", childScript)
	if err := os.Chmod(child, 0o700); err != nil {
		t.Fatalf("chmod Run child: %v", err)
	}

	setRunPTYEnvironment(t, "PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	setRunPTYEnvironment(t, "RUN_PTY_ARGS", filepath.Join(artifact, "args"))
	setRunPTYEnvironment(t, "RUN_PTY_CWD", filepath.Join(artifact, "cwd"))
	setRunPTYEnvironment(t, "RUN_PTY_STDIN", filepath.Join(artifact, "stdin"))
	setRunPTYEnvironment(t, "RUN_PTY_STARTED", filepath.Join(artifact, "started"))

	color := os.Getenv("NO_COLOR") == ""
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: color},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: color},
		},
		Input: os.Stdin, Output: os.Stdout, Diagnostics: os.Stderr,
	})
	err := runRun(&Options{
		Context:          context.Background(),
		WorkingDirectory: func() (string, error) { return project, nil },
		Terminal:         experience,
		Reader:           osRunFileReader{},
		Exists:           osRunPathExists,
		Runner:           newOSRunChildRunner(os.Stdin, os.Stdout, os.Stderr),
	})
	switch mode {
	case "normal":
		if err != nil {
			t.Fatalf("normal runRun() error = %v", err)
		}
	case "nonzero":
		var outcome *runChildOutcome
		if !errors.As(err, &outcome) || outcome.ExitCode() != 7 {
			t.Fatalf("nonzero runRun() error = %v", err)
		}
	case "signal":
		var outcome *runChildOutcome
		if !errors.As(err, &outcome) || outcome.ExitCode() != 143 {
			t.Fatalf("signal runRun() error = %v", err)
		}
	default:
		t.Fatalf("unknown Run handoff mode %q", mode)
	}
	writeRunPTYFile(t, artifact, "helper-complete", mode)
}

func runHandoffPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, artifact string) string {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start Run handoff PTY helper: %v", err)
	}
	defer process.Close()
	if err := process.Resize(width, height); err != nil {
		t.Fatalf("resize PTY to %dx%d: %v", width, height, err)
	}
	var output runPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("x\n")); err != nil {
		t.Fatalf("release Run handoff PTY helper after sizing: %v", err)
	}
	waitForRunPTYText(t, &output, "Select a script to run")
	submitRunSelect(t, process, &output, "Select a package", "", "")
	submitRunSelect(t, process, &output, "", "", filepath.Join(artifact, "started"))
	waitForRunPTYFile(t, filepath.Join(artifact, "started"))
	if _, err := process.Terminal().Write([]byte("inherited stdin payload\n")); err != nil {
		t.Fatalf("write inherited child stdin: %v", err)
	}
	completion := filepath.Join(artifact, "helper-complete")
	if !waitForRunPTYFileWithinPath(completion, 3*time.Second) {
		t.Fatalf("timed out waiting for Run handoff helper completion %q; output: %q", completion, output.String())
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("wait Run handoff PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close Run handoff PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading Run handoff PTY output: %q", output.String())
	}
	return output.String()
}

func assertRunHandoffPTYOutput(t *testing.T, output string, color, wide bool, artifact, mode string) {
	t.Helper()
	args, err := os.ReadFile(filepath.Join(artifact, "args"))
	if err != nil {
		t.Fatalf("read child argv: %v", err)
	}
	if string(args) != "run\ncheck\n" {
		t.Fatalf("child argv = %q", args)
	}
	cwd, err := os.ReadFile(filepath.Join(artifact, "cwd"))
	if err != nil {
		t.Fatalf("read child cwd: %v", err)
	}
	wantCWD := filepath.Join(artifact, "project") + "\n"
	if string(cwd) != wantCWD {
		t.Fatalf("child cwd = %q, want %q", cwd, wantCWD)
	}
	stdin, err := os.ReadFile(filepath.Join(artifact, "stdin"))
	if err != nil {
		t.Fatalf("read child stdin: %v", err)
	}
	if string(stdin) != "inherited stdin payload\n" {
		t.Fatalf("child stdin = %q", stdin)
	}

	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	childStart := strings.Index(visible, "CHILD_START")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || childStart < 0 || leave > childStart || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Run handoff did not release primary screen before child: %q", output)
	}
	live := strings.Join(strings.Fields(terminaltest.StripANSI(visible[enter:leave])), " ")
	expected := []string{"YCY / run", "STATE", "PHASE", "DETAIL", "Resolve project", "Resolve package manager"}
	if wide {
		expected = append(expected, "Prepare child command")
	} else {
		expected = append(expected, "Select a package")
	}
	for _, expected := range expected {
		if !strings.Contains(live, expected) {
			t.Fatalf("Run handoff live Console missing %q: %q", expected, output)
		}
	}
	transcript := visible[leave:childStart]
	for _, expected := range []string{"Resolve project (completed)", "Resolve package manager (completed)", "Prepare child command (completed)", "Release terminal (completed)", "succeeded"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Run handoff Transcript missing %q: %q", expected, output)
		}
	}
	childOutput := visible[childStart:]
	for _, expected := range []string{"CHILD_START", "CHILD_STDOUT", "CHILD_STDERR"} {
		if !strings.Contains(childOutput, expected) {
			t.Fatalf("Run handoff child output missing %q: %q", expected, output)
		}
	}
	for _, forbidden := range []string{"YCY / run", "STATE", "PHASE", "DETAIL", "Terminal released", "succeeded", "completed", "Operation cancelled", "\x1b["} {
		if strings.Contains(childOutput, forbidden) {
			t.Fatalf("Run handoff parent decoration after child startup %q: %q", forbidden, output)
		}
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR Run handoff output contains %q: %q", prefix, output)
			}
		}
	}
	if !strings.Contains(string(mustRunPTYFile(t, filepath.Join(artifact, "helper-complete"))), mode) {
		t.Fatalf("Run handoff helper did not record mode %q", mode)
	}
}

func submitRunSelect(t *testing.T, process *terminaltest.PTYProcess, output *runPTYBuffer, nextPrompt, filter, markerPath string) {
	t.Helper()
	if filter != "" {
		if _, err := process.Terminal().Write([]byte("/" + filter)); err != nil {
			t.Fatalf("write Run selection filter %q: %v", filter, err)
		}
	}
	if _, err := process.Terminal().Write([]byte("\r")); err != nil {
		t.Fatalf("submit Run selection: %v", err)
	}
	if nextPrompt == "" {
		if markerPath != "" {
			if waitForRunPTYFileWithinPath(markerPath, 700*time.Millisecond) {
				return
			}
		} else if waitForRunPTYTextWithin(output, "CHILD_START", 700*time.Millisecond) {
			return
		}
		if _, err := process.Terminal().Write([]byte("\r")); err != nil {
			t.Fatalf("submit second Run selection Enter: %v", err)
		}
		return
	}
	if waitForRunPTYTextWithin(output, nextPrompt, 700*time.Millisecond) {
		return
	}
	if _, err := process.Terminal().Write([]byte("\r")); err != nil {
		t.Fatalf("submit second Run selection Enter: %v", err)
	}
	waitForRunPTYText(t, output, nextPrompt)
}

func waitForRunPTYText(t *testing.T, output *runPTYBuffer, needle string) {
	t.Helper()
	if !waitForRunPTYTextWithin(output, needle, 10*time.Second) {
		t.Fatalf("timed out waiting for Run PTY text %q: %q", needle, output.String())
	}
}

func waitForRunPTYTextWithin(output *runPTYBuffer, needle string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), needle) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return strings.Contains(output.String(), needle)
}

func waitForRunPTYFile(t *testing.T, path string) {
	t.Helper()
	if !waitForRunPTYFileWithinPath(path, 10*time.Second) {
		t.Fatalf("timed out waiting for Run PTY file %q", path)
	}
}

func waitForRunPTYFileWithinPath(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err := os.Stat(path)
	return err == nil
}

func writeRunPTYFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	if name != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create Run PTY directory: %v", err)
		}
		directory = filepath.Join(directory, name)
	} else if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		t.Fatalf("create Run PTY file directory: %v", err)
	}
	if err := os.WriteFile(directory, []byte(contents), 0o600); err != nil {
		t.Fatalf("write Run PTY file %s: %v", directory, err)
	}
}

func setRunPTYEnvironment(t *testing.T, key, value string) {
	t.Helper()
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("set Run PTY environment %s: %v", key, err)
	}
}

func mustRunPTYFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Run PTY file %s: %v", path, err)
	}
	return contents
}

func runPTYEnvironment(overrides map[string]string) []string {
	ignored := map[string]struct{}{"CI": {}, "CLICOLOR": {}, "CLICOLOR_FORCE": {}, "COLORTERM": {}, "NO_COLOR": {}, "TERM": {}, "YCY_RUN_SELECTION_PTY_HELPER": {}, "YCY_RUN_HANDOFF_PTY_HELPER": {}, "YCY_RUN_PTY_START": {}, "YCY_RUN_PTY_ARTIFACT": {}, "YCY_RUN_HANDOFF_PTY_MODE": {}}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, skip := ignored[key]; skip {
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

type runPTYBuffer struct {
	mu    sync.Mutex
	value strings.Builder
}

func (buffer *runPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.value.Write(value)
}

func (buffer *runPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.value.String()
}
