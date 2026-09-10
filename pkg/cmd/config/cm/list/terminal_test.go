package list

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalCMListPresentationPreservesPlainAndAutomationResults(t *testing.T) {
	result := Result{Profiles: []Profile{
		{Name: "work", Model: "gpt-4.1-mini", BaseURL: "https://work.example/v1"},
		{Name: "personal", Model: "deepseek-chat", BaseURL: "https://personal.example/v1", Default: true},
	}}
	const want = "Commit message profiles\nPROFILE  MODEL  BASE URL\n  work gpt-4.1-mini https://work.example/v1\n* personal deepseek-chat https://personal.example/v1\n"

	for _, session := range []terminalexperience.Capabilities{
		{Interaction: terminalexperience.PlainInteractive},
		{Interaction: terminalexperience.Automation},
	} {
		var output bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
		run := experience.Open(context.Background())
		if err := run.Result(terminalCMListDocument(result)); err != nil {
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

func TestTerminalCMListPresentationUsesRichSemanticRoles(t *testing.T) {
	result := Result{Profiles: []Profile{
		{Name: "work", Model: "gpt-4.1-mini", BaseURL: "https://work.example/v1"},
		{Name: "personal", Model: "deepseek-chat", BaseURL: "https://personal.example/v1", Default: true},
	}}

	for _, testCase := range []struct {
		name    string
		session terminalexperience.Capabilities
	}{
		{name: "color", session: terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive}},
		{name: "no color", session: terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			document := terminalCMListDocument(result)
			if got, want := []terminalexperience.VisualRole{
				document.Blocks[0].Role,
				document.Blocks[1].Role,
				document.Blocks[2].Role,
				document.Blocks[3].Role,
			}, []terminalexperience.VisualRole{
				terminalexperience.VisualRoleTitle,
				terminalexperience.VisualRoleMuted,
				terminalexperience.VisualRolePlain,
				terminalexperience.VisualRoleSuccess,
			}; !reflect.DeepEqual(got, want) {
				t.Fatalf("Rich roles = %#v, want %#v", got, want)
			}
			var output bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: testCase.session, Output: &output})
			run := experience.Open(context.Background())
			if err := run.Result(document); err != nil {
				t.Fatalf("Present() error = %v", err)
			}
			if err := run.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			for _, expected := range []string{"Commit message profiles", "PROFILE  MODEL  BASE URL", "work", "personal", "deepseek-chat"} {
				if !strings.Contains(output.String(), expected) {
					t.Fatalf("Rich result = %q, missing %q", output.String(), expected)
				}
			}
			if terminaltest.ContainsTerminalControl(output.Bytes()) {
				t.Fatalf("non-terminal writer output contains terminal control: %q", output.String())
			}
		})
	}
}

func TestTerminalCMListRichProjectionUsesPlaceholdersForUnsafeFields(t *testing.T) {
	result := Result{Profiles: []Profile{{
		Name:    "bad\nname",
		Model:   string([]byte{'m', 0xff, 'x'}),
		BaseURL: "https://user:secret@example.test/v1?token=hidden#fragment",
	}}}
	row := terminalCMListRichRow(result.Profiles[0])
	for _, field := range strings.Split(row, "\t") {
		if strings.ContainsAny(field, "\r\n\x1b") {
			t.Fatalf("unsafe Rich field contains raw control: %q", row)
		}
	}
	for _, expected := range []string{"Profile", "Model configured", "https://example.test/v1"} {
		if !strings.Contains(row, expected) {
			t.Fatalf("Rich row = %q, missing %q", row, expected)
		}
	}
	if strings.Contains(row, "secret") || strings.Contains(row, "hidden") || strings.Contains(row, "fragment") {
		t.Fatalf("Rich row exposed URL credentials/query/fragment: %q", row)
	}
}

func TestCMListConsoleDescriptorProvidesOnlySafeStaticContext(t *testing.T) {
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / config cm list",
		Target:  "profile inventory",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{{
			Label: "scope",
			Value: "commit message configuration",
		}},
	}
	if got := cmListConsoleDescriptor(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
}

func TestCMListFinishRequestUsesOnlySafeSummaries(t *testing.T) {
	request := terminalCMListFinishRequest(terminalexperience.Succeeded, &Result{Profiles: []Profile{{Name: " work ", Default: true}}})
	if request.Outcome != terminalexperience.Succeeded {
		t.Fatalf("Finish outcome = %v, want succeeded", request.Outcome)
	}
	if got, want := terminalexperience.RenderPlain(request.Summary), "Loaded 1 CM profile\nDefault profile: work\n"; got != want {
		t.Fatalf("safe Finish summary = %q, want %q", got, want)
	}
	if text := terminalexperience.RenderPlain(request.Summary); strings.Contains(text, "https://") || strings.Contains(text, "API") {
		t.Fatalf("Finish summary exposed profile detail: %q", text)
	}

	unsafe := terminalCMListFinishRequest(terminalexperience.Succeeded, &Result{Profiles: []Profile{{Name: "bad\nname", Default: true}}})
	if got, want := terminalexperience.RenderPlain(unsafe.Summary), "Loaded 1 CM profile\n"; got != want {
		t.Fatalf("unsafe Finish summary = %q, want %q", got, want)
	}

	empty := terminalCMListFinishRequest(terminalexperience.Succeeded, &Result{})
	if got, want := terminalexperience.RenderPlain(empty.Summary), "Loaded 0 CM profiles\nNo CM profiles configured. Run \"ycy config cm add\" to add one.\n"; got != want {
		t.Fatalf("empty Finish summary = %q, want %q", got, want)
	}

	failed := terminalCMListFinishRequest(terminalexperience.Failed, nil)
	if got, want := terminalexperience.RenderPlain(failed.Summary), "Unable to load CM profiles\n"; got != want {
		t.Fatalf("failed Finish summary = %q, want %q", got, want)
	}
}
