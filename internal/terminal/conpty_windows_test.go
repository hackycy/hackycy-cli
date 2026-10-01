package terminal_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

const conPTYHelper = "YCY_CONPTY_INVOCATION_HELPER"
const conPTYReportPath = "YCY_CONPTY_REPORT"

type conPTYReport struct {
	Capabilities           terminal.Capabilities
	Before, AfterUI, After [3]uint32
	Answer                 string
	Cancelled              bool
	Width, Height          int
	Stdout, Stderr         string
}

func TestWindowsInvocationConPTY(t *testing.T) {
	if scenario := os.Getenv(conPTYHelper); scenario != "" {
		runConPTYInvocationHelper(t, scenario)
		return
	}
	for _, test := range []struct {
		name        string
		environment []string
		mode        terminal.InteractionMode
		color       bool
		interactive bool
	}{
		{name: "missing-term", mode: terminal.RichInteractive, color: true, interactive: true},
		{name: "unknown-term", environment: []string{"TERM=custom-terminal"}, mode: terminal.RichInteractive, color: true, interactive: true},
		{name: "no-color", environment: []string{"NO_COLOR=0"}, mode: terminal.RichInteractive, interactive: true},
		{name: "cancel", mode: terminal.RichInteractive, color: true, interactive: true},
		{name: "dumb", environment: []string{"TERM=dumb"}, mode: terminal.PlainInteractive},
		{name: "ci", environment: []string{"CI=1"}, mode: terminal.Automation, color: true},
		{name: "stdin-file", mode: terminal.Automation, color: true},
		{name: "stdout-file", mode: terminal.RichInteractive, color: true, interactive: true},
		{name: "stderr-file", mode: terminal.Automation, color: true},
		{name: "forced-files", environment: []string{"FORCE_COLOR=1"}, mode: terminal.Automation, color: true},
		{name: "input-backend-failure", mode: terminal.RichInteractive, color: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reportPath := filepath.Join(t.TempDir(), "report.json")
			command := exec.Command(os.Args[0], "-test.run=^TestWindowsInvocationConPTY$")
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				switch strings.ToUpper(key) {
				case "TERM", "COLORTERM", "NO_COLOR", "FORCE_COLOR", "CI", "CLICOLOR", "CLICOLOR_FORCE", "TTY_FORCE":
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env, test.environment...)
			command.Env = append(command.Env, conPTYHelper+"="+test.name, conPTYReportPath+"="+reportPath)
			process, err := terminaltest.StartConPTY(command, 100, 30)
			if err != nil {
				t.Fatalf("start native ConPTY: %v", err)
			}
			defer process.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if test.interactive {
				waitForConPTYText(t, ctx, process, "Enter answer")
				if err := process.Resize(40, 14); err != nil {
					t.Fatal(err)
				}
				if err := process.Resize(140, 42); err != nil {
					t.Fatal(err)
				}
				input := "alice\r"
				if test.name == "cancel" {
					input = "\x03"
				}
				if _, err := process.Input().WriteString(input); err != nil {
					t.Fatal(err)
				}
			}
			if err := process.Wait(ctx); err != nil {
				t.Fatalf("native child: %v\n%s", err, process.Output())
			}
			if err := process.Close(); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatalf("native report: %v\n%s", err, process.Output())
			}
			var report conPTYReport
			if err := json.Unmarshal(contents, &report); err != nil {
				t.Fatal(err)
			}
			if report.Capabilities.Interaction != test.mode {
				t.Fatalf("automatic mode = %#v", report.Capabilities)
			}
			if report.Before != report.After {
				t.Fatalf("console modes leaked: before=%x after=%x", report.Before, report.After)
			}
			if test.interactive {
				if report.Width != 140 || report.Height != 42 {
					t.Fatalf("native resize = %dx%d", report.Width, report.Height)
				}
				if report.AfterUI[0] != report.Before[0] {
					t.Fatalf("raw input survived UI closure: %x -> %x", report.Before[0], report.AfterUI[0])
				}
				if report.AfterUI[2]&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
					t.Fatal("VT ended before transcript/results/logging")
				}
				if test.name == "cancel" {
					if !report.Cancelled {
						t.Fatal("Ctrl+C did not cancel")
					}
				} else if report.Answer != "alice" {
					t.Fatalf("native input = %q", report.Answer)
				}
			}
			output := process.Output()
			if test.interactive && (!strings.Contains(output, "\x1b[?1049h") || !strings.Contains(output, "\x1b[?1049l")) {
				t.Fatalf("screen lifecycle missing: %q", output)
			}
			if test.interactive {
				afterUI := output[strings.LastIndex(output, "\x1b[?1049l"):]
				if !strings.Contains(terminaltest.StripANSI(afterUI), "TRANSCRIPT_MARKER") {
					t.Fatalf("semantic transcript was not replayed after screen restoration: %q", afterUI)
				}
			}
			if !test.interactive && strings.Contains(output, "\x1b[?1049h") {
				t.Fatal("noninteractive output started a full-screen UI")
			}
			text := output + report.Stdout + report.Stderr
			if !strings.Contains(terminaltest.StripANSI(text), "FINAL_RESULT") || !strings.Contains(terminaltest.StripANSI(text), "AFTER_UI_LOG") {
				t.Fatalf("missing final output: %q", text)
			}
			if !test.color && strings.Contains(terminaltest.StyleSequences(text), "38;") {
				t.Fatalf("NO_COLOR/dumb emitted color: %q", text)
			}
			if test.color && !strings.Contains(terminaltest.StyleSequences(text), "38;") {
				t.Fatalf("native text lost color: %q", text)
			}
			if test.name == "stdout-file" && (report.Capabilities.Stdout.Profile != terminal.NoColor || terminaltest.ContainsTerminalControl([]byte(report.Stdout))) {
				t.Fatalf("redirected stdout was styled: %q", report.Stdout)
			}
			if test.name == "stderr-file" && (report.Capabilities.Stderr.Profile != terminal.NoColor || terminaltest.ContainsTerminalControl([]byte(report.Stderr))) {
				t.Fatalf("redirected stderr was styled: %q", report.Stderr)
			}
		})
	}
}

