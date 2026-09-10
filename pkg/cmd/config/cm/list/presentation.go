package list

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

const terminalCMListEmptyMessage = "No CM profiles configured. Run \"ycy config cm add\" to add one."

func terminalCMListDocument(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleTitle, Text: "Commit message profiles"}}
	if len(result.Profiles) == 0 {
		return terminalexperience.PresentationDocument{Blocks: append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "No CM profiles configured."},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Run \"ycy config cm add\" to add one."},
		)}
	}
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "PROFILE  MODEL  BASE URL"})
	for _, profile := range result.Profiles {
		role := terminalexperience.VisualRolePlain
		if profile.Default {
			role = terminalexperience.VisualRoleSuccess
		}
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: role, Text: terminalCMListRow(profile)})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalCMListRichDocument(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / config cm list"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Commit message profiles"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Configured providers for commit message generation"},
	}
	if len(result.Profiles) == 0 {
		return terminalexperience.PresentationDocument{Blocks: append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "No CM profiles configured."},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Run \"ycy config cm add\" to add one."},
		)}
	}
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "DEFAULT\tPROFILE\tMODEL\tBASE URL"})
	for _, profile := range result.Profiles {
		role := terminalexperience.VisualRolePlain
		if profile.Default {
			role = terminalexperience.VisualRoleSuccess
		}
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: role, Text: terminalCMListRichRow(profile)})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalCMListSummaryDocument(result Result) terminalexperience.PresentationDocument {
	count := len(result.Profiles)
	label := "profiles"
	if count == 1 {
		label = "profile"
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleSuccess,
		Text: fmt.Sprintf("Loaded %d CM %s", count, label),
	}}}
}

func terminalCMListFinishSummaryDocument(result Result) terminalexperience.PresentationDocument {
	blocks := append([]terminalexperience.PresentationBlock(nil), terminalCMListSummaryDocument(result).Blocks...)
	blocks = append(blocks, terminalCMListDefaultDocument(result).Blocks...)
	if len(result.Profiles) == 0 {
		blocks = append(blocks, terminalCMListEmptyDocument().Blocks...)
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalCMListDefaultDocument(result Result) terminalexperience.PresentationDocument {
	for _, profile := range result.Profiles {
		if !profile.Default || !cmListValueSafe(profile.Name) {
			continue
		}
		name := strings.TrimSpace(safeCMListText(profile.Name))
		if name == "" {
			continue
		}
		return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleMuted,
			Text: "Default profile: " + name,
		}}}
	}
	return terminalexperience.PresentationDocument{}
}

func terminalCMListEmptyDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleWarning,
		Text: "No CM profiles configured. Run \"ycy config cm add\" to add one.",
	}}}
}

func terminalCMListPlainText(profiles []Profile) string {
	if len(profiles) == 0 {
		return terminalCMListEmptyMessage + "\n"
	}
	var output bytes.Buffer
	for _, profile := range profiles {
		_, _ = fmt.Fprintln(&output, terminalCMListRow(profile))
	}
	return output.String()
}

func terminalCMListRow(profile Profile) string {
	marker := " "
	if profile.Default {
		marker = "*"
	}
	return fmt.Sprintf("%s %s %s %s", marker, profile.Name, profile.Model, profile.BaseURL)
}

func terminalCMListRichRow(profile Profile) string {
	marker := "-"
	if profile.Default {
		marker = "✓"
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s",
		marker,
		safeCMListField(profile.Name, "Profile"),
		safeCMListField(profile.Model, "Model configured"),
		safeCMListURL(profile.BaseURL),
	)
}

func safeCMListURL(value string) string {
	if !cmListValueSafe(value) {
		return "Base URL configured"
	}
	value = safeCMListText(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "Base URL configured"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if parsed.Path == "" {
		return boundCMListText(parsed.Scheme + "://" + parsed.Host)
	}
	return boundCMListText(parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath())
}

func safeCMListField(value, fallback string) string {
	if !cmListValueSafe(value) {
		return fallback
	}
	value = safeCMListText(value)
	if value == "" {
		return fallback
	}
	return value
}

func safeCMListText(value string) string {
	if !utf8.ValidString(value) {
		return ""
	}
	var output strings.Builder
	for _, character := range value {
		switch {
		case character == '\r' || character == '\n' || character == '\t':
			output.WriteByte(' ')
		case unicode.IsControl(character):
			output.WriteRune('\uFFFD')
		default:
			output.WriteRune(character)
		}
	}
	return boundCMListText(output.String())
}

func cmListValueSafe(value string) bool {
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

func boundCMListText(value string) string {
	runes := []rune(value)
	if len(runes) > 256 {
		return string(runes[:256]) + "..."
	}
	return string(runes)
}
