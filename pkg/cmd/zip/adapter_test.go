package zip

import (
	archivezip "archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalZipAdapterTranslatesPlanningAndPresentation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "two"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "one"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Values: []string{"**/*.html", "assets/**/*"}}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "custom archive"}},
	)
	run := experience.Open(context.Background())
	adapter := newTerminalZipAdapter(run)
	choices := []PlanningChoice{
		{Value: "one", Label: "one", Hint: "first"},
		{Value: "two", Label: "two", Hint: "second"},
	}
	packageRoot, cancelled, err := adapter.SelectPackage(SelectPackageStep{Message: "Select a package to zip:", Options: choices})
	if err != nil || cancelled || packageRoot != "two" {
		t.Fatalf("SelectPackage() = (%q, %t, %v)", packageRoot, cancelled, err)
	}
	source, cancelled, err := adapter.SelectSource(SelectSourceStep{Message: "Select a directory to zip:", Options: choices})
	if err != nil || cancelled || source != "one" {
		t.Fatalf("SelectSource() = (%q, %t, %v)", source, cancelled, err)
	}
	patterns, cancelled, err := adapter.SelectGlob(SelectGlobStep{
		Message:       "Select file patterns to include in the zip:",
		Options:       []PlanningChoice{{Value: "**/*", Label: "All"}, {Value: "**/*.html", Label: "HTML"}, {Value: "assets/**/*", Label: "Assets"}},
		InitialValues: []string{"**/*"},
	})
	if err != nil || cancelled || !reflect.DeepEqual(patterns, []string{"**/*.html", "assets/**/*"}) {
		t.Fatalf("SelectGlob() = (%#v, %t, %v)", patterns, cancelled, err)
	}
	filename, cancelled, err := adapter.EditOutputFile(EditOutputFileStep{Message: "Enter name", InitialValue: "default"})
	if err != nil || cancelled || filename != "custom archive" {
		t.Fatalf("EditOutputFile() = (%q, %t, %v)", filename, cancelled, err)
	}
	adapter.Intro()
	adapter.Note(PlanningNote{Title: "Zip plan", Lines: []string{"Source: dist", "Output: archive.zip"}})
	adapter.Progress("Collecting files...")
	adapter.Cancel("Operation cancelled.")
	adapter.Outro("Done!")
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 10 || operations[0].Kind != terminaltest.AskOperation || operations[4].Kind != terminaltest.NoticeOperation || operations[7].Kind != terminaltest.ResultOperation || operations[8].Kind != terminaltest.ResultOperation || operations[9].Kind != terminaltest.CloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	packageRequest := operations[0].Value.(terminalexperience.InteractionRequest)
	if packageRequest.Kind != terminalexperience.InteractionSelect || packageRequest.Message != "Select a package to zip:" || packageRequest.ConsoleStepID != zipPackageFormID || packageRequest.TranscriptLabel != "Workspace package" || !packageRequest.HasDefault || packageRequest.Default.Value != "one" || !reflect.DeepEqual(packageRequest.Options, []terminalexperience.InteractionOption{{Label: "one", Value: "one", Description: "first"}, {Label: "two", Value: "two", Description: "second"}}) || !reflect.DeepEqual(packageRequest.CancelValues, []string{"q", "quit", "cancel"}) || packageRequest.PlainLead != "Select a package to zip:" || packageRequest.PlainPrompt != "> " || packageRequest.ParsePlain == nil || packageRequest.TranscriptProject == nil {
		t.Fatalf("package request = %#v", packageRequest)
	}
	globRequest := operations[2].Value.(terminalexperience.InteractionRequest)
	if globRequest.Kind != terminalexperience.InteractionMultiSelect || globRequest.ConsoleStepID != zipPatternsFormID || globRequest.TranscriptLabel != "File patterns" || !globRequest.HasDefault || !reflect.DeepEqual(globRequest.Default.Values, []string{"**/*"}) || globRequest.ParsePlain == nil || globRequest.TranscriptProject == nil {
		t.Fatalf("glob request = %#v", globRequest)
	}
	outputRequest := operations[3].Value.(terminalexperience.InteractionRequest)
	if outputRequest.Kind != terminalexperience.InteractionText || outputRequest.ConsoleStepID != zipOutputFormID || outputRequest.TranscriptLabel != "Archive output" || outputRequest.Placeholder != "default" || !outputRequest.HasDefault || outputRequest.Default.Value != "default" || outputRequest.PlainPrompt != "Enter name [default]: " || outputRequest.ParsePlain == nil || outputRequest.TranscriptProject == nil {
		t.Fatalf("output request = %#v", outputRequest)
	}
	intro := operations[4].Value.(terminalexperience.PresentationDocument)
	if !reflect.DeepEqual(intro.Blocks, []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleTitle, Text: "HACKYCY CLI"}, {Role: terminalexperience.VisualRoleActive, Text: "Zip Directory"}}) {
		t.Fatalf("intro document = %#v", intro)
	}
	note := operations[5].Value.(terminalexperience.PresentationDocument)
	if !reflect.DeepEqual(note.Blocks, []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: "Zip plan"}, {Role: terminalexperience.VisualRoleMuted, Text: "Source: dist\nOutput: archive.zip"}}) {
		t.Fatalf("note document = %#v", note)
	}
	if got := operations[8].Value.(terminalexperience.PresentationDocument).Blocks[0].Role; got != terminalexperience.VisualRoleSuccess {
		t.Fatalf("success role = %v", got)
	}
}

