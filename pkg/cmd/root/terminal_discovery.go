package root

import (
	"context"
	"errors"
	"fmt"
	"strings"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

type terminalDiscoveryAdapter struct {
	experience terminalexperience.Experience
}

func newTerminalDiscoveryAdapter(experience terminalexperience.Experience) DiscoveryPresenter {
	return terminalDiscoveryAdapter{experience: experience}
}

func (adapter terminalDiscoveryAdapter) PresentDiscovery(ctx context.Context, document DiscoveryDocument) error {
	run := adapter.experience.Open(ctx)
	presentation := terminalDiscoveryDocument(document)
	presentErr := run.Finish(terminalexperience.Succeeded, &presentation)
	return errors.Join(presentErr, run.Close())
}

func terminalDiscoveryDocument(document DiscoveryDocument) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleTitle,
		Text: document.CommandPath,
	}}
	if document.Summary != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRoleMuted,
			Text: document.Summary,
		})
	}
	blocks = append(blocks,
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleActive, Text: "Usage:"},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRolePlain, Text: "  " + document.Usage},
	)
	if len(document.Descendants) > 0 {
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleActive, Text: "Commands:"},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRolePlain, Text: formatDiscoveryDescendants(document.Descendants)},
		)
	}
	if len(document.Flags) > 0 {
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleActive, Text: "Flags:"},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRolePlain, Text: formatDiscoveryFlags(document.Flags)},
		)
	}
	if len(document.Examples) > 0 {
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleActive, Text: "Examples:"},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRolePlain, Text: strings.Join(document.Examples, "\n")},
		)
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func formatDiscoveryDescendants(descendants []DiscoveryDescendant) string {
	lines := make([]string, 0, len(descendants))
	for _, descendant := range descendants {
		line := "  " + descendant.Name
		if descendant.Summary != "" {
			line += "\t" + descendant.Summary
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatDiscoveryFlags(flags []DiscoveryFlag) string {
	lines := make([]string, 0, len(flags))
	for _, flag := range flags {
		name := "--" + flag.Name
		if flag.Shorthand != "" {
			name = fmt.Sprintf("-%s, %s", flag.Shorthand, name)
		}
		line := "  " + name
		if flag.Usage != "" {
			line += "\t" + flag.Usage
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
