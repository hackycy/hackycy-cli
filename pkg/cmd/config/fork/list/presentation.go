package list

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

const terminalForkListEmptyMessage = "No instances configured. Run \"ycy config fork add\" to add one."

func terminalForkListDocument(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleTitle,
		Text: "Fork provider instances",
	}}
	if len(result.Instances) == 0 {
		return terminalexperience.PresentationDocument{Blocks: append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "No instances configured."},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Run \"ycy config fork add\" to add one."},
		)}
	}
	blocks = append(blocks,
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "NAME  TYPE  SCHEME  HOST  TOKEN"},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRolePlain, Text: terminalForkListRows(result.Instances)},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: terminalForkListCount(len(result.Instances))},
	)
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalForkListRichDocument(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / config fork list"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Fork provider instances"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Configured providers for git fork operations"},
	}
	if len(result.Instances) == 0 {
		return terminalexperience.PresentationDocument{Blocks: append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "No instances configured."},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Run \"ycy config fork add\" to add one."},
		)}
	}
	blocks = append(blocks,
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "NAME\tTYPE\tSCHEME\tHOST\tTOKEN"},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRolePlain, Text: terminalForkListRichRows(result.Instances)},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: terminalForkListCount(len(result.Instances))},
	)
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalForkListFinishSummaryDocument(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleSuccess,
		Text: terminalForkListCountSummary(len(result.Instances)),
	}}
	if len(result.Instances) == 0 {
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRoleWarning,
			Text: "No instances configured. Run \"ycy config fork add\" to add one.",
		})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalForkListPlainText(instances []Instance) string {
	if len(instances) == 0 {
		return terminalForkListEmptyMessage + "\n"
	}

	var output bytes.Buffer
	table := tabwriter.NewWriter(&output, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "NAME\tTYPE\tSCHEME\tHOST\tTOKEN")
	for _, instance := range instances {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", instance.Name, instance.Type, instance.Scheme, instance.Host, instance.TokenPreview)
	}
	_ = table.Flush()
	_, _ = fmt.Fprintln(&output, terminalForkListCount(len(instances)))
	return output.String()
}

func terminalForkListRows(instances []Instance) string {
	lines := make([]string, 0, len(instances))
	for _, instance := range instances {
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s  %s", instance.Name, instance.Type, instance.Scheme, instance.Host, instance.TokenPreview))
	}
	return strings.Join(lines, "\n")
}

func terminalForkListRichRows(instances []Instance) string {
	var output bytes.Buffer
	table := tabwriter.NewWriter(&output, 0, 8, 2, ' ', 0)
	for _, instance := range instances {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			safeForkListField(instance.Name, "Name configured"),
			safeForkListField(instance.Type, "Provider configured"),
			safeForkListField(instance.Scheme, "Scheme configured"),
			safeForkListField(instance.Host, "Host configured"),
			safeForkTokenPreview(instance.TokenPreview),
		)
	}
	_ = table.Flush()
	return strings.TrimSuffix(output.String(), "\n")
}

func terminalForkListCountSummary(count int) string {
	return fmt.Sprintf("Loaded %d fork provider %s", count, pluralForkList(count))
}

func pluralForkList(count int) string {
	if count == 1 {
		return "instance"
	}
	return "instances"
}

func safeForkListField(value, fallback string) string {
	if !forkListValueSafe(value) {
		return fallback
	}
	value = safeForkListText(value)
	if value == "" {
		return fallback
	}
	return value
}

func safeForkTokenPreview(value string) string {
	if !forkListValueSafe(value) {
		return "[redacted]"
	}
	value = safeForkListText(value)
	if value == "***" || (strings.HasSuffix(value, "***") && !strings.Contains(value, ":") && len([]rune(value)) <= 64) {
		return value
	}
	return "[redacted]"
}

func safeForkListText(value string) string {
	runes := []rune(value)
	if len(runes) > 256 {
		return string(runes[:256]) + "..."
	}
	return value
}

func forkListValueSafe(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func terminalForkListCount(count int) string {
	label := "instances"
	if count == 1 {
		label = "instance"
	}
	return fmt.Sprintf("%d %s configured", count, label)
}