func TestTerminalZipAdapterPlainPreservesLegacyInputGrammar(t *testing.T) {
	stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("invalid\n2\n\n2,3\n\nall\nnone\n\n custom archive \ncancel\n"),
		Output:       stdout,
		Diagnostics:  diagnostics,
	})
	run := experience.Open(context.Background())
	adapter := newTerminalZipAdapter(run)
	choices := []PlanningChoice{{Value: "one", Label: "one", Hint: "first"}, {Value: "two", Label: "two", Hint: "second"}, {Value: "three", Label: "three"}}
	packageRoot, cancelled, err := adapter.SelectPackage(SelectPackageStep{Message: "Select a package to zip:", Options: choices})
	if err != nil || cancelled || packageRoot != "two" {
		t.Fatalf("SelectPackage() = (%q, %t, %v)", packageRoot, cancelled, err)
	}
	source, cancelled, err := adapter.SelectSource(SelectSourceStep{Message: "Select a directory to zip:", Options: choices})
	if err != nil || cancelled || source != "one" {
		t.Fatalf("SelectSource() = (%q, %t, %v)", source, cancelled, err)
	}
	globStep := SelectGlobStep{Message: "Select file patterns to include in the zip:", Options: []PlanningChoice{{Value: "**/*", Label: "All"}, {Value: "**/*.html", Label: "HTML"}, {Value: "assets/**/*", Label: "Assets"}}, InitialValues: []string{"**/*"}}
	patterns, cancelled, err := adapter.SelectGlob(globStep)
	if err != nil || cancelled || !reflect.DeepEqual(patterns, []string{"**/*.html", "assets/**/*"}) {
		t.Fatalf("SelectGlob() = (%#v, %t, %v)", patterns, cancelled, err)
	}
	for _, value := range [][]string{{"**/*"}, {"**/*"}, {"**/*"}} {
		patterns, cancelled, err = adapter.SelectGlob(globStep)
		if err != nil || cancelled || !reflect.DeepEqual(patterns, value) {
			t.Fatalf("default glob = (%#v, %t, %v), want %#v", patterns, cancelled, err, value)
		}
	}
	name, cancelled, err := adapter.EditOutputFile(EditOutputFileStep{Message: "Enter name", InitialValue: "default"})
	if err != nil || cancelled || name != "default" {
		t.Fatalf("EditOutputFile() default = (%q, %t, %v)", name, cancelled, err)
	}
	name, cancelled, err = adapter.EditOutputFile(EditOutputFileStep{Message: "Enter name", InitialValue: "default"})
	if err != nil || cancelled || name != "custom archive" {
		t.Fatalf("EditOutputFile() = (%q, %t, %v)", name, cancelled, err)
	}
	_, cancelled, err = adapter.SelectSource(SelectSourceStep{Message: "Select a directory to zip:", Options: choices})
	if err != nil || !cancelled {
		t.Fatalf("SelectSource() cancellation = (%t, %v)", cancelled, err)
	}
	if stdout.Len() != 0 || !strings.Contains(diagnostics.String(), "Invalid selection") || !strings.Contains(diagnostics.String(), "2) two - second") || !strings.Contains(diagnostics.String(), "Enter name [default]: ") || terminaltest.ContainsTerminalControl(append(stdout.Bytes(), diagnostics.Bytes()...)) {
		t.Fatalf("Plain streams = (%q, %q)", stdout.String(), diagnostics.String())
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	for _, cancellation := range []string{"q", "quit", "cancel"} {
		t.Run(cancellation, func(t *testing.T) {
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
				Input:        strings.NewReader(cancellation + "\n"),
				Output:       io.Discard,
				Diagnostics:  io.Discard,
			})
			run := experience.Open(context.Background())
			_, cancelled, err := newTerminalZipAdapter(run).SelectSource(SelectSourceStep{Options: choices})
			if err != nil || !cancelled {
				t.Fatalf("SelectSource() cancellation = (%t, %v)", cancelled, err)
			}
			if err := run.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		})
	}
}

