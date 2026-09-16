package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"golang.org/x/term"
)

const versionFile = "cmd/ycy/VERSION"

type commandRunner interface {
	Run(context.Context, string, string, []string, io.Writer, io.Writer) error
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, directory, name string, args []string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

type gitRunner interface {
	Run(context.Context, []string) (gitprocess.Output, error)
}

type releasePrompter interface {
	Select(context.Context, string, string, []promptOption) (string, error)
	Confirm(context.Context, string, string) (bool, error)
	Close() error
}

type promptOption struct {
	Value       string
	Label       string
	Description string
}

type terminalPrompter struct {
	run terminal.ExperienceRun
}

type lazyPrompter struct {
	new   func() (releasePrompter, error)
	inner releasePrompter
}

func (prompter *lazyPrompter) Select(ctx context.Context, message, description string, options []promptOption) (string, error) {
	inner, err := prompter.ensure()
	if err != nil {
		return "", err
	}
	return inner.Select(ctx, message, description, options)
}

func (prompter *lazyPrompter) Confirm(ctx context.Context, message, description string) (bool, error) {
	inner, err := prompter.ensure()
	if err != nil {
		return false, err
	}
	return inner.Confirm(ctx, message, description)
}

func (prompter *lazyPrompter) Close() error {
	if prompter.inner == nil {
		return nil
	}
	return prompter.inner.Close()
}

func (prompter *lazyPrompter) ensure() (releasePrompter, error) {
	if prompter.inner != nil {
		return prompter.inner, nil
	}
	inner, err := prompter.new()
	if err != nil {
		return nil, err
	}
	prompter.inner = inner
	return inner, nil
}

func newTerminalPrompter(input io.Reader, output, diagnostics *os.File) (*terminalPrompter, error) {
	inputFile, inputOK := input.(*os.File)
	caps := terminal.Classify(terminal.Facts{
		Stdin:     terminal.StreamFacts{Terminal: inputOK && term.IsTerminal(int(inputFile.Fd()))},
		Stdout:    terminal.StreamFacts{Terminal: output != nil && term.IsTerminal(int(output.Fd()))},
		Stderr:    terminal.StreamFacts{Terminal: diagnostics != nil && term.IsTerminal(int(diagnostics.Fd()))},
		LookupEnv: os.LookupEnv,
	})
	if caps.Interaction == terminal.Automation {
		return nil, errors.New("release requires an interactive terminal; version selection is not available in automation")
	}
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: caps,
		Input:        input,
		Output:       output,
		Diagnostics:  diagnostics,
	})
	run, err := experience.OpenConsole(context.Background(), releaseConsoleDescriptor())
	if err != nil {
		return nil, err
	}
	return &terminalPrompter{run: run}, nil
}

func releaseConsoleDescriptor() terminal.ConsoleDescriptor {
	return terminal.ConsoleDescriptor{
		Command: "make release",
		Target:  "repository release",
		FormCatalog: []terminal.ConsoleFormStep{
			{ID: "release-version", Name: "Release version", Detail: "Select the next stable version"},
			{ID: "release-confirm", Name: "Release confirmation", Detail: "Confirm checks, commit, and tag push"},
		},
	}
}

func (prompter *terminalPrompter) Select(ctx context.Context, message, description string, options []promptOption) (string, error) {
	choices := make([]terminal.InteractionOption, 0, len(options))
	for _, option := range options {
		choices = append(choices, terminal.InteractionOption{Value: option.Value, Label: option.Label, Description: option.Description})
	}
	answer, err := prompter.run.Ask(terminal.InteractionRequest{
		Kind:            terminal.InteractionSelect,
		Message:         message,
		Description:     description,
		Options:         choices,
		ConsoleStepID:   "release-version",
		HasDefault:      true,
		Default:         terminal.InteractionAnswer{Value: "next"},
		TranscriptLabel: "Release version",
	})
	if err != nil {
		return "", err
	}
	return answer.Value, nil
}

