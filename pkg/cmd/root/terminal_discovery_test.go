package root

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalDiscoveryPresenterTranslatesAndClosesOneExperienceRun(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	presenter := newTerminalDiscoveryAdapter(experience)
	err := presenter.PresentDiscovery(context.Background(), DiscoveryDocument{
		CommandPath: "ycy config",
		Summary:     "Manage ycy configuration",
		Usage:       "ycy config [flags]",
		Descendants: []DiscoveryDescendant{{Name: "cm", Summary: "Manage CM profiles"}},
		Flags:       []DiscoveryFlag{{Name: "log-level", Usage: "Log level"}},
		Examples:    []string{"ycy config --help"},
	})
	if err != nil {
		t.Fatalf("PresentDiscovery() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 2 || operations[0].Kind != terminaltest.FinishOperation || operations[1].Kind != terminaltest.CloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	finish, ok := operations[0].Value.(terminaltest.Finish)
	if !ok {
		t.Fatalf("presented value = %#v", operations[0].Value)
	}
	if finish.Outcome != terminalexperience.Succeeded || finish.Document == nil {
		t.Fatalf("finish = %#v", finish)
	}
	document := *finish.Document
	want := terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleTitle, Text: "ycy config"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Manage ycy configuration"},
		{Role: terminalexperience.VisualRoleActive, Text: "Usage:"},
		{Role: terminalexperience.VisualRolePlain, Text: "  ycy config [flags]"},
		{Role: terminalexperience.VisualRoleActive, Text: "Commands:"},
		{Role: terminalexperience.VisualRolePlain, Text: "  cm\tManage CM profiles"},
		{Role: terminalexperience.VisualRoleActive, Text: "Flags:"},
		{Role: terminalexperience.VisualRolePlain, Text: "  --log-level\tLog level"},
		{Role: terminalexperience.VisualRoleActive, Text: "Examples:"},
		{Role: terminalexperience.VisualRolePlain, Text: "ycy config --help"},
	}}
	if !reflect.DeepEqual(document, want) {
		t.Fatalf("document = %#v, want %#v", document, want)
	}
}

func TestTerminalDiscoveryDocumentUsesBDurableHierarchyWithoutChangingContent(t *testing.T) {
	document := terminalDiscoveryDocument(DiscoveryDocument{
		CommandPath: "ycy config",
		Summary:     "Manage ycy configuration",
		Usage:       "ycy config [flags]",
		Descendants: []DiscoveryDescendant{{Name: "cm", Summary: "Manage CM profiles"}},
		Flags:       []DiscoveryFlag{{Name: "log-level", Usage: "Log level"}},
		Examples:    []string{"ycy config --help"},
	})
	const want = "ycy config\nManage ycy configuration\nUsage:\n  ycy config [flags]\nCommands:\n  cm\tManage CM profiles\nFlags:\n  --log-level\tLog level\nExamples:\nycy config --help\n"
	if got := terminalexperience.RenderPlain(document); got != want {
		t.Fatalf("plain document = %q, want %q", got, want)
	}

	var colored bytes.Buffer
	if err := terminalexperience.WriteRich(&colored, document, terminalexperience.RichOptions{Width: 120, Color: true}); err != nil {
		t.Fatalf("WriteRich() error = %v", err)
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatalf("colored durable document omitted B hierarchy styling: %q", colored.String())
	}
	for _, field := range []string{"ycy config", "Manage ycy configuration", "Usage:", "ycy config [flags]", "Commands:", "cm", "Manage CM profiles", "Flags:", "--log-level", "Log level", "Examples:", "ycy config --help"} {
		if !strings.Contains(terminaltest.StripANSI(colored.String()), field) {
			t.Fatalf("colored durable document omitted %q: %q", field, colored.String())
		}
	}

	var noColor bytes.Buffer
	if err := terminalexperience.WriteRich(&noColor, document, terminalexperience.RichOptions{Width: 40, Color: false}); err != nil {
		t.Fatalf("WriteRich() no-color error = %v", err)
	}
	if terminaltest.ContainsTerminalControl(noColor.Bytes()) {
		t.Fatalf("no-color durable document contains terminal control: %q", noColor.String())
	}
}
