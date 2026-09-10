package pulse

import (
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

func TestTerminalPulseRichDocumentEscapesControlsAndKeepsEveryCommitAcrossLayouts(t *testing.T) {
	root := "/workspace"
	report := Report{CommitCount: 2, Repositories: []RepositoryReport{{
		Path: root + "/team\nproject\x1b[31m",
		Commits: []Commit{
			{Date: "2026-08-23 10:00:00", Author: "Ada\tOne", Subject: "first\x1b[2K subject"},
			{Date: "2026-08-22 10:00:00", Author: "Ben", Subject: "second subject"},
		},
	}}}

	wide := terminalPulseRichDocumentForWidth(root, report, 120)
	wideText := terminalexperience.RenderPlain(wide)
	for _, expected := range []string{"team\\nproject\\x1b[31m", "Ada\\tOne", "first\\x1b[2K subject", "second subject"} {
		if !strings.Contains(wideText, expected) {
			t.Fatalf("wide Rich document missing %q: %q", expected, wideText)
		}
	}
	if strings.Contains(wideText, "\x1b") {
		t.Fatalf("wide Rich document retained terminal control: %q", wideText)
	}

	narrow := terminalPulseRichDocumentForWidth(root, report, 40)
	narrowText := terminalexperience.RenderPlain(narrow)
	if !strings.Contains(narrowText, "Ada\\tOne\n      first\\x1b[2K subject") || !strings.Contains(narrowText, "Ben\n      second subject") {
		t.Fatalf("narrow Rich document did not separate author and subject: %q", narrowText)
	}
}

func TestPulseWarningPathsAreSafeBoundedAndDeterministic(t *testing.T) {
	root := "/workspace"
	paths := []string{
		root + "/zeta",
		root + "/alpha\x1b[2K",
		root + "/beta",
		root + "/gamma",
		root + "/delta",
		root + "/epsilon",
		root + "/eta",
	}
	got := pulseWarningPaths(root, paths)
	for _, expected := range []string{"alpha\\x1b[2K", "beta", "delta", "epsilon", "eta", "... and 2 more"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("warning paths missing %q: %q", expected, got)
		}
	}
	if strings.Contains(got, root) || strings.Contains(got, "\x1b") {
		t.Fatalf("warning paths leaked absolute/control data: %q", got)
	}
}

func TestPulseAuthorInteractionOptionsKeepDistinctValuesAfterSafeLabelsCollide(t *testing.T) {
	options := pulseAuthorInteractionOptions([]AuthorChoice{
		{Value: "one\x1b", Label: "Ada\x1b"},
		{Value: "two", Label: "Ada\\x1b"},
		{Value: "three", Label: "Ada\x1b"},
	})
	if got, want := []string{options[0].Label, options[1].Label, options[2].Label}, []string{"Ada\\x1b (1)", "Ada\\x1b (2)", "Ada\\x1b (3)"}; !samePulseStrings(got, want) {
		t.Fatalf("labels = %#v, want %#v", got, want)
	}
	if got, want := []string{options[0].Value, options[1].Value, options[2].Value}, []string{"one\x1b", "two", "three"}; !samePulseStrings(got, want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
}

func samePulseStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