func waitForConPTYText(t *testing.T, ctx context.Context, process *terminaltest.ConPTYProcess, text string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !strings.Contains(terminaltest.StripANSI(process.Output()), text) {
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %q: %v\n%s", text, ctx.Err(), process.Output())
		case <-ticker.C:
		}
	}
}

func nativeConsoleModes(t *testing.T) (modes [3]uint32) {
	t.Helper()
	for index, file := range []*os.File{os.Stdin, os.Stdout, os.Stderr} {
		if err := windows.GetConsoleMode(windows.Handle(file.Fd()), &modes[index]); err != nil {
			t.Fatal(err)
		}
	}
	return modes
}

func runConPTYInvocationHelper(t *testing.T, scenario string) {
	// Ensure the test proves invocation-owned VT preparation even when the
	// host initially enables it. stdout/stderr normally alias the same buffer.
	initial := nativeConsoleModes(t)
	for index, file := range []*os.File{os.Stdout, os.Stderr} {
		if err := windows.SetConsoleMode(windows.Handle(file.Fd()), initial[index+1] & ^uint32(windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)); err != nil {
			t.Fatal(err)
		}
	}
	report := conPTYReport{Before: nativeConsoleModes(t)}
	input, output, diagnostics := os.Stdin, os.Stdout, os.Stderr
	var outFile, errFile *os.File
	redirect := func(label string) *os.File {
		file, err := os.CreateTemp(filepath.Dir(os.Getenv(conPTYReportPath)), label)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { file.Close() })
		return file
	}
	switch scenario {
	case "input-backend-failure":
		// A native output handle is a TTY but cannot enter raw input mode.
		// This exercises an actual backend failure without injecting Rich caps.
		input = os.Stdout
	case "stdin-file":
		input = redirect("stdin")
	case "stdout-file":
		outFile = redirect("stdout")
		output = outFile
	case "stderr-file":
		errFile = redirect("stderr")
		diagnostics = errFile
	case "forced-files":
		input = redirect("stdin")
		outFile, errFile = redirect("stdout"), redirect("stderr")
		output, diagnostics = outFile, errFile
	}
	invocation, err := terminal.PrepareInvocation(input, output, diagnostics, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer invocation.Close()
	report.Capabilities = invocation.Capabilities
	runtime := terminal.NewExperience(terminal.ExperienceOptions{Capabilities: invocation.Capabilities, Environment: invocation.Environment, Input: input, Output: output, Diagnostics: diagnostics})
	run, err := runtime.OpenConsole(context.Background(), terminal.ConsoleDescriptor{Command: "YCY / conpty", FormCatalog: []terminal.ConsoleFormStep{{ID: "answer", Name: "Name"}}})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Capabilities.Interaction == terminal.RichInteractive && scenario != "input-backend-failure" {
		answer, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "Enter answer", ConsoleStepID: "answer"})
		if scenario == "cancel" {
			report.Cancelled = errors.Is(err, terminal.ErrInteractionCancelled)
		} else if err != nil {
			t.Fatal(err)
		}
		report.Answer = answer.Value
	}
	if err := run.Milestone(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleSuccess, Text: "TRANSCRIPT_MARKER"}}}); err != nil {
		t.Fatal(err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	report.AfterUI = nativeConsoleModes(t)
	report.Width, report.Height, err = term.GetSize(int(os.Stderr.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.Open(context.Background())
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleTitle, Text: "FINAL_RESULT"}}}
	if err := result.Finish(terminal.FinishRequest{Outcome: terminal.Succeeded}, &document); err != nil {
		t.Fatal(err)
	}
	logger := logging.NewRuntime(logging.Options{Writer: runtime.DiagnosticWriter(), Profile: invocation.Capabilities.Stderr.Profile})
	logger.Logger("conpty").Info("AFTER_UI_LOG", nil)
	if err := invocation.Close(); err != nil {
		t.Fatal(err)
	}
	report.After = nativeConsoleModes(t)
	read := func(file *os.File) string {
		if file == nil {
			return ""
		}
		if _, err := file.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		contents, err := os.ReadFile(file.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(contents)
	}
	report.Stdout, report.Stderr = read(outFile), read(errFile)
	contents, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv(conPTYReportPath), contents, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "NATIVE_RESTORED")
}
