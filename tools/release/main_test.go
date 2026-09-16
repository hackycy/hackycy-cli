package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
)

func TestCandidates(t *testing.T) {
	current, err := parseStableVersion("0.0.69")
	if err != nil {
		t.Fatal(err)
	}
	kind := bumpMinor
	got := candidates(current, &kind)
	want := []string{"1.0.0", "0.1.0", "0.0.70", "0.0.70", "0.1.0"}
	if len(got) != len(want) {
		t.Fatalf("candidate count = %d, want %d", len(got), len(want))
	}
	for index, candidate := range got {
		if candidate.Version != want[index] {
			t.Errorf("candidate %s = %s, want %s", candidate.Kind, candidate.Version, want[index])
		}
	}
}

func TestConventionalBump(t *testing.T) {
	tests := []struct {
		name     string
		messages string
		want     bumpKind
		found    bool
	}{
		{name: "breaking header", messages: "feat(api)!: change\x1fdetails\x1e", want: bumpMajor, found: true},
		{name: "breaking footer", messages: "fix: repair\x1f\nBREAKING CHANGE: contract\x1e", want: bumpMajor, found: true},
		{name: "breaking footer on any type", messages: "docs: document\x1f\nBREAKING CHANGE: contract\x1e", want: bumpMajor, found: true},
		{name: "feature wins", messages: "fix: repair\x1f\x1efeat: add\x1f\x1e", want: bumpMinor, found: true},
		{name: "patch", messages: "perf: faster\x1f\x1e", want: bumpPatch, found: true},
		{name: "unrelated", messages: "docs: update\x1f\x1e", found: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, found := conventionalBump(test.messages)
			if got != test.want || found != test.found {
				t.Fatalf("conventionalBump() = %s, %t; want %s, %t", got, found, test.want, test.found)
			}
		})
	}
}

func TestParseStableVersionRejectsNonStableValues(t *testing.T) {
	for _, value := range []string{"", "v1.2.3", "1.2", "01.2.3", "1.2.3-rc.1", "1.2.3+build"} {
		if _, err := parseStableVersion(value); err == nil {
			t.Errorf("parseStableVersion(%q) unexpectedly succeeded", value)
		}
	}
}

func TestWriteVersionAtomic(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "ycy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeVersionAtomic(root, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, versionFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "1.2.3\n" {
		t.Fatalf("VERSION = %q, want newline-terminated version", contents)
	}
}

func TestRunReleaseCommitsVersionBeforeTagging(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "ycy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, versionFile), []byte("0.0.69\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := newReleaseGit(root, "feat: add feature\x1f\x1e")
	prompt := &fakePrompt{selection: "next", confirmed: true}
	commands := &fakeCommands{}
	var diagnostics bytes.Buffer
	options := releaseOptions{
		Root:        root,
		Git:         git,
		Commands:    commands,
		Prompt:      prompt,
		LookPath:    func(string) (string, error) { return "/usr/bin/actionlint", nil },
		Diagnostics: &diagnostics,
		Output:      io.Discard,
	}
	if err := runRelease(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, versionFile))
	if err != nil || string(contents) != "0.0.70\n" {
		t.Fatalf("VERSION = %q, %v", contents, err)
	}
	joined := strings.Join(git.calls, "\n")
	commit := "-C " + root + " commit -m chore(release): v0.0.70"
	mainPush := "-C " + root + " push origin HEAD:main"
	tag := "-C " + root + " tag -a v0.0.70 -m chore: release v0.0.70"
	tagPush := "-C " + root + " push origin refs/tags/v0.0.70"
	for _, expected := range []string{commit, mainPush, tag, tagPush} {
		if !strings.Contains(joined, expected) {
			t.Errorf("git calls do not contain %q:\n%s", expected, joined)
		}
	}
	if strings.Index(joined, commit) > strings.Index(joined, mainPush) || strings.Index(joined, mainPush) > strings.Index(joined, tag) || strings.Index(joined, tag) > strings.Index(joined, tagPush) {
		t.Fatalf("mutation order is incorrect:\n%s", joined)
	}
	if len(commands.calls) != 2 || !strings.HasSuffix(commands.calls[0], "/make check") || !strings.Contains(commands.calls[1], "/actionlint .github/workflows/release.yml .github/workflows/docker.yml") {
		t.Fatalf("external command calls = %#v", commands.calls)
	}
}

