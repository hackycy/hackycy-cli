package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalRunAdapterTranslatesSelectionAndPresentation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "build"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: string(PackageManagerExternal)}},
	)
	run := experience.Open(context.Background())
	adapter := newTerminalRunAdapter(run)

	script, cancelled, err := adapter.SelectScript(ScriptPrompt{
		Message: "Select a script to run:",
		Options: []ScriptChoice{
			{Value: "check", Label: "check", Hint: "go test ./..."},
			{Value: "build", Label: "build", Hint: "go build ./cmd/ycy"},
		},
	})
	if err != nil || cancelled || script != "build" {
		t.Fatalf("SelectScript() = (%q, %t, %v)", script, cancelled, err)
	}
	manager, cancelled, err := adapter.SelectPackageManager(PackageManagerPrompt{
		Message: "Select a package manager:",
		Options: []PackageManagerChoice{{Value: PackageManagerExternal, Label: string(PackageManagerExternal)}},
	})
	if err != nil || cancelled || manager != PackageManagerExternal {
		t.Fatalf("SelectPackageManager() = (%q, %t, %v)", manager, cancelled, err)
	}
	adapter.Intro("Run Script")
	adapter.Info(string(PackageManagerExternal) + " run build")
	adapter.Blank()
	adapter.Cancel("Operation cancelled.")
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 7 || operations[0].Kind != terminaltest.AskOperation || operations[2].Kind != terminaltest.NoticeOperation || operations[5].Kind != terminaltest.ResultOperation || operations[6].Kind != terminaltest.CloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	scriptRequest := operations[0].Value.(terminalexperience.InteractionRequest)
	if scriptRequest.Kind != terminalexperience.InteractionSelect || scriptRequest.Message != "Select a script to run:" || !reflect.DeepEqual(scriptRequest.Options, []terminalexperience.InteractionOption{
		{Label: "check", Value: "check", Description: "go test ./..."},
		{Label: "build", Value: "build", Description: "go build ./cmd/ycy"},
	}) || !reflect.DeepEqual(scriptRequest.CancelValues, []string{"", "q", "quit", "cancel"}) {
		t.Fatalf("script request = %#v", scriptRequest)
	}
	managerRequest := operations[1].Value.(terminalexperience.InteractionRequest)
	if managerRequest.Kind != terminalexperience.InteractionSelect || managerRequest.Message != "Select a package manager:" || !reflect.DeepEqual(managerRequest.Options, []terminalexperience.InteractionOption{{Label: string(PackageManagerExternal), Value: string(PackageManagerExternal)}}) {
		t.Fatalf("manager request = %#v", managerRequest)
	}
	intro := operations[2].Value.(terminalexperience.PresentationDocument)
	if !reflect.DeepEqual(intro.Blocks, []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleTitle, Text: "HACKYCY CLI"}, {Role: terminalexperience.VisualRoleActive, Text: "Run Script"}}) {
		t.Fatalf("intro document = %#v", intro)
	}
	if got := operations[5].Value.(terminalexperience.PresentationDocument).Blocks[0].Role; got != terminalexperience.VisualRoleWarning {
		t.Fatalf("cancellation role = %v", got)
	}
}

func TestTerminalRunAdapterDetailedPathUsesOneControlledWorkCatalog(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "check"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: string(PackageManagerNPM)}},
	)
	run := experience.Open(context.Background())
	adapter := newTerminalRunAdapter(run)
	adapter.enableDetailed()

	if _, cancelled, err := adapter.SelectScript(ScriptPrompt{Message: "Select a script", Options: []ScriptChoice{{Value: "check", Label: "check"}}}); err != nil || cancelled {
		t.Fatalf("SelectScript() = (%t, %v)", cancelled, err)
	}
	if _, cancelled, err := adapter.SelectPackageManager(PackageManagerPrompt{Message: "Select a package manager", Options: []PackageManagerChoice{{Value: PackageManagerNPM, Label: "npm"}}}); err != nil || cancelled {
		t.Fatalf("SelectPackageManager() = (%t, %v)", cancelled, err)
	}
	for _, phase := range runPhaseDefinitions {
		adapter.reportRunPhase(phase.ID, terminalexperience.PhaseActive, "working")
		adapter.reportRunPhase(phase.ID, terminalexperience.PhaseCompleted, "complete")
	}
	if err := adapter.finishDetailed(); err != nil {
		t.Fatalf("finishDetailed() error = %v", err)
	}

	operations := experience.Run.Operations()
	var starts, updates, closes, tracks int
	for _, operation := range operations {
		switch operation.Kind {
		case terminaltest.StartWorkOperation:
			starts++
			catalog := operation.Value.(terminalexperience.WorkCatalog)
			if catalog.ID != runWorkCatalogID || !reflect.DeepEqual(catalog.Phases, runPhaseDefinitions) {
				t.Fatalf("work catalog = %#v", catalog)
			}
		case terminaltest.WorkUpdateOperation:
			updates++
		case terminaltest.WorkCloseOperation:
			closes++
		case terminaltest.TrackOperation:
			tracks++
		}
	}
	if starts != 1 || updates != len(runPhaseDefinitions)*2 || closes != 1 || tracks != 0 {
		t.Fatalf("work operations = starts:%d updates:%d closes:%d tracks:%d (%#v)", starts, updates, closes, tracks, operations)
	}
	requests := []terminalexperience.InteractionRequest{}
	for _, operation := range operations {
		if operation.Kind == terminaltest.AskOperation {
			requests = append(requests, operation.Value.(terminalexperience.InteractionRequest))
		}
	}
	if len(requests) != 2 || requests[0].ConsoleStepID != runScriptFormID || requests[1].ConsoleStepID != runManagerFormID {
		t.Fatalf("form IDs = %#v", requests)
	}
}

