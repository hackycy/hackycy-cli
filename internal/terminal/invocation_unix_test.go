//go:build !windows

package terminal_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	"golang.org/x/term"
)

func TestUnixInvocationAutomaticPTYCapabilities(t *testing.T) {
	const helper = "YCY_UNIX_INVOCATION_HELPER"
	if scenario := os.Getenv(helper); scenario != "" {
		before, err := term.GetState(int(os.Stdin.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		invocation, err := terminal.PrepareInvocation(os.Stdin, os.Stdout, os.Stderr, os.Environ())
		if err != nil {
			t.Fatal(err)
		}
		defer invocation.Close()
		caps := invocation.Capabilities
		wantMode, wantProfile := terminal.RichInteractive, terminal.ANSI16
		if scenario == "no-color" {
			wantProfile = terminal.NoColor
		}
		if scenario == "dumb" {
			wantMode, wantProfile = terminal.PlainInteractive, terminal.NoColor
		}
		if caps.Interaction != wantMode || caps.Stdout.Profile != wantProfile || caps.Stderr.Profile != wantProfile {
			t.Fatalf("automatic PTY caps = %#v, want mode %d profile %d", caps, wantMode, wantProfile)
		}
		runtime := terminal.NewExperience(terminal.ExperienceOptions{Capabilities: caps, Environment: invocation.Environment, Input: os.Stdin, Output: os.Stdout, Diagnostics: os.Stderr})
		run, err := runtime.OpenConsole(context.Background(), terminal.ConsoleDescriptor{Command: "YCY / Unix PTY"})
		if err != nil {
			t.Fatal(err)
		}
		if err := run.Milestone(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleSuccess, Text: "PTY_CHECKPOINT"}}}); err != nil {
			t.Fatal(err)
		}
		if err := run.Close(); err != nil {
			t.Fatal(err)
		}
		after, err := term.GetState(int(os.Stdin.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("Unix raw input was not restored")
		}
		result := runtime.Open(context.Background())
		if err := result.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleTitle, Text: "PTY_RESULT"}}}); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, "PTY_RESTORED")
		return
	}
	for _, scenario := range []string{"missing-term", "unknown-term", "no-color", "dumb"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUnixInvocationAutomaticPTYCapabilities$")
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				switch key {
				case "TERM", "COLORTERM", "WT_SESSION", "TMUX", "ConEmuANSI", "GOOGLE_CLOUD_SHELL", "NO_COLOR", "FORCE_COLOR", "CI", "CLICOLOR", "CLICOLOR_FORCE", "TTY_FORCE":
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env, helper+"="+scenario)
			switch scenario {
			case "unknown-term":
				command.Env = append(command.Env, "TERM=custom-terminal")
			case "no-color":
				command.Env = append(command.Env, "NO_COLOR=0")
			case "dumb":
				command.Env = append(command.Env, "TERM=dumb")
			}
			process, err := terminaltest.StartPTY(command)
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			var output bytes.Buffer
			done := make(chan struct{})
			go func() { _, _ = io.Copy(&output, process.Terminal()); close(done) }()
			waitErr := process.Wait()
			process.Close()
			<-done
			if waitErr != nil {
				t.Fatalf("PTY child: %v\n%s", waitErr, output.String())
			}
			text := output.String()
			if !strings.Contains(text, "PTY_RESTORED") || !strings.Contains(text, "PTY_RESULT") {
				t.Fatalf("PTY lost result/restore: %q", text)
			}
			if scenario == "dumb" {
				if terminaltest.ContainsTerminalControl(output.Bytes()) {
					t.Fatalf("dumb PTY used controls: %q", text)
				}
			} else if !strings.Contains(text, "\x1b[?1049h") || !strings.Contains(text, "\x1b[?1049l") {
				t.Fatalf("automatic PTY did not run UI: %q", text)
			}
			if (scenario == "no-color" || scenario == "dumb") && strings.Contains(terminaltest.StyleSequences(text), "38;") {
				t.Fatalf("monochrome PTY colored output: %q", text)
			}
		})
	}
}
