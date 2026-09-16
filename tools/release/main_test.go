package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestReleaseConsoleDescriptorDeclaresEveryRichForm(t *testing.T) {
	descriptor := releaseConsoleDescriptor()
	if descriptor.Command != "make release" || descriptor.Target != "repository release" {
		t.Fatalf("descriptor identity = %#v", descriptor)
	}
	if len(descriptor.FormCatalog) != 2 {
		t.Fatalf("form catalog length = %d, want 2", len(descriptor.FormCatalog))
	}
	for index, want := range []terminal.ConsoleFormStep{
		{ID: "release-version", Name: "Release version"},
		{ID: "release-confirm", Name: "Release confirmation"},
	} {
		got := descriptor.FormCatalog[index]
		if got.ID != want.ID || got.Name != want.Name {
			t.Errorf("form %d = %#v, want ID/name %q/%q", index, got, want.ID, want.Name)
		}
	}
}

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

func TestWriteReleaseErrorFormatsDirtyWorkingTree(t *testing.T) {
	var output bytes.Buffer
	writeReleaseError(&output, &dirtyWorkingTreeError{status: " M tools/release/main.go\n?? tools/release/new.go\nD  scripts/release"})
	want := "\nRelease blocked\n  Commit, stash, or discard these changes before releasing:\n  modified   tools/release/main.go\n  untracked  tools/release/new.go\n  deleted    scripts/release\n\n"
	if output.String() != want {
		t.Fatalf("error output = %q, want %q", output.String(), want)
	}
}

func TestPrepareReleaseReportsProgressForEveryPreflightStep(t *testing.T) {
	root := writeReleaseVersion(t)
	var diagnostics bytes.Buffer
	work := &fakeReleaseWork{}
	if _, err := prepareRelease(context.Background(), normalizeReleaseOptions(releaseOptions{
		Root:        root,
		Git:         newReleaseGit(root, "docs: update\x1f\x1e"),
		Commands:    &fakeCommands{},
		Prompt:      &fakePrompt{},
		Diagnostics: &diagnostics,
		Output:      io.Discard,
	}), work); err != nil {
		t.Fatal(err)
	}
	want := []terminal.OperationPhase{
		{ID: releaseInspectWorkspacePhaseID, State: terminal.PhaseActive, Detail: "Checking branch and working tree"},
		{ID: releaseInspectWorkspacePhaseID, State: terminal.PhaseCompleted, Detail: "main branch and working tree are ready"},
		{ID: releaseSyncMainPhaseID, State: terminal.PhaseActive, Detail: "Fetching origin/main"},
		{ID: releaseSyncMainPhaseID, State: terminal.PhaseCompleted, Detail: "main is up to date"},
		{ID: releaseValidateBaselinePhaseID, State: terminal.PhaseActive, Detail: "Reading VERSION and release tags"},
		{ID: releaseValidateBaselinePhaseID, State: terminal.PhaseCompleted, Detail: "v0.0.69 is the release baseline"},
		{ID: releaseSuggestVersionPhaseID, State: terminal.PhaseActive, Detail: "Scanning commits since v0.0.69"},
		{ID: releaseSuggestVersionPhaseID, State: terminal.PhaseCompleted, Detail: "No conventional release suggestion"},
	}
	if got := work.updates; !reflect.DeepEqual(got, want) {
		t.Fatalf("preflight updates = %#v, want %#v", got, want)
	}
	if got := diagnostics.String(); got != "" {
		t.Fatalf("preflight diagnostics = %q, want no duplicate plain output", got)
	}
}

func TestLazyPrompterStartsTheTerminalForPreflight(t *testing.T) {
	factoryCalls := 0
	inner := &fakePrompt{selection: "next", confirmed: true}
	prompt := &lazyPrompter{new: func() (releasePrompter, error) {
		factoryCalls++
		return inner, nil
	}}
	if err := prompt.Close(); err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 0 || inner.closed {
		t.Fatalf("preflight close initialized prompt: factory=%d closed=%t", factoryCalls, inner.closed)
	}
	work, err := prompt.StartPreflight(releasePreflightWorkCatalog())
	if err != nil || work == nil || factoryCalls != 1 || !inner.preflightStarted {
		t.Fatalf("StartPreflight() = (%T, %v); factory=%d, started=%t", work, err, factoryCalls, inner.preflightStarted)
	}
	if err := prompt.Close(); err != nil || !inner.closed {
		t.Fatalf("Close() = %v, closed=%t", err, inner.closed)
	}
}