func (prompter *terminalPrompter) Confirm(ctx context.Context, message, description string) (bool, error) {
	answer, err := prompter.run.Ask(terminal.InteractionRequest{
		Kind:            terminal.InteractionConfirm,
		Message:         message,
		Description:     description,
		ConsoleStepID:   "release-confirm",
		HasDefault:      true,
		Default:         terminal.InteractionAnswer{Confirmed: false},
		TranscriptLabel: "Release confirmation",
	})
	return answer.Confirmed, err
}

func (prompter *terminalPrompter) Close() error { return prompter.run.Close() }

type releaseOptions struct {
	Root        string
	Git         gitRunner
	Commands    commandRunner
	Prompt      releasePrompter
	LookPath    func(string) (string, error)
	Diagnostics io.Writer
	Output      io.Writer
	DryRun      bool
}

func main() {
	os.Exit(runCLI(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runCLI(ctx context.Context, args []string, input *os.File, output, diagnostics io.Writer) int {
	if len(args) > 0 {
		if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
			fmt.Fprintln(diagnostics, "Usage: make release")
			fmt.Fprintln(diagnostics, "Set DRY_RUN=1 to run checks without updating VERSION, committing, or pushing.")
			return 0
		}
		fmt.Fprintln(diagnostics, "release: target version is selected interactively; arguments are not supported")
		return 2
	}
	dryRun := os.Getenv("DRY_RUN")
	if dryRun != "" && dryRun != "0" && dryRun != "1" {
		fmt.Fprintln(diagnostics, "release: DRY_RUN must be 0 or 1")
		return 1
	}
	options := releaseOptions{
		Git:         &gitprocess.Runner{},
		Commands:    osCommandRunner{},
		LookPath:    exec.LookPath,
		Diagnostics: diagnostics,
		Output:      output,
		DryRun:      dryRun == "1",
	}
	prompter := &lazyPrompter{new: func() (releasePrompter, error) {
		return newTerminalPrompter(input, os.Stdout, os.Stderr)
	}}
	if err := runWithPrompter(ctx, options, prompter); err != nil {
		writeReleaseError(diagnostics, err)
		return 1
	}
	return 0
}

// runWithPrompter always restores the terminal before runCLI reports a failure.
// A Rich console otherwise clears an error written to its diagnostic stream.
func runWithPrompter(ctx context.Context, options releaseOptions, prompter releasePrompter) error {
	options.Prompt = prompter
	releaseErr := runRelease(ctx, options)
	closeErr := prompter.Close()
	return errors.Join(releaseErr, closeErr)
}

type dirtyWorkingTreeError struct {
	status string
}

func (err *dirtyWorkingTreeError) Error() string {
	return "working tree is not clean"
}

func writeReleaseError(writer io.Writer, err error) {
	var dirty *dirtyWorkingTreeError
	if errors.As(err, &dirty) {
		fmt.Fprintln(writer, "")
		fmt.Fprintln(writer, "Release blocked")
		fmt.Fprintln(writer, "  Commit, stash, or discard these changes before releasing:")
		for _, line := range strings.Split(strings.TrimRight(dirty.status, "\r\n"), "\n") {
			fmt.Fprintf(writer, "  %-10s %s\n", releaseChangeKind(line), releaseChangePath(line))
		}
		fmt.Fprintln(writer, "")
		return
	}
	fmt.Fprintln(writer, "")
	fmt.Fprintln(writer, "Release stopped")
	fmt.Fprintf(writer, "  %v\n\n", err)
}

func releaseChangeKind(status string) string {
	if len(status) < 2 {
		return "changed"
	}
	switch {
	case status[:2] == "??":
		return "untracked"
	case strings.Contains(status[:2], "D"):
		return "deleted"
	case strings.Contains(status[:2], "R"):
		return "renamed"
	case strings.Contains(status[:2], "A"):
		return "added"
	default:
		return "modified"
	}
}

func releaseChangePath(status string) string {
	if len(status) < 4 {
		return strings.TrimSpace(status)
	}
	return strings.TrimSpace(status[3:])
}

func runRelease(ctx context.Context, options releaseOptions) error {
	if options.Git == nil || options.Commands == nil || options.Prompt == nil {
		return errors.New("release dependencies are incomplete")
	}
	if options.Diagnostics == nil {
		options.Diagnostics = io.Discard
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	root := options.Root
	if root == "" {
		output, err := options.Git.Run(ctx, []string{"rev-parse", "--show-toplevel"})
		if err != nil || output.ExitCode != 0 {
			return gitFailure("resolve repository root", output, err)
		}
		root = strings.TrimSpace(string(output.Stdout))
	}
	if root == "" {
		return errors.New("repository root is empty")
	}
	options.Root = root
	if err := requireGitValue(ctx, options, []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, "main", "release must start from the main branch"); err != nil {
		return err
	}
	if err := ensureClean(ctx, options); err != nil {
		return err
	}
	fmt.Fprintln(options.Diagnostics, "release: fast-forwarding main from origin...")
	if err := runGitCommand(ctx, options, []string{"pull", "--ff-only", "origin", "main"}); err != nil {
		return err
	}
	if err := ensureClean(ctx, options); err != nil {
		return err
	}
	currentText, err := os.ReadFile(filepath.Join(root, versionFile))
	if err != nil {
		return fmt.Errorf("read %s: %w", versionFile, err)
	}
	currentValue := strings.TrimSpace(string(currentText))
	current, err := parseStableVersion(currentValue)
	if err != nil {
		return fmt.Errorf("%s: %w", versionFile, err)
	}
	currentTag := "v" + current.String()
	if err := validateCurrentTag(ctx, options, currentTag, current); err != nil {
		return err
	}
	conventional, err := resolveConventionalBump(ctx, options, currentTag)
	if err != nil {
		return err
	}
	versionCandidates := candidates(current, conventional)
	selected, err := options.Prompt.Select(ctx, "Select release version", "Current version "+current.String(), candidateOptions(versionCandidates))
	if err != nil {
		return fmt.Errorf("select release version: %w", err)
	}
	target, ok := candidateByKind(versionCandidates, bumpKind(selected))
	if !ok {
		return fmt.Errorf("unknown release selection: %s", selected)
	}
	targetTag := "v" + target.Version
	if err := ensureTargetTagUnused(ctx, options, targetTag); err != nil {
		return err
	}
	baseHead, err := gitValue(ctx, options, []string{"rev-parse", "HEAD"})
	if err != nil {
		return err
	}
	summary := fmt.Sprintf("Current version: %s\nTarget version: %s\nTag: %s\nChecks: make check and actionlint\nMutation: update %s, commit, push main, create and push annotated tag", current.String(), target.Version, targetTag, versionFile)
	confirmed, err := options.Prompt.Confirm(ctx, "Create release?", summary)
	if err != nil {
		return fmt.Errorf("confirm release: %w", err)
	}
	if !confirmed {
		return errors.New("release cancelled")
	}
	fmt.Fprintln(options.Diagnostics, "release: running make check...")
	if err := options.Commands.Run(ctx, root, "make", []string{"check"}, options.Output, options.Output); err != nil {
		return fmt.Errorf("make check failed: %w", err)
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if _, err := options.LookPath("actionlint"); err != nil {
		return errors.New("actionlint is required; install it before releasing")
	}
	fmt.Fprintln(options.Diagnostics, "release: validating GitHub Actions workflows...")
	if err := options.Commands.Run(ctx, root, "actionlint", []string{".github/workflows/release.yml", ".github/workflows/docker.yml"}, options.Output, options.Output); err != nil {
		return fmt.Errorf("actionlint failed: %w", err)
	}
	if err := ensureClean(ctx, options); err != nil {
		return err
	}
	if head, err := gitValue(ctx, options, []string{"rev-parse", "HEAD"}); err != nil || head != baseHead {
		if err != nil {
			return err
		}
		return errors.New("HEAD changed while release checks were running; aborting")
	}
	if options.DryRun {
		fmt.Fprintln(options.Diagnostics, "release: dry run complete; VERSION, commit, and tag were not changed")
		return nil
	}
	if err := writeVersionAtomic(root, target.Version); err != nil {
		return err
	}
	if err := runGitCommand(ctx, options, []string{"add", "--", versionFile}); err != nil {
		return err
	}
	if err := runGitCommand(ctx, options, []string{"commit", "-m", "chore(release): " + targetTag}); err != nil {
		return fmt.Errorf("create release commit: %w", err)
	}
	fmt.Fprintln(options.Diagnostics, "release: pushing release commit to origin/main...")
	if err := runGitCommand(ctx, options, []string{"push", "origin", "HEAD:main"}); err != nil {
		return fmt.Errorf("release commit was created locally but main push failed: %w", err)
	}
	if err := ensureTargetTagUnused(ctx, options, targetTag); err != nil {
		return err
	}
	fmt.Fprintf(options.Diagnostics, "release: creating annotated tag %s...\n", targetTag)
	if err := runGitCommand(ctx, options, []string{"tag", "-a", targetTag, "-m", "chore: release " + targetTag}); err != nil {
		return err
	}
	fmt.Fprintf(options.Diagnostics, "release: pushing only refs/tags/%s...\n", targetTag)
	if err := runGitCommand(ctx, options, []string{"push", "origin", "refs/tags/" + targetTag}); err != nil {
		return fmt.Errorf("tag push failed; local tag %s was kept: %w", targetTag, err)
	}
	fmt.Fprintf(options.Diagnostics, "release: tag %s pushed; GitHub Actions will run the release workflow\n", targetTag)
	return nil
}

func candidateOptions(candidates []versionCandidate) []promptOption {
	options := make([]promptOption, 0, len(candidates))
	for _, candidate := range candidates {
		options = append(options, promptOption{Value: string(candidate.Kind), Label: string(candidate.Kind), Description: candidate.Version})
	}
	return options
}

func candidateByKind(candidates []versionCandidate, kind bumpKind) (versionCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.Kind == kind {
			return candidate, true
		}
	}
	return versionCandidate{}, false
}

func resolveConventionalBump(ctx context.Context, options releaseOptions, currentTag string) (*bumpKind, error) {
	output, err := options.Git.Run(ctx, []string{"-C", options.Root, "log", "--format=%s%x1f%b%x1e", currentTag + "..HEAD"})
	if err != nil || output.ExitCode != 0 {
		return nil, gitFailure("inspect Conventional Commits", output, err)
	}
	kind, found := conventionalBump(string(output.Stdout))
	if !found {
		fmt.Fprintln(options.Diagnostics, "release: conventional option unavailable; no releasable feat/fix/perf commit was found")
		return nil, nil
	}
	return &kind, nil
}

func validateCurrentTag(ctx context.Context, options releaseOptions, tag string, current stableVersion) error {
	output, err := options.Git.Run(ctx, []string{"-C", options.Root, "cat-file", "-t", tag})
	if err != nil || output.ExitCode != 0 || strings.TrimSpace(string(output.Stdout)) != "tag" {
		return fmt.Errorf("current VERSION %s must match an annotated tag %s", current.String(), tag)
	}
	output, err = options.Git.Run(ctx, []string{"-C", options.Root, "tag", "--list", "v*"})
	if err != nil || output.ExitCode != 0 {
		return gitFailure("inspect stable tags", output, err)
	}
	highest := highestStableTag(current, strings.Fields(string(output.Stdout)))
	output, err = options.Git.Run(ctx, []string{"-C", options.Root, "ls-remote", "--refs", "origin", "refs/tags/v*"})
	if err != nil || output.ExitCode != 0 {
		return gitFailure("inspect remote stable tags", output, err)
	}
	remoteTags := make([]string, 0)
	for _, line := range strings.Split(string(output.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			remoteTags = append(remoteTags, strings.TrimPrefix(fields[1], "refs/tags/"))
		}
	}
	highest = highestStableTag(highest, remoteTags)
	if compareStableVersions(current, highest) != 0 {
		return fmt.Errorf("%s %s is behind the highest stable tag %s", versionFile, current.String(), highest.String())
	}
	return nil
}

func highestStableTag(initial stableVersion, tags []string) stableVersion {
	highest := initial
	for _, raw := range tags {
		if !strings.HasPrefix(raw, "v") {
			continue
		}
		candidate, parseErr := parseStableVersion(strings.TrimPrefix(raw, "v"))
		if parseErr == nil && compareStableVersions(candidate, highest) > 0 {
			highest = candidate
		}
	}
	return highest
}

func ensureTargetTagUnused(ctx context.Context, options releaseOptions, tag string) error {
	output, err := options.Git.Run(ctx, []string{"-C", options.Root, "rev-parse", "--verify", "--quiet", "refs/tags/" + tag})
	if err != nil {
		return gitFailure("inspect local target tag", output, err)
	}
	if output.ExitCode == 0 && strings.TrimSpace(string(output.Stdout)) != "" {
		return fmt.Errorf("local tag already exists: %s", tag)
	}
	output, err = options.Git.Run(ctx, []string{"-C", options.Root, "ls-remote", "--refs", "origin", "refs/tags/" + tag})
	if err != nil || output.ExitCode != 0 {
		return gitFailure("inspect remote target tag", output, err)
	}
	if strings.TrimSpace(string(output.Stdout)) != "" {
		return fmt.Errorf("remote tag already exists: %s", tag)
	}
	return nil
}

func ensureClean(ctx context.Context, options releaseOptions) error {
	output, err := options.Git.Run(ctx, []string{"-C", options.Root, "status", "--porcelain", "--untracked-files=all"})
	if err != nil || output.ExitCode != 0 {
		return gitFailure("inspect working tree", output, err)
	}
	status := strings.TrimRight(string(output.Stdout), "\r\n")
	if status != "" {
		return &dirtyWorkingTreeError{status: status}
	}
	return nil
}

func requireGitValue(ctx context.Context, options releaseOptions, arguments []string, want, message string) error {
	value, err := gitValue(ctx, options, arguments)
	if err != nil {
		return err
	}
	if value != want {
		return errors.New(message)
	}
	return nil
}

func gitValue(ctx context.Context, options releaseOptions, arguments []string) (string, error) {
	arguments = append([]string{"-C", options.Root}, arguments...)
	output, err := options.Git.Run(ctx, arguments)
	if err != nil || output.ExitCode != 0 {
		return "", gitFailure(strings.Join(arguments[2:], " "), output, err)
	}
	return strings.TrimSpace(string(output.Stdout)), nil
}

func runGitCommand(ctx context.Context, options releaseOptions, arguments []string) error {
	output, err := options.Git.Run(ctx, append([]string{"-C", options.Root}, arguments...))
	if err != nil || output.ExitCode != 0 {
		return gitFailure(strings.Join(arguments, " "), output, err)
	}
	return nil
}

func gitFailure(action string, output gitprocess.Output, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	detail := strings.TrimSpace(string(output.Stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(output.Stdout))
	}
	if detail != "" {
		return fmt.Errorf("%s: %s", action, detail)
	}
	return fmt.Errorf("%s failed", action)
}

func writeVersionAtomic(root, version string) error {
	path := filepath.Join(root, versionFile)
	temporary, err := os.CreateTemp(filepath.Dir(path), ".VERSION-*")
	if err != nil {
		return fmt.Errorf("create temporary VERSION file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.WriteString(version + "\n"); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write VERSION file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync VERSION file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close VERSION file: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace VERSION file: %w", err)
	}
	return nil
}
