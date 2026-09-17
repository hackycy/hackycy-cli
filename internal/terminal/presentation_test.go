package terminal_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestWritePlainRendersDurableDocumentToStdoutWithoutTerminalControl(t *testing.T) {
	document := terminal.PresentationDocument{
		Blocks: []terminal.PresentationBlock{
			{Role: terminal.VisualRoleTitle, Text: "HACKYCY CLI"},
			{Role: terminal.VisualRoleSuccess, Text: "Saved \x1b[32mconfiguration\x1b[0m"},
			{Role: terminal.VisualRoleMuted, Text: "path: /tmp/ycy\n"},
		},
	}
	var stdout bytes.Buffer

	if err := terminal.WritePlain(&stdout, document); err != nil {
		t.Fatalf("WritePlain() error = %v", err)
	}
	if got, want := stdout.String(), "HACKYCY CLI\nSaved configuration\npath: /tmp/ycy\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if terminaltest.ContainsTerminalControl(stdout.Bytes()) {
		t.Fatalf("plain stdout contains terminal control: %q", stdout.String())
	}
}

func TestRenderPlainDoesNotInventOutputForAnEmptyDocument(t *testing.T) {
	if got := terminal.RenderPlain(terminal.PresentationDocument{}); got != "" {
		t.Fatalf("RenderPlain(empty) = %q", got)
	}
}

func TestWritePlainRedactsSensitiveBlocks(t *testing.T) {
	var stdout bytes.Buffer
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{
		{Text: "token=secret-value", Sensitive: true},
		{Text: "safe summary"},
	}}

	if err := terminal.WritePlain(&stdout, document); err != nil {
		t.Fatalf("WritePlain() error = %v", err)
	}
	if got, want := stdout.String(), "[redacted]\nsafe summary\n"; got != want {
		t.Fatalf("plain output = %q, want %q", got, want)
	}
}

func TestPresentationSpansKeepPlainContentSafeAndRedacted(t *testing.T) {
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{
		{
			Role: terminal.VisualRoleMuted,
			Text: "2026-09-17\x1b[2K | ",
			Spans: []terminal.PresentationSpan{
				{Role: terminal.VisualRoleActive, Text: "Ada"},
				{Role: terminal.VisualRoleMuted, Text: " | "},
				{Role: terminal.VisualRolePlain, Text: "secret", Sensitive: true},
			},
		},
		{Text: "ignored", Sensitive: true, Spans: []terminal.PresentationSpan{{Text: "also ignored"}}},
	}}

	if got, want := terminal.RenderPlain(document), "2026-09-17 | Ada | [redacted]\n[redacted]\n"; got != want {
		t.Fatalf("plain inline document = %q, want %q", got, want)
	}
}

func TestWriteRichStylesInlineSpansWithoutChangingVisibleContent(t *testing.T) {
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{
		Role: terminal.VisualRoleMuted,
		Text: "2026-09-17 14:32:05 | ",
		Spans: []terminal.PresentationSpan{
			{Role: terminal.VisualRoleActive, Text: "Ada Lovelace"},
			{Role: terminal.VisualRoleMuted, Text: " | "},
			{Role: terminal.VisualRolePlain, Text: "feat: add cache support"},
		},
	}}}

	var colored bytes.Buffer
	if err := terminal.WriteRich(&colored, document, terminal.RichOptions{Width: 120, Color: true}); err != nil {
		t.Fatalf("WriteRich(color) error = %v", err)
	}
	if got, want := ansi.Strip(colored.String()), terminal.RenderPlain(document); got != want {
		t.Fatalf("colored visible content = %q, want %q", got, want)
	}
	for _, sequence := range []string{"\x1b[2;", "\x1b[1;"} {
		if !strings.Contains(colored.String(), sequence) {
			t.Fatalf("colored inline document missing semantic style %q: %q", sequence, colored.String())
		}
	}

	var noColor bytes.Buffer
	if err := terminal.WriteRich(&noColor, document, terminal.RichOptions{Width: 120, Color: false}); err != nil {
		t.Fatalf("WriteRich(NO_COLOR) error = %v", err)
	}
	if got, want := noColor.String(), terminal.RenderPlain(document); got != want {
		t.Fatalf("NO_COLOR inline document = %q, want %q", got, want)
	}
	if terminaltest.ContainsTerminalControl(noColor.Bytes()) {
		t.Fatalf("NO_COLOR inline document contains terminal control: %q", noColor.String())
	}
}

