package rm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewRequiresEveryRMDependency(t *testing.T) {
	workingDirectory := func() (string, error) { return t.TempDir(), nil }
	prompter := &scriptedPrompter{}
	remover := pathRemoverFunc(os.RemoveAll)
	presenter := &recordingPresenter{}
	testCases := []struct {
		name         string
		dependencies Dependencies
		message      string
	}{
		{name: "working directory", dependencies: Dependencies{Prompter: prompter, Remover: remover, Presenter: presenter}, message: "rm working directory is required"},
		{name: "prompter", dependencies: Dependencies{WorkingDirectory: workingDirectory, Remover: remover, Presenter: presenter}, message: "rm prompter is required"},
		{name: "remover", dependencies: Dependencies{WorkingDirectory: workingDirectory, Prompter: prompter, Presenter: presenter}, message: "rm remover is required"},
		{name: "presenter", dependencies: Dependencies{WorkingDirectory: workingDirectory, Prompter: prompter, Remover: remover}, message: "rm presenter is required"},
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

func TestRunReturnsCancelledContextBeforeAnyCommandWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	presenter := &recordingPresenter{}
	workingDirectoryCalls := 0
	module := newRMModule(t, Dependencies{
		WorkingDirectory: func() (string, error) {
			workingDirectoryCalls++
			return t.TempDir(), nil
		},
		Prompter:  &scriptedPrompter{},
		Remover:   pathRemoverFunc(os.RemoveAll),
		Presenter: presenter,
	})

	_, err := module.Run(ctx, Input{Paths: []string{"target"}})

	if !errors.Is(err, context.Canceled) || workingDirectoryCalls != 0 || len(presenter.events) != 0 {
		t.Fatalf("cancelled run = (%v, calls %d, events %#v)", err, workingDirectoryCalls, presenter.events)
	}
}

func TestRunStopsAfterExplicitConfirmationBeforeDeletionWhenContextIsCancelled(t *testing.T) {
	root := newDisposableRoot(t)
	target := writeDisposableFile(t, root, "target.txt")
	ctx, cancel := context.WithCancel(context.Background())
	presenter := &recordingPresenter{}
	prompter := &cancellingPrompter{scriptedPrompter: scriptedPrompter{confirmed: true}, cancelAfterExplicit: cancel}
	module := newRMModule(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Prompter:         prompter,
		Remover:          pathRemoverFunc(os.RemoveAll),
		Presenter:        presenter,
	})

	_, err := module.Run(ctx, Input{Paths: []string{filepath.Base(target)}})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("explicit cancelled run error = %v", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("cancelled explicit run changed target: %v", statErr)
	}
	if containsEventPrefix(presenter.events, "start:Deleting") {
		t.Fatalf("cancelled explicit run started deletion: %#v", presenter.events)
	}
}

func TestRunStopsAfterSmartSelectionBeforeDeletionWhenContextIsCancelled(t *testing.T) {
	root := newDisposableRoot(t)
	target := makeDirectory(t, root, "dist")
	ctx, cancel := context.WithCancel(context.Background())
	presenter := &recordingPresenter{}
	prompter := &cancellingPrompter{
		scriptedPrompter:      scriptedPrompter{action: smartActions[0], targets: []string{target}},
		cancelAfterSmartPaths: cancel,
	}
	module := newRMModule(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Prompter:         prompter,
		Remover:          pathRemoverFunc(os.RemoveAll),
		Presenter:        presenter,
	})

	_, err := module.Run(ctx, Input{})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("smart cancelled run error = %v", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("cancelled smart run changed target: %v", statErr)
	}
	if containsEventPrefix(presenter.events, "start:Deleting") {
		t.Fatalf("cancelled smart run started deletion: %#v", presenter.events)
	}
}

