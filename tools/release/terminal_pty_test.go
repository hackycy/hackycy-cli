package main

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

func TestReleaseCLIUsesNativeTerminalCapabilities(t *testing.T) {
	const helperEnvironment = "YCY_RELEASE_CLI_TERMINAL_HELPER"
	if scenario := os.Getenv(helperEnvironment); scenario != "" {
		runReleaseCLITerminalHelper(t, scenario)
		return
	}

	root := writeReleaseVersion(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "init", "--initial-branch=main", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize dirty release fixture: %v\n%s", err, output)
	}

	for _, test := range []struct {
		name        string
		environment []string
		rich        bool
		automation  bool
	}{
		{name: "ordinary", environment: []string{"TERM=xterm-256color"}, rich: true},
		{name: "missing-term", rich: true},
		{name: "unknown-term", environment: []string{"TERM=custom-terminal"}, rich: true},
		{name: "no-color", environment: []string{"TERM=xterm-256color", "NO_COLOR=0"}, rich: true},
		{name: "stdout-redirected", environment: []string{"TERM=xterm-256color"}, rich: true},
		{name: "dumb", environment: []string{"TERM=dumb"}},
		{name: "ci", environment: []string{"TERM=xterm-256color", "CI=1"}, automation: true},
		{name: "stdin-redirected", environment: []string{"TERM=xterm-256color"}, automation: true},
		{name: "stderr-redirected", environment: []string{"TERM=xterm-256color"}, automation: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReleaseCLIUsesNativeTerminalCapabilities$")
			command.Dir = root
			command.Env = append(releasePTYEnvironment(), test.environment...)
			command.Env = append(command.Env, helperEnvironment+"="+test.name)
			process, err := terminaltest.StartPTY(command)
			if errors.Is(err, terminaltest.ErrPTYUnsupported) {
				t.Skip(err)
			}
			if err != nil {
				t.Fatalf("start release CLI PTY helper: %v", err)
			}
			defer process.Close()
			output := newReleasePTYBuffer("Release blocked")
			readDone := make(chan struct{})
			go func() {
				_, _ = io.Copy(output, process.Terminal())
				close(readDone)
			}()
			waitErr := process.Wait()
			_ = process.Close()
			select {
			case <-readDone:
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out reading release CLI PTY output: %q", output.String())
			}
			text := output.String()
			if waitErr != nil {
				t.Fatalf("release CLI PTY helper: %v\n%s", waitErr, text)
			}
			if test.automation {
				if !strings.Contains(text, "release requires an interactive terminal") || strings.Contains(text, "Inspect repository") {
					t.Fatalf("automation did not stop before preflight: %q", text)
				}
			} else if !strings.Contains(text, "Release blocked") || !strings.Contains(text, versionFile) {
				t.Fatalf("release did not stop at the dirty fixture: %q", text)
			}
			if test.rich {
				entry, exit := releaseAlternateScreenEntry(text), releaseAlternateScreenExit(text)
				if entry < 0 || exit <= entry || exit > strings.Index(text, "Release blocked") {
					t.Fatalf("Rich screen did not open and restore before the error: %q", text)
				}
			} else if terminaltest.ContainsTerminalControl([]byte(text)) {
				t.Fatalf("Plain/Automation used terminal controls: %q", text)
			}
			if test.name == "no-color" && terminaltest.StyleSequences(text) != "" {
				t.Fatalf("NO_COLOR used styling: %q", text)
			}
		})
	}
}

func runReleaseCLITerminalHelper(t *testing.T, scenario string) {
	t.Helper()
	input, output, diagnostics := os.Stdin, os.Stdout, os.Stderr
	var redirected *os.File
	if strings.HasSuffix(scenario, "-redirected") {
		var err error
		redirected, err = os.Create(filepath.Join(t.TempDir(), "stream"))
		if err != nil {
			t.Fatal(err)
		}
		defer redirected.Close()
		switch scenario {
		case "stdin-redirected":
			input = redirected
		case "stdout-redirected":
			output = redirected
		case "stderr-redirected":
			diagnostics = redirected
		}
	}
	if code := runCLI(context.Background(), nil, input, output, diagnostics); code != 1 {
		t.Fatalf("release exit code = %d, want 1", code)
	}
	if redirected != nil {
		text, err := os.ReadFile(redirected.Name())
		if err != nil {
			t.Fatal(err)
		}
		if scenario == "stderr-redirected" {
			if !strings.Contains(string(text), "release requires an interactive terminal") {
				t.Fatalf("redirected diagnostics = %q", text)
			}
			fmt.Fprintln(os.Stdout, string(text))
		} else if len(text) != 0 {
			t.Fatalf("redirected stream contains UI output: %q", text)
		}
	}
}
