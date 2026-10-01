package terminal_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestPresentationUsesEffectiveProfile(t *testing.T) {
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleTitle, Text: "Result"}}}
	for _, profile := range []terminal.ColorProfile{terminal.NoColor, terminal.ANSI16, terminal.ANSI256, terminal.TrueColor} {
		var output bytes.Buffer
		if err := terminal.WriteRich(&output, document, terminal.RichOptions{Profile: profile}); err != nil {
			t.Fatal(err)
		}
		if terminaltest.StripANSI(output.String()) != "Result\n" {
			t.Fatalf("profile %d: %q", profile, output.String())
		}
		sgr := terminaltest.StyleSequences(output.String())
		if profile == terminal.NoColor && sgr != "" {
			t.Fatalf("monochrome styles: %q", sgr)
		}
		if profile == terminal.ANSI16 && (strings.Contains(sgr, "38;2") || strings.Contains(sgr, "38;5")) {
			t.Fatalf("ANSI16 styles: %q", sgr)
		}
		if profile == terminal.ANSI256 && (strings.Contains(sgr, "38;2") || !strings.Contains(sgr, "38;5")) {
			t.Fatalf("ANSI256 styles: %q", sgr)
		}
		if profile == terminal.TrueColor && !strings.Contains(sgr, "38;2") {
			t.Fatalf("truecolor styles: %q", sgr)
		}
	}
}

func TestForcedPipeTextNeverControlsScreen(t *testing.T) {
	var output bytes.Buffer
	runtime := terminal.NewExperience(terminal.ExperienceOptions{Capabilities: terminal.Capabilities{Stdout: terminal.StreamCapability{Profile: terminal.ANSI16}}, Output: &output})
	run := runtime.Open(context.Background())
	if _, err := run.Ask(terminal.InteractionRequest{Kind: terminal.InteractionText, Message: "input"}); err != terminal.ErrAutomationInteraction {
		t.Fatalf("Ask() = %v", err)
	}
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleTitle, Text: "safe\x1b[2J\x1b[?1049h"}}}
	if err := run.Finish(terminal.FinishRequest{Outcome: terminal.Succeeded}, &document); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b[") || strings.Contains(output.String(), "2J") || strings.Contains(output.String(), "1049") {
		t.Fatalf("forced text: %q", output.String())
	}
}

func TestMonochromeTerminalResultsStillWrap(t *testing.T) {
	var output bytes.Buffer
	runtime := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Stdout: terminal.StreamCapability{Terminal: true}}, Output: &output, Width: 8,
	})
	run := runtime.Open(context.Background())
	if err := run.Result(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "first second"}}}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "first\nsecond\n" {
		t.Fatalf("monochrome layout = %q", output.String())
	}
}