func TestRunMapsPromptCancellationAndNoTargetOutcomesWithoutDeletion(t *testing.T) {
	root := newDisposableRoot(t)
	target := writeDisposableFile(t, root, "target.txt")
	explicitPresenter := &recordingPresenter{}
	explicitModule := newRMModule(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Prompter:         &scriptedPrompter{},
		Remover:          pathRemoverFunc(os.RemoveAll),
		Presenter:        explicitPresenter,
	})
	if _, err := explicitModule.Run(context.Background(), Input{Paths: []string{filepath.Base(target)}}); err != nil {
		t.Fatalf("explicit cancellation run: %v", err)
	}
	if _, err := os.Stat(target); err != nil || !containsEvent(explicitPresenter.events, "cancel:Cancelled.") {
		t.Fatalf("explicit cancellation changed target or output = (%v, %#v)", err, explicitPresenter.events)
	}

	smartPresenter := &recordingPresenter{}
	smartModule := newRMModule(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Prompter:         &scriptedPrompter{action: smartActions[0]},
		Remover:          pathRemoverFunc(os.RemoveAll),
		Presenter:        smartPresenter,
	})
	if _, err := smartModule.Run(context.Background(), Input{}); err != nil {
		t.Fatalf("empty smart run: %v", err)
	}
	wantEvents := []string{"intro:Remove", "start:Scanning...", "stop:No targets found.", "outro:Nothing to clean."}
	if !reflect.DeepEqual(smartPresenter.events, wantEvents) {
		t.Fatalf("empty smart events = %#v, want %#v", smartPresenter.events, wantEvents)
	}
}

func TestRunKeepsPartialDeletionCommittedAndReturnsSuccess(t *testing.T) {
	root := newDisposableRoot(t)
	successful := writeDisposableFile(t, root, "successful.txt")
	failed := writeDisposableFile(t, root, "failed.txt")
	failure := errors.New("permission denied")
	presenter := &recordingPresenter{}
	module := newRMModule(t, Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		Prompter:         &scriptedPrompter{confirmed: true},
		Remover:          &recordingRemover{failures: map[string]error{failed: failure}},
		Presenter:        presenter,
	})

	_, err := module.Run(context.Background(), Input{Paths: []string{filepath.Base(successful), filepath.Base(failed)}})

	if err != nil {
		t.Fatalf("partial rm run error = %v", err)
	}
	if _, statErr := os.Stat(successful); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("successful target = %v, want missing", statErr)
	}
	if _, statErr := os.Stat(failed); statErr != nil {
		t.Fatalf("failed target = %v, want retained", statErr)
	}
	if !containsEvent(presenter.events, "notice:  skipped: permission denied") || !containsEvent(presenter.events, "outro:Done!") {
		t.Fatalf("partial rm presentation = %#v", presenter.events)
	}
}

func newRMModule(t *testing.T, dependencies Dependencies) *Module {
	t.Helper()
	module, err := New(dependencies)
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}
	return module
}

type cancellingPrompter struct {
	scriptedPrompter
	cancelAfterExplicit   func()
	cancelAfterSmartPaths func()
}

func (prompter *cancellingPrompter) ConfirmExplicit(prompt ExplicitConfirmationPrompt) (bool, bool, error) {
	confirmed, cancelled, err := prompter.scriptedPrompter.ConfirmExplicit(prompt)
	if prompter.cancelAfterExplicit != nil {
		prompter.cancelAfterExplicit()
	}
	return confirmed, cancelled, err
}

func (prompter *cancellingPrompter) SelectSmartTargets(prompt SmartTargetPrompt) ([]string, bool, error) {
	targets, cancelled, err := prompter.scriptedPrompter.SelectSmartTargets(prompt)
	if prompter.cancelAfterSmartPaths != nil {
		prompter.cancelAfterSmartPaths()
	}
	return targets, cancelled, err
}

func containsEvent(events []string, target string) bool {
	for _, event := range events {
		if event == target {
			return true
		}
	}
	return false
}

func containsEventPrefix(events []string, prefix string) bool {
	for _, event := range events {
		if len(event) >= len(prefix) && event[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
