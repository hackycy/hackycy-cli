package pulse

import (
	"bytes"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
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
	if !strings.Contains(narrowText, "2026-08-23 10:00:00\n      Ada\\tOne\n      first\\x1b[2K subject") || !strings.Contains(narrowText, "2026-08-22 10:00:00\n      Ben\n      second subject") {
		t.Fatalf("narrow Rich document did not separate time, author, and subject: %q", narrowText)
	}
}

func TestTerminalPulseRichDocumentUsesSemanticFieldHierarchyAndRepositorySpacing(t *testing.T) {
	root := "/workspace"
	report := Report{CommitCount: 2, Repositories: []RepositoryReport{
		{Path: root + "/alpha", Commits: []Commit{{Date: "2026-09-17 14:32:05", Author: "Ada Lovelace", Subject: "first subject"}}},
		{Path: root + "/beta", Commits: []Commit{{Date: "2026-09-16 09:08:07", Author: "Grace Hopper", Subject: "second subject"}}},
	}}

	document := terminalPulseRichDocumentForWidth(root, report, 120)
	if got, want := len(document.Blocks), 11; got != want {
		t.Fatalf("block count = %d, want %d: %#v", got, want, document.Blocks)
	}
	repository := document.Blocks[4]
	if repository.Role != terminalexperience.VisualRoleActive || repository.Text != "alpha" || len(repository.Spans) != 1 || repository.Spans[0].Role != terminalexperience.VisualRoleMuted || repository.Spans[0].Text != " (1 commit)" {
		t.Fatalf("repository header = %#v", repository)
	}
	commit := document.Blocks[6]
	if commit.Role != terminalexperience.VisualRoleMuted || commit.Text != "   `- " {
		t.Fatalf("commit prefix = %#v", commit)
	}
	wantRoles := []terminalexperience.VisualRole{
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleActive,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRolePlain,
	}
	if len(commit.Spans) != len(wantRoles) {
		t.Fatalf("commit spans = %#v", commit.Spans)
	}
	for index, role := range wantRoles {
		if commit.Spans[index].Role != role {
			t.Fatalf("commit span %d role = %v, want %v: %#v", index, commit.Spans[index].Role, role, commit.Spans)
		}
	}

	plain := terminalexperience.RenderPlain(document)
	if !strings.Contains(plain, "first subject\n\nbeta (1 commit)") {
		t.Fatalf("repository groups are not separated by one blank line: %q", plain)
	}
	var colored bytes.Buffer
	if err := terminalexperience.WriteRich(&colored, document, terminalexperience.RichOptions{Width: 120, Color: true}); err != nil {
		t.Fatalf("WriteRich() error = %v", err)
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatalf("colored report omitted semantic styles: %q", colored.String())
	}
	if got := terminaltest.StripANSI(colored.String()); got != plain {
		t.Fatalf("colored report changed visible content: got %q, want %q", got, plain)
	}
}

func TestTerminalPulseRichDocumentSwitchesAtEightyColumnsAndHangingWrapsLongFields(t *testing.T) {
	root := "/workspace"
	commit := Commit{
		Date:    "2026-09-17 14:32:05",
		Author:  "Ada Lovelace with a deliberately long display name",
		Subject: "实现一个需要在窄终端中稳定换行且不能截断内容的提交说明",
	}
	report := Report{CommitCount: 1, Repositories: []RepositoryReport{{Path: root + "/alpha", Commits: []Commit{commit}}}}

	wideText := terminalexperience.RenderPlain(terminalPulseRichDocumentForWidth(root, report, 120))
	if !strings.Contains(wideText, "2026-09-17 14:32:05 | Ada Lovelace with a deliberately long display name | ") {
		t.Fatalf("wide report did not retain its single-line metadata layout: %q", wideText)
	}
	boundaryText := terminalexperience.RenderPlain(terminalPulseRichDocumentForWidth(root, Report{CommitCount: 1, Repositories: []RepositoryReport{{Path: root + "/alpha", Commits: []Commit{{Date: commit.Date, Author: "Ada", Subject: "short"}}}}}, 80))
	if !strings.Contains(boundaryText, "2026-09-17 14:32:05 | Ada | short") {
		t.Fatalf("80-column report unexpectedly used the narrow layout: %q", boundaryText)
	}

	narrowText := terminalexperience.RenderPlain(terminalPulseRichDocumentForWidth(root, report, 40))
	if !strings.Contains(narrowText, "2026-09-17 14:32:05\n      Ada Lovelace") || strings.Contains(narrowText, "2026-09-17 14:32:05 |") {
		t.Fatalf("narrow report did not use the three-line layout: %q", narrowText)
	}
	for _, line := range strings.Split(strings.TrimSuffix(narrowText, "\n"), "\n") {
		if width := terminalexperience.TextWidth(line); width > 40 {
			t.Fatalf("narrow line width = %d, want <= 40: %q", width, line)
		}
	}
	for _, expected := range []string{"Ada Lovelace with a deliberately", "long display name"} {
		if !strings.Contains(narrowText, expected) {
			t.Fatalf("narrow report lost wrapped field %q: %q", expected, narrowText)
		}
	}
	joinedContinuations := strings.ReplaceAll(narrowText, "\n"+pulseCommitIndent, "")
	if !strings.Contains(joinedContinuations, commit.Subject) {
		t.Fatalf("narrow report truncated the subject: %q", narrowText)
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
