package terminal_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
)

func TestRichRuntimeWheelScrollAndCopyModeRestoreTerminal(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_SCROLL_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		experience := terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: richTestCapabilities(true), Input: os.Stdin, Output: os.Stdout, Diagnostics: os.Stderr,
		})
		run, err := experience.OpenConsole(context.Background(), terminal.ConsoleDescriptor{
			Command: "YCY / scroll", FormCatalog: []terminal.ConsoleFormStep{{ID: "choice", Name: "Select item"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer run.Close()
		answer, err := run.Ask(terminal.InteractionRequest{
			Kind: terminal.InteractionSelect, Message: "Wheel selection", ConsoleStepID: "choice", Options: richListOptions(100),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: fmt.Sprintf("wheel-selected=%s", answer.Value)}}}); err != nil {
			t.Fatal(err)
		}
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestRichRuntimeWheelScrollAndCopyModeRestoreTerminal$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, output, readDone := startRichPTYTest(t, command, "Wheel selection")
	defer process.Close()
	respondToHuhTerminalQueries(t, process, output)
	waitForTrackedPrompt(t, output, "item-000")
	waitForTrackedPrompt(t, output, "\x1b[?1006h")
	// SGR mouse protocol: wheel down inside the body, without changing selection.
	writeRichPTYInput(t, process, strings.Repeat("\x1b[<65;5;5M", 40))
	waitForTrackedPrompt(t, output, "item-099")
	writeRichPTYInput(t, process, "\x07")
	waitForTrackedPrompt(t, output, "\x1b[?1006l")
	writeRichPTYInput(t, process, "\x07")
	// The first Enter must reveal the original selection; only the second submits.
	before := len(output.String())
	writeRichPTYInput(t, process, "\r")
	waitForRichPromptReplacement(t, output, "item-099", "item-000")
	if strings.Contains(output.String()[before:], "wheel-selected=") {
		t.Fatal("hidden selection was submitted")
	}
	writeRichPTYInput(t, process, "\r")
	finishRichPTYTest(t, process, readDone, output)
	text := output.String()
	assertTrackedPTYCleanup(t, text, "wheel-selected=item-000")
	exit := strings.LastIndex(text, "\x1b[?1049l")
	if exit < 0 || !strings.Contains(text[exit:], "\x1b[?1006l") {
		t.Fatal("mouse reporting not restored on exit")
	}
}
