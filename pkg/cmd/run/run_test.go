package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewRequiresEveryRunDependency(t *testing.T) {
	workingDirectory := func() (string, error) { return t.TempDir(), nil }
	reader := moduleFileReader(os.ReadFile)
	exists := FileExists(modulePathExists)
	prompter := &recordingRunPrompter{}
	runner := &recordingRunChildRunner{}
	presenter := &recordingRunPresenter{}
	testCases := []struct {
		name         string
		dependencies Dependencies
		message      string
	}{
		{name: "working directory", dependencies: Dependencies{Reader: reader, Exists: exists, Prompter: prompter, Runner: runner, Presenter: presenter}, message: "run working directory is required"},
		{name: "reader", dependencies: Dependencies{WorkingDirectory: workingDirectory, Exists: exists, Prompter: prompter, Runner: runner, Presenter: presenter}, message: "run package reader is required"},
		{name: "exists", dependencies: Dependencies{WorkingDirectory: workingDirectory, Reader: reader, Prompter: prompter, Runner: runner, Presenter: presenter}, message: "run path existence checker is required"},
		{name: "prompter", dependencies: Dependencies{WorkingDirectory: workingDirectory, Reader: reader, Exists: exists, Runner: runner, Presenter: presenter}, message: "run prompter is required"},
		{name: "runner", dependencies: Dependencies{WorkingDirectory: workingDirectory, Reader: reader, Exists: exists, Prompter: prompter, Presenter: presenter}, message: "run child runner is required"},
		{name: "presenter", dependencies: Dependencies{WorkingDirectory: workingDirectory, Reader: reader, Exists: exists, Prompter: prompter, Runner: runner}, message: "run presenter is required"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := New(testCase.dependencies)
			if err == nil || err.Error() != testCase.message {
				t.Fatalf("New() error = %v, want %q", err, testCase.message)
			}
		})
	}
}

func TestRunComposesDiscoverySelectionPresentationAndChildRequest(t *testing.T) {
	root := t.TempDir()
	writeRunPackage(t, root, `{"scripts":{"check":"go test ./..."}}`)
	if err := os.WriteFile(filepath.Join(root, "b"+"un"+".lock"), nil, 0o600); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	prompter := &recordingRunPrompter{script: "check", manager: PackageManagerExternal}
	runner := &recordingRunChildRunner{result: Result{ExitCode: 7}}
	presenter := &recordingRunPresenter{}
	module := newRunModuleForTest(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Reader:           moduleFileReader(os.ReadFile),
		Exists: func(path string) (bool, error) {
			if prompter.scriptPrompt.Message == "" {
				t.Fatal("package-manager check happened before script selection")
			}
			return modulePathExists(path)
		},
		Prompter:  prompter,
		Runner:    runner,
		Presenter: presenter,
	})

	result, err := module.Run(context.Background(), Input{})

	if err != nil || result.ExitCode != 7 {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	wantRequest := ChildRequest{Executable: string(PackageManagerExternal), Arguments: []string{"run", "check"}, Directory: root}
	if !reflect.DeepEqual(runner.requests, []ChildRequest{wantRequest}) {
		t.Fatalf("requests = %#v, want %#v", runner.requests, []ChildRequest{wantRequest})
	}
	wantEvents := []string{"intro:Run Script", "info:" + string(PackageManagerExternal) + " run check", "blank"}
	if !reflect.DeepEqual(presenter.events, wantEvents) {
		t.Fatalf("presentation = %#v, want %#v", presenter.events, wantEvents)
	}
	if len(prompter.managerPrompt.Options) == 0 || prompter.managerPrompt.Options[0].Value != PackageManagerExternal {
		t.Fatalf("manager prompt = %#v", prompter.managerPrompt)
	}
}

func TestRunMapsEitherPromptCancellationToSuccessWithoutAChild(t *testing.T) {
	testCases := []struct {
		name     string
		prompter *recordingRunPrompter
	}{
		{name: "script", prompter: &recordingRunPrompter{scriptCancelled: true}},
		{name: "package manager", prompter: &recordingRunPrompter{script: "check", managerCancelled: true}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			writeRunPackage(t, root, `{"scripts":{"check":"go test ./..."}}`)
			runner := &recordingRunChildRunner{}
			presenter := &recordingRunPresenter{}
			module := newRunModuleForTest(t, Dependencies{
				WorkingDirectory: func() (string, error) { return root, nil },
				Reader:           moduleFileReader(os.ReadFile),
				Exists:           modulePathExists,
				Prompter:         testCase.prompter,
				Runner:           runner,
				Presenter:        presenter,
			})

			result, err := module.Run(context.Background(), Input{})

			if err != nil || result.ExitCode != 0 || len(runner.requests) != 0 || !containsRunEvent(presenter.events, "cancel:Operation cancelled.") {
				t.Fatalf("Run() = (%#v, %v), requests = %#v, events = %#v", result, err, runner.requests, presenter.events)
			}
		})
	}
}

func TestRunDoesNotLaunchAfterContextCancellationAtThePromptBoundary(t *testing.T) {
	root := t.TempDir()
	writeRunPackage(t, root, `{"scripts":{"check":"go test ./..."}}`)
	ctx, cancel := context.WithCancel(context.Background())
	prompter := &cancellingRunPrompter{recordingRunPrompter: recordingRunPrompter{script: "check"}, cancelAfterScript: cancel}
	runner := &recordingRunChildRunner{}
	module := newRunModuleForTest(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Reader:           moduleFileReader(os.ReadFile),
		Exists:           modulePathExists,
		Prompter:         prompter,
		Runner:           runner,
		Presenter:        &recordingRunPresenter{},
	})

	_, err := module.Run(ctx, Input{})

	if !errors.Is(err, context.Canceled) || len(runner.requests) != 0 {
		t.Fatalf("Run() error = %v, requests = %#v", err, runner.requests)
	}
}

func newRunModuleForTest(t *testing.T, dependencies Dependencies) *Module {
	t.Helper()
	module, err := New(dependencies)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return module
}

type moduleFileReader func(string) ([]byte, error)

func (function moduleFileReader) ReadFile(path string) ([]byte, error) {
	return function(path)
}

func modulePathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

type recordingRunChildRunner struct {
	requests []ChildRequest
	result   Result
	err      error
}

func (runner *recordingRunChildRunner) Run(_ context.Context, request ChildRequest) (Result, error) {
	runner.requests = append(runner.requests, request)
	return runner.result, runner.err
}

type cancellingRunPrompter struct {
	recordingRunPrompter
	cancelAfterScript func()
}

func (prompter *cancellingRunPrompter) SelectScript(prompt ScriptPrompt) (string, bool, error) {
	selected, cancelled, err := prompter.recordingRunPrompter.SelectScript(prompt)
	if prompter.cancelAfterScript != nil {
		prompter.cancelAfterScript()
	}
	return selected, cancelled, err
}

func containsRunEvent(events []string, target string) bool {
	for _, event := range events {
		if event == target {
			return true
		}
	}
	return false
}