func TestRunReleaseClosesBeforeReturningPreflightFailure(t *testing.T) {
	root := t.TempDir()
	git := &fakeGit{outputs: map[string]gitprocess.Output{
		"-C " + root + " symbolic-ref --quiet --short HEAD":        {Stdout: []byte("main\n")},
		"-C " + root + " status --porcelain --untracked-files=all": {Stdout: []byte(" M tools/release/main.go\n")},
	}}
	prompt := &fakePrompt{}
	err := runRelease(context.Background(), releaseOptions{
		Root:     root,
		Git:      git,
		Commands: &fakeCommands{},
		Prompt:   prompt,
	})
	if err == nil || !strings.Contains(err.Error(), "working tree is not clean") {
		t.Fatalf("runRelease() error = %v", err)
	}
	if !prompt.preflight.closed {
		t.Fatal("runRelease did not close the release preflight before returning")
	}
	if got, want := prompt.preflight.updates, []terminal.OperationPhase{
		{ID: releaseInspectWorkspacePhaseID, State: terminal.PhaseActive, Detail: "Checking branch and working tree"},
		{ID: releaseInspectWorkspacePhaseID, State: terminal.PhaseFailed, Detail: "Preflight stopped"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failed preflight updates = %#v, want %#v", got, want)
	}
	if !prompt.closed {
		t.Fatal("runRelease did not close the terminal prompt before returning")
	}
}

func TestReleaseRestoresRichTerminalBeforeRunningChecks(t *testing.T) {
	const helperEnvironment = "YCY_RELEASE_RICH_HANDOFF_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runReleaseRichHandoffHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestReleaseRestoresRichTerminalBeforeRunningChecks$")
	command.Env = append(releasePTYEnvironment(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start release PTY helper: %v", err)
	}
	defer process.Close()

	output := newReleasePTYBuffer("Select release version")
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, process.Terminal())
		close(readDone)
	}()
	respondToReleaseTerminalQueries(t, process, output)
	waitForReleasePTYText(t, output, "Fast-forward main")
	waitForReleasePTYText(t, output, "Validate release baseline")
	waitForReleasePTYText(t, output, "Select release version")
	writeReleasePTYInput(t, process, "\r")
	waitForReleasePTYText(t, output, "Create release?")
	writeReleasePTYInput(t, process, "y")

	if err := process.Wait(); err != nil {
		t.Fatalf("wait release PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close release PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading release PTY output: %q", output.String())
	}

	text := output.String()
	for _, marker := range []string{"Fast-forward main", "Validate release baseline"} {
		if at := strings.Index(text, marker); at < 0 || at > strings.Index(text, "Select release version") {
			t.Fatalf("preflight phase %q did not render before release selection: %q", marker, text)
		}
	}
	exit := releaseAlternateScreenExit(text)
	for _, marker := range []string{
		"Release checks",
		"Running make check...",
		"CHECK_STDOUT",
		"CHECK_STDERR",
		"Validating GitHub Actions workflows...",
		"ACTIONLINT_STDOUT",
		"ACTIONLINT_STDERR",
	} {
		at := strings.LastIndex(text, marker)
		if exit < 0 || at < 0 || exit > at {
			t.Fatalf("Rich terminal was not restored before %q: %q", marker, text)
		}
	}
}