func TestTerminalRunAdapterMapsAutomationInteractionFailure(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrAutomationInteraction})
	adapter := newTerminalRunAdapter(experience.Open(context.Background()))
	if _, _, err := adapter.SelectScript(ScriptPrompt{Options: []ScriptChoice{{Value: "check", Label: "check"}}}); !errors.Is(err, errRunRequiresInteractive) {
		t.Fatalf("SelectScript() error = %v", err)
	}
}

func TestRunPlainSelectionReleasesFormBeforeRawChildIO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writeStandaloneRunFile(t, project, "package.json", `{"scripts":{"check":"echo check"}}`)
	writeStandaloneRunFile(t, project, "b"+"un"+".lock", "")
	binDirectory := filepath.Join(root, "bin")
	argumentsPath := filepath.Join(root, "arguments")
	workingDirectoryPath := filepath.Join(root, "working-directory")
	manager := filepath.Join(binDirectory, string(PackageManagerExternal))
	managerScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RUN_ARGUMENTS\"\npwd > \"$RUN_WORKING_DIRECTORY\"\nprintf 'child stdout'\nprintf 'child stderr' >&2\n"
	if err := os.MkdirAll(binDirectory, 0o700); err != nil {
		t.Fatalf("create manager directory: %v", err)
	}
	if err := os.WriteFile(manager, []byte(managerScript), 0o700); err != nil {
		t.Fatalf("write manager fixture: %v", err)
	}
	t.Setenv("PATH", binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RUN_ARGUMENTS", argumentsPath)
	t.Setenv("RUN_WORKING_DIRECTORY", workingDirectoryPath)
	withRunWorkingDirectory(t, project)

	rawInput := strings.NewReader("invalid\n1\n1\n")
	stdout, diagnostics, childStderr := &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        rawInput,
		Output:       stdout,
		Diagnostics:  diagnostics,
	})

	err := runRun(&Options{
		Context:          context.Background(),
		WorkingDirectory: os.Getwd,
		Terminal:         experience,
		Reader:           osRunFileReader{},
		Exists:           osRunPathExists,
		Runner:           newOSRunChildRunner(rawInput, stdout, childStderr),
	})
	if err != nil {
		t.Fatalf("runRun() error = %v", err)
	}
	if got, want := stdout.String(), "child stdout"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(diagnostics.String(), "Invalid selection") || !strings.Contains(diagnostics.String(), "check - echo check") || !strings.Contains(diagnostics.String(), "HACKYCY CLI") || !strings.Contains(diagnostics.String(), string(PackageManagerExternal)+" run check") || terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
		t.Fatalf("Plain diagnostics = %q", diagnostics.String())
	}
	if got, want := childStderr.String(), "child stderr"; got != want {
		t.Fatalf("child stderr = %q, want %q", got, want)
	}
	assertRunProcessFile(t, argumentsPath, "run\ncheck\n")
	resolvedProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatalf("resolve project path: %v", err)
	}
	assertRunProcessFile(t, workingDirectoryPath, resolvedProject+"\n")
}

func TestRunPlainCancellationWritesOneCancellationResultBeforeClosing(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writeStandaloneRunFile(t, project, "package.json", `{"scripts":{"check":"echo check"}}`)
	withRunWorkingDirectory(t, project)
	input := strings.NewReader("\n")
	stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        input,
		Output:       stdout,
		Diagnostics:  diagnostics,
	})
	started := false
	err := runRun(&Options{
		Context:          context.Background(),
		WorkingDirectory: os.Getwd,
		Terminal:         experience,
		Reader:           osRunFileReader{},
		Exists:           osRunPathExists,
		Runner: runChildRunnerFunc(func(context.Context, ChildRequest) (Result, error) {
			started = true
			return Result{}, nil
		}),
	})
	if err != nil || started {
		t.Fatalf("runRun() = (%v, started=%t)", err, started)
	}
	if got, want := stdout.String(), "Operation cancelled.\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), diagnostics.Bytes()...)) {
		t.Fatalf("Plain cancellation emitted terminal control: (%q, %q)", stdout.String(), diagnostics.String())
	}
}

func TestReleasedRunChildRunnerReleasesBeforeStartingChild(t *testing.T) {
	steps := []string{}
	runner := releasedRunChildRunner{
		release: func() error {
			steps = append(steps, "release")
			return nil
		},
		runner: runChildRunnerFunc(func(context.Context, ChildRequest) (Result, error) {
			steps = append(steps, "child")
			return Result{}, nil
		}),
	}
	if _, err := runner.Run(context.Background(), ChildRequest{}); err != nil || !reflect.DeepEqual(steps, []string{"release", "child"}) {
		t.Fatalf("Run() = (steps=%#v, err=%v)", steps, err)
	}
}

func writeStandaloneRunFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create package directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func withRunWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

type runChildRunnerFunc func(context.Context, ChildRequest) (Result, error)

func (function runChildRunnerFunc) Run(ctx context.Context, request ChildRequest) (Result, error) {
	return function(ctx, request)
}
