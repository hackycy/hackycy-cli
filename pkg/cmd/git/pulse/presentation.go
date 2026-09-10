package pulse

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

func terminalPulseRichDocumentForWidth(root string, report Report, width int) terminalexperience.PresentationDocument {
	if width <= 0 {
		width = pulseRichDefaultWidth
	}
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / git pulse"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Workspace commit activity"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Recent commits grouped by repository"},
		{Role: terminalexperience.VisualRoleSuccess, Text: fmt.Sprintf("Found %d %s in %d %s", report.CommitCount, pulsePlural(report.CommitCount, "commit", "commits"), len(report.Repositories), pulsePlural(len(report.Repositories), "repository", "repositories"))},
	}
	for _, repository := range report.Repositories {
		relative := pulseSafeRelativePath(root, repository.Path)
		name := safePulseValue(filepath.Base(relative))
		parent := safePulseValue(filepath.Dir(relative))
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleActive, Text: fmt.Sprintf("%s (%d %s)", name, len(repository.Commits), pulsePlural(len(repository.Commits), "commit", "commits"))},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "   " + parent + string(filepath.Separator)},
		)
		for index, commit := range repository.Commits {
			connector := "|-"
			if index == len(repository.Commits)-1 {
				connector = "`-"
			}
			date := safePulseValue(commit.Date)
			author := safePulseValue(commit.Author)
			subject := safePulseValue(commit.Subject)
			text := fmt.Sprintf("   %s %s | %s | %s", connector, date, author, subject)
			if width < pulseRichNarrowWidth {
				text = fmt.Sprintf("   %s %s | %s\n      %s", connector, date, author, subject)
			}
			blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRolePlain, Text: text})
		}
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func pulseSafeRelativePath(root, path string) string {
	return safePulseValue(pulseRelativePath(root, path))
}

func pulseWarningPaths(root string, paths []string) string {
	if len(paths) == 0 {
		return "none"
	}
	ordered := append([]string(nil), paths...)
	sort.Strings(ordered)
	limit := min(len(ordered), pulseWarningPathLimit)
	projected := make([]string, 0, limit+1)
	for _, path := range ordered[:limit] {
		projected = append(projected, safePulseField(pulseRelativePath(root, path), 160))
	}
	if remaining := len(ordered) - limit; remaining > 0 {
		projected = append(projected, fmt.Sprintf("... and %d more", remaining))
	}
	return strings.Join(projected, ", ")
}

func pulseDayLabel(days int) string {
	switch days {
	case 1:
		return "Today"
	case 2:
		return "Yesterday"
	case 3:
		return "Last 3 days"
	case 7:
		return "Last 7 days"
	case 30:
		return "Last 30 days"
	default:
		return fmt.Sprintf("%d days", days)
	}
}

func safePulseField(value string, limit int) string {
	value = safePulseValue(value)
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

// safePulseValue preserves ordinary Unicode while displaying terminal control
// bytes as literals, so command data cannot alter the terminal state.
func safePulseValue(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	var output strings.Builder
	for _, character := range value {
		switch character {
		case '\n':
			output.WriteString("\\n")
		case '\r':
			output.WriteString("\\r")
		case '\t':
			output.WriteString("\\t")
		default:
			if unicode.IsControl(character) {
				if character <= 0xff {
					_, _ = fmt.Fprintf(&output, "\\x%02x", character)
				} else {
					_, _ = fmt.Fprintf(&output, "\\u%04x", character)
				}
				continue
			}
			output.WriteRune(character)
		}
	}
	value = strings.TrimSpace(output.String())
	if value == "" {
		return "-"
	}
	return value
}

func isPulseCancellation(err error) bool {
	if err == nil || (!errorsIsCancellation(err)) {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !isPulseCancellation(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if cause := wrapped.Unwrap(); cause != nil {
			return isPulseCancellation(cause)
		}
	}
	return true
}

func errorsIsCancellation(err error) bool {
	return err != nil && (isContextCancellation(err, context.Canceled) || isContextCancellation(err, context.DeadlineExceeded))
}

func isContextCancellation(err, target error) bool {
	return errors.Is(err, target)
}