func TestRunReleaseStopsBeforeChecksWhenPromptCannotClose(t *testing.T) {
	root := writeReleaseVersion(t)
	prompt := &fakePrompt{selection: "next", confirmed: true, closeErr: errors.New("terminal restore failed")}
	commands := &fakeCommands{}
	err := runRelease(context.Background(), releaseOptions{
		Root:        root,
		Git:         newReleaseGit(root, "fix: repair\x1f\x1e"),
		Commands:    commands,
		Prompt:      prompt,
		LookPath:    func(string) (string, error) { return "/usr/bin/actionlint", nil },
		Diagnostics: io.Discard,
		Output:      io.Discard,
		DryRun:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "restore interactive terminal") {
		t.Fatalf("runRelease() error = %v", err)
	}
	if len(commands.calls) != 0 {
		t.Fatalf("checks ran after terminal close failed: %#v", commands.calls)
	}
}

func TestRunReleaseClosesPromptBeforeStartingChecks(t *testing.T) {
	root := writeReleaseVersion(t)
	prompt := &fakePrompt{selection: "next", confirmed: true}
	commands := &promptAwareCommands{prompt: prompt}
	if err := runRelease(context.Background(), releaseOptions{
		Root:        root,
		Git:         newReleaseGit(root, "fix: repair\x1f\x1e"),
		Commands:    commands,
		Prompt:      prompt,
		LookPath:    func(string) (string, error) { return "/usr/bin/actionlint", nil },
		Diagnostics: io.Discard,
		Output:      io.Discard,
		DryRun:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(commands.calls) != 2 {
		t.Fatalf("checks = %#v, want make check and actionlint", commands.calls)
	}
}

func TestRunReleaseStartsPreflightBeforeGit(t *testing.T) {
	root := writeReleaseVersion(t)
	prompt := &fakePrompt{selection: "next", confirmed: true}
	git := &preflightAwareGit{fakeGit: newReleaseGit(root, "fix: repair\x1f\x1e"), prompt: prompt}
	if err := runRelease(context.Background(), releaseOptions{
		Root:        root,
		Git:         git,
		Commands:    &fakeCommands{},
		Prompt:      prompt,
		LookPath:    func(string) (string, error) { return "/usr/bin/actionlint", nil },
		Diagnostics: io.Discard,
		Output:      io.Discard,
		DryRun:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := prompt.preflightCatalog, releasePreflightWorkCatalog(); !reflect.DeepEqual(got, want) {
		t.Fatalf("preflight catalog = %#v, want %#v", got, want)
	}
}

func TestRunReleaseKeepsChildStdoutAndStderrSeparate(t *testing.T) {
	root := writeReleaseVersion(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runRelease(context.Background(), releaseOptions{
		Root:        root,
		Git:         newReleaseGit(root, "fix: repair\x1f\x1e"),
		Commands:    releasePTYCommands{},
		Prompt:      &fakePrompt{selection: "next", confirmed: true},
		LookPath:    func(string) (string, error) { return "/usr/bin/actionlint", nil },
		Diagnostics: &stderr,
		Output:      &stdout,
		DryRun:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "CHECK_STDOUT\nACTIONLINT_STDOUT\n" {
		t.Fatalf("child stdout = %q", got)
	}
	for _, marker := range []string{"CHECK_STDERR", "ACTIONLINT_STDERR", "Release checks", "Release ready"} {
		if !strings.Contains(stderr.String(), marker) {
			t.Errorf("diagnostics missing %q: %q", marker, stderr.String())
		}
	}
	for _, marker := range []string{"CHECK_STDOUT", "ACTIONLINT_STDOUT"} {
		if strings.Contains(stderr.String(), marker) {
			t.Errorf("diagnostics unexpectedly contain child stdout %q: %q", marker, stderr.String())
		}
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

type releasePTYCommands struct{}

func (releasePTYCommands) Run(_ context.Context, _ string, name string, _ []string, stdout, stderr io.Writer) error {
	switch name {
	case "make":
		_, _ = io.WriteString(stdout, "CHECK_STDOUT\n")
		_, _ = io.WriteString(stderr, "CHECK_STDERR\n")
	case "actionlint":
		_, _ = io.WriteString(stdout, "ACTIONLINT_STDOUT\n")
		_, _ = io.WriteString(stderr, "ACTIONLINT_STDERR\n")
	default:
		return errors.New("unexpected command: " + name)
	}
	return nil
}

type promptAwareCommands struct {
	prompt *fakePrompt
	calls  []string
}

func (commands *promptAwareCommands) Run(_ context.Context, _ string, name string, _ []string, _, _ io.Writer) error {
	if !commands.prompt.closed {
		return errors.New("check started before interactive prompt closed")
	}
	commands.calls = append(commands.calls, name)
	return nil
}

type preflightAwareGit struct {
	*fakeGit
	prompt *fakePrompt
}

func (git *preflightAwareGit) Run(ctx context.Context, arguments []string) (gitprocess.Output, error) {
	if !git.prompt.preflightStarted {
		return gitprocess.Output{}, errors.New("git command started before release preflight")
	}
	return git.fakeGit.Run(ctx, arguments)
}

type fakePrompt struct {
	selection        string
	confirmed        bool
	opened           bool
	preflightStarted bool
	preflightCatalog terminal.WorkCatalog
	preflight        *fakeReleaseWork
	preflightErr     error
	closed           bool
	closeErr         error
}

func (prompt *fakePrompt) StartPreflight(catalog terminal.WorkCatalog) (terminal.WorkSession, error) {
	prompt.preflightStarted = true
	prompt.preflightCatalog = catalog
	if prompt.preflightErr != nil {
		return nil, prompt.preflightErr
	}
	if prompt.preflight == nil {
		prompt.preflight = &fakeReleaseWork{}
	}
	return prompt.preflight, nil
}

func (prompt *fakePrompt) Select(context.Context, string, string, []promptOption) (string, error) {
	prompt.opened = true
	return prompt.selection, nil
}

func (prompt *fakePrompt) Confirm(context.Context, string, string) (bool, error) {
	return prompt.confirmed, nil
}

func (prompt *fakePrompt) Close() error {
	prompt.closed = true
	return prompt.closeErr
}

type fakeReleaseWork struct {
	updates   []terminal.OperationPhase
	updateErr error
	closed    bool
	closeErr  error
}

func (work *fakeReleaseWork) Update(phase terminal.OperationPhase) error {
	work.updates = append(work.updates, phase)
	return work.updateErr
}

func (work *fakeReleaseWork) Close() error {
	work.closed = true
	return work.closeErr
}

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

func writeReleaseVersion(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "ycy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, versionFile), []byte("0.0.69\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func runReleaseRichHandoffHelper(t *testing.T) {
	t.Helper()
	root := writeReleaseVersion(t)
	prompt, err := newTerminalPrompter(os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := runRelease(context.Background(), releaseOptions{
		Root:        root,
		Git:         delayedReleaseGit{fakeGit: newReleaseGit(root, "fix: repair\x1f\x1e")},
		Commands:    releasePTYCommands{},
		Prompt:      prompt,
		LookPath:    func(string) (string, error) { return "/usr/bin/actionlint", nil },
		Diagnostics: os.Stderr,
		Output:      os.Stdout,
	}); err != nil {
		t.Fatal(err)
	}
}

type delayedReleaseGit struct {
	*fakeGit
}

func (git delayedReleaseGit) Run(ctx context.Context, arguments []string) (gitprocess.Output, error) {
	if strings.HasSuffix(strings.Join(arguments, " "), " pull --ff-only origin main") {
		time.Sleep(150 * time.Millisecond)
	}
	return git.fakeGit.Run(ctx, arguments)
}

type releasePTYBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	needle    string
	prompt    chan struct{}
	query     chan struct{}
	once      sync.Once
	queryOnce sync.Once
}

func newReleasePTYBuffer(needle string) *releasePTYBuffer {
	return &releasePTYBuffer{needle: needle, prompt: make(chan struct{}), query: make(chan struct{})}
}

func (buffer *releasePTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	count, err := buffer.buffer.Write(value)
	text := buffer.buffer.String()
	if strings.Contains(text, buffer.needle) {
		buffer.once.Do(func() { close(buffer.prompt) })
	}
	if strings.Contains(text, "\x1b]11;?") || strings.Contains(text, "\x1b[6n") {
		buffer.queryOnce.Do(func() { close(buffer.query) })
	}
	return count, err
}

func (buffer *releasePTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func respondToReleaseTerminalQueries(t *testing.T, process *terminaltest.PTYProcess, output *releasePTYBuffer) {
	t.Helper()
	select {
	case <-output.query:
		writeReleasePTYInput(t, process, "\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[1;1R")
	case <-output.prompt:
		// The terminal may have enough cached capability state to render immediately.
	}
}

func waitForReleasePTYText(t *testing.T, output *releasePTYBuffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(output.String(), needle) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("release PTY output did not contain %q: %q", needle, output.String())
}

func writeReleasePTYInput(t *testing.T, process *terminaltest.PTYProcess, value string) {
	t.Helper()
	if _, err := io.WriteString(process.Terminal(), value); err != nil {
		t.Fatalf("write release PTY input: %v", err)
	}
}

func releaseAlternateScreenExit(output string) int {
	exit := -1
	for _, code := range []string{"\x1b[?1049l", "\x1b[?1047l", "\x1b[?47l"} {
		exit = max(exit, strings.LastIndex(output, code))
	}
	return exit
}

func releasePTYEnvironment() []string {
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
