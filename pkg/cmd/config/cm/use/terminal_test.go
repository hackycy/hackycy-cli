package use

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalCMUsePresentationPreservesPlainAndAutomationResults(t *testing.T) {
	result := UseResult{Profile: "work"}
	const want = "Default CM profile set to work\n"

	for _, session := range []terminalexperience.Capabilities{
		{Interaction: terminalexperience.PlainInteractive},
		{Interaction: terminalexperience.Automation},
	} {
		var output bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
		run := experience.Open(context.Background())
		if err := run.Result(terminalCMUseDocument(result)); err != nil {
			t.Fatalf("Present() error = %v", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got := output.String(); got != want {
			t.Fatalf("%v result = %q, want %q", session.Interaction, got, want)
		}
		if terminaltest.ContainsTerminalControl(output.Bytes()) {
			t.Fatalf("%v result contains terminal control: %q", session.Interaction, output.String())
		}
	}
}

func TestTerminalCMUsePresentationUsesARichSuccessRole(t *testing.T) {
	result := UseResult{Profile: "work"}
	for _, session := range []terminalexperience.Capabilities{
		{Interaction: terminalexperience.RichInteractive},
		{Interaction: terminalexperience.RichInteractive},
	} {
		document := terminalCMUseDocument(result)
		if got, want := document.Blocks[0].Role, terminalexperience.VisualRoleSuccess; got != want {
			t.Fatalf("Rich role = %v, want %v", got, want)
		}
		var output bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
		run := experience.Open(context.Background())
		if err := run.Result(document); err != nil {
			t.Fatalf("Present() error = %v", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got, want := output.String(), "Default CM profile set to work\n"; got != want {
			t.Fatalf("Rich output = %q, want %q", got, want)
		}
		if terminaltest.ContainsTerminalControl(output.Bytes()) {
			t.Fatalf("non-terminal writer output contains terminal control: %q", output.String())
		}
	}
}

func TestTerminalCMUseRichProjectionHidesUnsafeProfileIdentity(t *testing.T) {
	for _, profile := range []string{"bad\nprofile", string([]byte{'b', 0xff, 'd'}), "   "} {
		document := terminalCMUseRichDocument(UseResult{Profile: profile})
		if got := document.Blocks[len(document.Blocks)-1].Text; got != "Default CM profile set to Requested profile" {
			t.Fatalf("profile %q Rich result = %q", profile, got)
		}
	}
}

func TestCMUseConsoleDescriptorProvidesOnlySafeStaticContext(t *testing.T) {
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / config cm use",
		Target:  "profile selection",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{{
			Label: "scope",
			Value: "commit message configuration",
		}},
	}
	if got := cmUseConsoleDescriptor(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
}

func TestCMUseFinishRequestUsesCommandOwnedSafeOutcomeSummary(t *testing.T) {
	succeeded := terminalCMUseFinishRequest(terminalexperience.Succeeded)
	if got, want := succeeded, (terminalexperience.FinishRequest{
		Outcome: terminalexperience.Succeeded,
		Summary: terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleSuccess,
			Text: "Default CM profile set",
		}}},
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("succeeded Finish request = %#v, want %#v", got, want)
	}

	failed := terminalCMUseFinishRequest(terminalexperience.Failed)
	if got, want := failed, (terminalexperience.FinishRequest{
		Outcome: terminalexperience.Failed,
		Summary: terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleError,
			Text: "Unable to set default CM profile",
		}}},
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed Finish request = %#v, want %#v", got, want)
	}
}
