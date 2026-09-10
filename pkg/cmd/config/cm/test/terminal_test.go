package test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestCMTestConsoleDescriptorProvidesSafeStaticAndRequestedContext(t *testing.T) {
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / config cm test",
		Target:  "provider connection",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "scope", Value: "non-mutating provider check"},
			{Label: "profile", Value: "work"},
		},
	}
	if got := terminalCMTestConsoleDescriptor(" work "); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
	withoutProfile := terminalCMTestConsoleDescriptor("")
	if len(withoutProfile.Metadata) != 1 || withoutProfile.Metadata[0].Label != "scope" {
		t.Fatalf("empty profile descriptor = %#v", withoutProfile)
	}
	unsafe := terminalCMTestConsoleDescriptor("profile\x1b[31m\nname")
	for _, field := range []string{unsafe.Command, unsafe.Target, unsafe.Status, unsafe.Metadata[0].Label, unsafe.Metadata[0].Value, unsafe.Metadata[1].Label, unsafe.Metadata[1].Value} {
		if strings.ContainsAny(field, "\r\n\t\x1b") {
			t.Fatalf("unsafe descriptor field contains terminal control: %q", field)
		}
	}
}

func TestTerminalCMTestPresentationPreservesPlainAndAutomationResults(t *testing.T) {
	tests := []struct {
		name   string
		result TestResult
		want   string
	}{
		{name: "success", result: TestResult{Content: "ok"}, want: "Commit message provider test\nResponse:\nok\nDone\n"},
		{name: "failure", result: TestResult{Diagnostic: &TestDiagnostic{Provider: "work", BaseURL: "https://provider.test/v1", Model: "provider-model"}}, want: "Commit message provider test\nProvider request failed\nProvider: work\nBase URL: https://provider.test/v1\nModel: provider-model\n"},
	}
	for _, testCase := range tests {
		for _, session := range []terminalexperience.Capabilities{
			{Interaction: terminalexperience.PlainInteractive},
			{Interaction: terminalexperience.Automation},
		} {
			var output bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
			run := experience.Open(context.Background())
			if err := run.Result(terminalCMTestDocument(testCase.result)); err != nil {
				t.Fatalf("%s Present() error = %v", testCase.name, err)
			}
			if err := run.Close(); err != nil {
				t.Fatalf("%s Close() error = %v", testCase.name, err)
			}
			if got := output.String(); got != testCase.want {
				t.Fatalf("%s %v output = %q, want %q", testCase.name, session.Interaction, got, testCase.want)
			}
			if terminaltest.ContainsTerminalControl(output.Bytes()) {
				t.Fatalf("%s %v output contains terminal control: %q", testCase.name, session.Interaction, output.String())
			}
		}
	}
}

func TestTerminalCMTestPresentationUsesRichSemanticRoles(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result TestResult
		roles  []terminalexperience.VisualRole
	}{
		{name: "success", result: TestResult{Content: "ok"}, roles: []terminalexperience.VisualRole{terminalexperience.VisualRoleTitle, terminalexperience.VisualRolePlain, terminalexperience.VisualRoleSuccess}},
		{name: "failure", result: TestResult{Diagnostic: &TestDiagnostic{Provider: "work", BaseURL: "https://provider.test/v1", Model: "provider-model"}}, roles: []terminalexperience.VisualRole{terminalexperience.VisualRoleTitle, terminalexperience.VisualRoleWarning, terminalexperience.VisualRoleMuted}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, session := range []terminalexperience.Capabilities{
				{Interaction: terminalexperience.RichInteractive},
				{Interaction: terminalexperience.RichInteractive},
			} {
				document := terminalCMTestDocument(testCase.result)
				if len(document.Blocks) != len(testCase.roles) {
					t.Fatalf("Rich blocks = %#v", document.Blocks)
				}
				for index, role := range testCase.roles {
					if document.Blocks[index].Role != role {
						t.Fatalf("Rich block %d role = %v, want %v", index, document.Blocks[index].Role, role)
					}
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
				if terminaltest.ContainsTerminalControl(output.Bytes()) {
					t.Fatalf("non-terminal writer output contains terminal control: %q", output.String())
				}
			}
		})
	}
}

func TestTerminalCMTestRichPresentationBoundsTheResponseAndKeepsUsageOutOfPlainResults(t *testing.T) {
	prompt, completion := 3.0, 2.0
	result := TestResult{
		Content: strings.Repeat("x", cmTestResponseLimit+1),
		usage:   &cmTestTokenUsage{PromptTokens: &prompt, CompletionTokens: &completion},
	}
	rich := terminalCMTestRichDocument(result)
	plain := terminalCMTestDocument(result)
	if rendered := terminalexperience.RenderPlain(rich); !strings.Contains(rendered, "... [truncated]") || !strings.Contains(rendered, "Prompt tokens: 3") || !strings.Contains(rendered, "Total tokens: 5") {
		t.Fatalf("Rich document = %q", rendered)
	}
	if rendered := terminalexperience.RenderPlain(plain); strings.Contains(rendered, "tokens:") || strings.Contains(rendered, "[truncated]") {
		t.Fatalf("Plain document changed durable output = %q", rendered)
	}
	if transcript := terminalexperience.RenderPlain(terminalCMTestResponseSummaryDocument(result)); strings.Contains(transcript, strings.Repeat("x", 32)) || !strings.Contains(transcript, "Total tokens: 5") {
		t.Fatalf("response Transcript summary = %q", transcript)
	}
}

func TestCMTestFinishRequestUsesSafeOutcomeSummary(t *testing.T) {
	prompt, completion := 3.0, 2.0
	succeeded := terminalCMTestFinishRequest(terminalexperience.Succeeded, terminalCMTestResponseSummaryDocument(TestResult{
		Content: "response body must not enter the summary",
		usage:   &cmTestTokenUsage{PromptTokens: &prompt, CompletionTokens: &completion},
	}))
	if got, want := terminalexperience.RenderPlain(succeeded.Summary), "Response received\nPrompt tokens: 3  Completion tokens: 2  Total tokens: 5\n"; got != want {
		t.Fatalf("success Finish summary = %q, want %q", got, want)
	}
	if strings.Contains(terminalexperience.RenderPlain(succeeded.Summary), "response body") {
		t.Fatalf("success Finish summary leaked response content: %q", terminalexperience.RenderPlain(succeeded.Summary))
	}
	failed := terminalCMTestFinishRequest(terminalexperience.Failed, terminalCMTestFailureSummary("Provider request failed (decode)"))
	if got, want := terminalexperience.RenderPlain(failed.Summary), "Provider request failed (decode)\n"; got != want {
		t.Fatalf("failed Finish summary = %q, want %q", got, want)
	}
}
