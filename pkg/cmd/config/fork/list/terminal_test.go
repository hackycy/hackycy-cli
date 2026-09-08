package list

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalForkListPresentationPreservesPlainAndAutomationResults(t *testing.T) {
	result := Result{Instances: []Instance{{
		Name:         "work",
		Host:         "gitlab.example",
		Scheme:       "https",
		Type:         "gitlab",
		TokenPreview: "MDEy***",
	}}}
	const want = "Fork provider instances\nNAME  TYPE  SCHEME  HOST  TOKEN\nwork  gitlab  https  gitlab.example  MDEy***\n1 instance configured\n"

	for _, session := range []terminalexperience.Capabilities{
		{Interaction: terminalexperience.PlainInteractive},
		{Interaction: terminalexperience.Automation},
	} {
		var output bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
		run := experience.Open(context.Background())
		if err := run.Result(terminalForkListDocument(result)); err != nil {
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

func TestTerminalForkListPresentationUsesRichSemanticRoles(t *testing.T) {
	result := Result{Instances: []Instance{{
		Name:         "work",
		Host:         "gitlab.example",
		Scheme:       "https",
		Type:         "gitlab",
		TokenPreview: "MDEy***",
	}}}

	for _, testCase := range []struct {
		name    string
		session terminalexperience.Capabilities
	}{
		{name: "color", session: terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive}},
		{name: "no color", session: terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			document := terminalForkListDocument(result)
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
			for _, expected := range []string{"Fork provider instances", "NAME  TYPE  SCHEME  HOST  TOKEN", "work", "gitlab.example", "MDEy***", "1 instance configured"} {
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

func TestTerminalForkListRichProjectionHidesUnsafeFields(t *testing.T) {
	result := Result{Instances: []Instance{{
		Name:         "unsafe\nname",
		Host:         "host\x1b[31m",
		Scheme:       string([]byte{'h', 0xff, 't'}),
		Type:         "provider\tname",
		TokenPreview: "preview\rvalue***",
	}}}

	row := terminalForkListRichRows(result.Instances)
	if strings.ContainsAny(row, "\r\n\t\x1b") {
		t.Fatalf("unsafe Rich row contains raw control: %q", row)
	}
	for _, expected := range []string{"Name configured", "Provider configured", "Scheme configured", "Host configured", "[redacted]"} {
		if !strings.Contains(row, expected) {
			t.Fatalf("unsafe Rich row = %q, missing %q", row, expected)
		}
	}
	for _, raw := range []string{"unsafe", "host", "provider", "preview"} {
		if strings.Contains(row, raw) {
			t.Fatalf("unsafe Rich row exposed %q: %q", raw, row)
		}
	}
}

func TestForkListConsoleDescriptorProvidesOnlySafeStaticContext(t *testing.T) {
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / config fork list",
		Target:  "provider inventory",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{{
			Label: "scope",
			Value: "git fork configuration",
		}},
	}
	if got := forkListConsoleDescriptor(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
}

func TestForkListFinishRequestUsesBoundedResultSummary(t *testing.T) {
	request := terminalForkListFinishRequest(terminalexperience.Succeeded, &Result{Instances: []Instance{{
		Name:         "work",
		Host:         "gitlab.example",
		Scheme:       "https",
		Type:         "gitlab",
		TokenPreview: "MDEy***",
	}}})
	if request.Outcome != terminalexperience.Succeeded {
		t.Fatalf("Finish outcome = %v, want succeeded", request.Outcome)
	}
	if got, want := request.Summary, (terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleSuccess,
		Text: "Loaded 1 fork provider instance",
	}}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Finish summary = %#v, want %#v", got, want)
	}
	if text := request.Summary.Blocks[0].Text; strings.Contains(text, "work") || strings.Contains(text, "MDEy") {
		t.Fatalf("Finish summary leaked list data: %q", text)
	}

	empty := terminalForkListFinishRequest(terminalexperience.Succeeded, &Result{})
	if got, want := empty.Summary.Blocks, []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleSuccess, Text: "Loaded 0 fork provider instances"},
		{Role: terminalexperience.VisualRoleWarning, Text: "No instances configured. Run \"ycy config fork add\" to add one."},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("empty Finish summary = %#v, want %#v", got, want)
	}

	failed := terminalForkListFinishRequest(terminalexperience.Failed, nil)
	if got, want := failed.Summary.Blocks, []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleError,
		Text: "Unable to load fork provider instances",
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed Finish summary = %#v, want %#v", got, want)
	}
}

func environmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
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