func TestRunReleaseDryRunDoesNotMutate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "ycy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, versionFile), []byte("0.0.69\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := newReleaseGit(root, "fix: repair\x1f\x1e")
	commands := &fakeCommands{}
	options := releaseOptions{
		Root:        root,
		Git:         git,
		Commands:    commands,
		Prompt:      &fakePrompt{selection: "next", confirmed: true},
		LookPath:    func(string) (string, error) { return "/usr/bin/actionlint", nil },
		Diagnostics: io.Discard,
		Output:      io.Discard,
		DryRun:      true,
	}
	if err := runRelease(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, versionFile))
	if err != nil || string(contents) != "0.0.69\n" {
		t.Fatalf("dry-run VERSION = %q, %v", contents, err)
	}
	for _, call := range git.calls {
		if strings.Contains(call, " commit ") || strings.Contains(call, " tag -a ") || strings.Contains(call, " push ") || strings.Contains(call, " add ") {
			t.Fatalf("dry-run mutation call = %q", call)
		}
	}
}

type fakeGit struct {
	outputs map[string]gitprocess.Output
	calls   []string
}

func (git *fakeGit) Run(_ context.Context, arguments []string) (gitprocess.Output, error) {
	call := strings.Join(arguments, " ")
	git.calls = append(git.calls, call)
	if output, ok := git.outputs[call]; ok {
		return output, nil
	}
	return gitprocess.Output{}, errors.New("unexpected git call: " + call)
}

type fakeCommands struct {
	calls []string
	err   error
}

func (commands *fakeCommands) Run(_ context.Context, directory, name string, args []string, _, _ io.Writer) error {
	commands.calls = append(commands.calls, directory+"/"+name+" "+strings.Join(args, " "))
	return commands.err
}

type fakePrompt struct {
	selection string
	confirmed bool
}

func (prompt *fakePrompt) Select(context.Context, string, string, []promptOption) (string, error) {
	return prompt.selection, nil
}

func (prompt *fakePrompt) Confirm(context.Context, string, string) (bool, error) {
	return prompt.confirmed, nil
}

func (prompt *fakePrompt) Close() error { return nil }

func newReleaseGit(root, logOutput string) *fakeGit {
	prefix := "-C " + root + " "
	outputs := map[string]gitprocess.Output{
		prefix + "symbolic-ref --quiet --short HEAD":            {Stdout: []byte("main\n")},
		prefix + "status --porcelain --untracked-files=all":     {},
		prefix + "pull --ff-only origin main":                   {},
		prefix + "cat-file -t v0.0.69":                          {Stdout: []byte("tag\n")},
		prefix + "tag --list v*":                                {Stdout: []byte("v0.0.69\n")},
		prefix + "ls-remote --refs origin refs/tags/v*":         {Stdout: []byte("abc\trefs/tags/v0.0.69\n")},
		prefix + "log --format=%s%x1f%b%x1e v0.0.69..HEAD":      {Stdout: []byte(logOutput)},
		prefix + "rev-parse --verify --quiet refs/tags/v0.0.70": {ExitCode: 1},
		prefix + "ls-remote --refs origin refs/tags/v0.0.70":    {},
		prefix + "rev-parse HEAD":                               {Stdout: []byte("head\n")},
		prefix + "add -- cmd/ycy/VERSION":                       {},
		prefix + "commit -m chore(release): v0.0.70":            {},
		prefix + "push origin HEAD:main":                        {},
		prefix + "tag -a v0.0.70 -m chore: release v0.0.70":     {},
		prefix + "push origin refs/tags/v0.0.70":                {},
	}
	return &fakeGit{outputs: outputs}
}