func TestWriteRichWrapsStyledUnicodeSpansByTerminalCells(t *testing.T) {
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{
		Role:  terminal.VisualRoleMuted,
		Text:  "作者: ",
		Spans: []terminal.PresentationSpan{{Role: terminal.VisualRoleActive, Text: "艾达洛夫莱斯提交记录"}},
	}}}

	var output bytes.Buffer
	if err := terminal.WriteRich(&output, document, terminal.RichOptions{Width: 10, Color: true}); err != nil {
		t.Fatalf("WriteRich() error = %v", err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(ansi.Strip(output.String()), "\n"), "\n") {
		if width := ansi.StringWidth(line); width > 10 {
			t.Fatalf("wrapped line width = %d, want <= 10: %q", width, line)
		}
	}
}

func TestWriteRichWrapsWithoutTruncating(t *testing.T) {
	var stdout bytes.Buffer
	document := terminal.PresentationDocument{
		Blocks: []terminal.PresentationBlock{{
			Role: terminal.VisualRoleActive,
			Text: "Choose a repository with a descriptive name",
		}},
	}

	if err := terminal.WriteRich(&stdout, document, terminal.RichOptions{Width: 12}); err != nil {
		t.Fatalf("WriteRich() error = %v", err)
	}
	if got, want := stdout.String(), "Choose a\nrepository\nwith a\ndescriptive\nname\n"; got != want {
		t.Fatalf("rich output = %q, want %q", got, want)
	}
}

func TestWriteRichNoColorContainsNoStyleBytes(t *testing.T) {
	var stdout bytes.Buffer
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleTitle, Text: "HACKYCY CLI"}}}

	if err := terminal.WriteRich(&stdout, document, terminal.RichOptions{Color: false}); err != nil {
		t.Fatalf("WriteRich() error = %v", err)
	}
	if got, want := stdout.String(), "HACKYCY CLI\n"; got != want {
		t.Fatalf("NO_COLOR rich output = %q, want %q", got, want)
	}
}

func TestWriteRichUsesBDurableHierarchyWithoutChangingDocumentOrTerminalMode(t *testing.T) {
	document := terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{
		{Role: terminal.VisualRoleTitle, Text: "YCY CONFIG"},
		{Role: terminal.VisualRoleMuted, Text: "workspace repo"},
		{Role: terminal.VisualRoleActive, Text: "Commands:"},
		{Role: terminal.VisualRolePlain, Text: "  list  List profiles"},
		{Role: terminal.VisualRoleSuccess, Text: "Saved"},
	}}
	var stdout bytes.Buffer

	if err := terminal.WriteRich(&stdout, document, terminal.RichOptions{Width: 120, Color: true}); err != nil {
		t.Fatalf("WriteRich() error = %v", err)
	}
	output := stdout.String()
	if got, want := ansi.Strip(output), terminal.RenderPlain(document); got != want {
		t.Fatalf("durable content = %q, want %q", got, want)
	}
	for _, sequence := range []string{"\x1b[?1049h", "\x1b[?1049l", "\x1b[?1047h", "\x1b[?1047l"} {
		if strings.Contains(output, sequence) {
			t.Fatalf("durable Rich output started terminal mode %q: %q", sequence, output)
		}
	}
	if !strings.Contains(output, "\x1b[") {
		t.Fatalf("durable Rich output has no B hierarchy styling: %q", output)
	}
}

func TestWriteRichUsesSemanticStylesOnPTY(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_RICH_RENDER_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		document := terminal.PresentationDocument{
			Blocks: []terminal.PresentationBlock{
				{Role: terminal.VisualRoleTitle, Text: "HACKYCY CLI"},
				{Role: terminal.VisualRoleSuccess, Text: "Saved"},
				{Role: terminal.VisualRoleWarning, Text: "Review this"},
				{Role: terminal.VisualRoleError, Text: "Failed"},
			},
		}
		if err := terminal.WriteRich(os.Stdout, document, terminal.RichOptions{Color: true}); err != nil {
			t.Fatalf("WriteRich() error = %v", err)
		}
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestWriteRichUsesSemanticStylesOnPTY$")
	command.Env = append(richPTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()

	var output bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if err := process.Wait(); err != nil {
		t.Fatalf("wait PTY helper: %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reading PTY output")
	}
	if got := output.String(); !strings.Contains(got, "\x1b[") {
		t.Fatalf("rich PTY output has no terminal style: %q", got)
	}
}

func richPTYEnvironment() []string {
	ignored := map[string]struct{}{
		"CI":             {},
		"CLICOLOR":       {},
		"CLICOLOR_FORCE": {},
		"COLORTERM":      {},
		"NO_COLOR":       {},
		"TERM":           {},
	}
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := ignored[key]; !skip {
			environment = append(environment, entry)
		}
	}
	return environment
}