func TestZIPPlainJourneyCreatesAStructuralArchive(t *testing.T) {
	project := t.TempDir()
	writeStandaloneZIPFile(t, project, "package.json", `{"name":"project","devDependencies":{"vite":"1"}}`)
	writeStandaloneZIPFile(t, project, "dist/index.html", "<main />")
	writeStandaloneZIPFile(t, project, "dist/assets/app.js", "console.log('app')")
	writeStandaloneZIPFile(t, project, "dist/.secret", "not archived")
	withZIPWorkingDirectory(t, project)
	stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("\n\n\n"),
		Output:       stdout,
		Diagnostics:  diagnostics,
	})

	err := runZIP(&Options{
		Context:   context.Background(),
		Directory: ".",
		Open:      false,
		WithDir:   "bundle",
		Terminal:  experience,
	})
	if err != nil || stdout.String() != "Done!\n" || !strings.Contains(diagnostics.String(), "Zip Directory") || !strings.Contains(diagnostics.String(), "Collecting files") || terminaltest.ContainsTerminalControl(append(stdout.Bytes(), diagnostics.Bytes()...)) {
		t.Fatalf("Plain zip = (%v), streams = (%q, %q)", err, stdout.String(), diagnostics.String())
	}
	archivePath := filepath.Join(project, "dist", "project.zip")
	archive, err := archivezip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	contents := make(map[string]string)
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open archive entry: %v", err)
		}
		bytes, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatalf("read archive entry: %v", err)
		}
		contents[file.Name] = string(bytes)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	want := map[string]string{"bundle/index.html": "<main />", "bundle/assets/app.js": "console.log('app')"}
	if !reflect.DeepEqual(contents, want) {
		t.Fatalf("archive contents = %#v, want %#v", contents, want)
	}
}

func TestZipRemoteNameResolverPrefersOriginAndIgnoresUnusableOutput(t *testing.T) {
	resolver := newZipRemoteNameResolver(zipRemoteOutputRunnerFunc(func(directory string) ([]byte, error) {
		if directory != "/workspace" {
			t.Fatalf("directory = %q", directory)
		}
		return []byte("upstream\thttps://github.com/example/upstream.git (fetch)\norigin\tgit@github.com:example/project.git (fetch)\n"), nil
	}))
	name, err := resolver.ResolveRemoteName("/workspace")
	if err != nil || name != "example-project" {
		t.Fatalf("ResolveRemoteName() = (%q, %v)", name, err)
	}

	resolver = newZipRemoteNameResolver(zipRemoteOutputRunnerFunc(func(string) ([]byte, error) {
		return []byte("origin\tinvalid remote (fetch)\n"), nil
	}))
	name, err = resolver.ResolveRemoteName("/workspace")
	if err != nil || name != "" {
		t.Fatalf("invalid remote = (%q, %v)", name, err)
	}

	wantError := errors.New("git unavailable")
	resolver = newZipRemoteNameResolver(zipRemoteOutputRunnerFunc(func(string) ([]byte, error) {
		return nil, wantError
	}))
	_, err = resolver.ResolveRemoteName("/workspace")
	if !errors.Is(err, wantError) {
		t.Fatalf("resolver error = %v, want %v", err, wantError)
	}
}

func TestOSZipRemoteOutputRunnerReadsADisposableRepository(t *testing.T) {
	repository := t.TempDir()
	for _, arguments := range [][]string{{"init"}, {"remote", "add", "origin", "https://github.com/example/project.git"}} {
		command := exec.Command("git", arguments...)
		command.Dir = repository
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v\n%s", arguments, err, output)
		}
	}
	resolver := newZipRemoteNameResolver(osZipRemoteOutputRunner{})
	name, err := resolver.ResolveRemoteName(repository)
	if err != nil || name != "example-project" {
		t.Fatalf("ResolveRemoteName() = (%q, %v)", name, err)
	}
}

func TestHostZipRevealerUsesPlatformCommands(t *testing.T) {
	testCases := []struct {
		goos string
		name string
		args []string
	}{
		{goos: "darwin", name: "open", args: []string{"/tmp/archive.zip"}},
		{goos: "linux", name: "xdg-open", args: []string{"/tmp/archive.zip"}},
		{goos: "windows", name: "cmd", args: []string{"/c", "start", "", "/tmp/archive.zip"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.goos, func(t *testing.T) {
			name, args, err := zipRevealCommand(testCase.goos, "/tmp/archive.zip")
			if err != nil || name != testCase.name || !reflect.DeepEqual(args, testCase.args) {
				t.Fatalf("zipRevealCommand() = (%q, %#v, %v)", name, args, err)
			}
		})
	}
	if _, _, err := zipRevealCommand("plan9", "/tmp/archive.zip"); err == nil {
		t.Fatal("unsupported reveal platform did not fail")
	}

	runner := &recordingZipHostRunner{}
	revealer := newHostZipRevealer(runner)
	if err := revealer.Reveal("/tmp/archive.zip"); err != nil {
		t.Fatalf("Reveal() error = %v", err)
	}
	wantName, wantArgs, _ := zipRevealCommand(runtime.GOOS, "/tmp/archive.zip")
	if runner.name != wantName || !reflect.DeepEqual(runner.arguments, wantArgs) {
		t.Fatalf("runner = %#v, want (%q, %#v)", runner, wantName, wantArgs)
	}
}

type zipRemoteOutputRunnerFunc func(string) ([]byte, error)

func (function zipRemoteOutputRunnerFunc) Output(directory string) ([]byte, error) {
	return function(directory)
}

type recordingZipHostRunner struct {
	name      string
	arguments []string
}

func (runner *recordingZipHostRunner) Run(name string, arguments []string) error {
	runner.name = name
	runner.arguments = append([]string(nil), arguments...)
	return nil
}

func writeStandaloneZIPFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func withZIPWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
