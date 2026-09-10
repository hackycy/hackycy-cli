package rm

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelectExplicitTargetsUsesDefaultNegativeConfirmationAndForceBypass(t *testing.T) {
	targets := []string{"/tmp/one", "/tmp/two"}
	prompter := &scriptedPrompter{confirmed: true}

	selected, cancelled, err := selectExplicitTargets(targets, false, prompter)

	if err != nil || cancelled || !reflect.DeepEqual(selected, targets) {
		t.Fatalf("selected explicit targets = (%#v, %t, %v), want (%#v, false, nil)", selected, cancelled, err, targets)
	}
	wantPrompt := ExplicitConfirmationPrompt{Message: "Delete 2 items?", Initial: false}
	if !reflect.DeepEqual(prompter.explicitPrompt, wantPrompt) {
		t.Fatalf("explicit prompt = %#v, want %#v", prompter.explicitPrompt, wantPrompt)
	}

	forcePrompter := &scriptedPrompter{}
	selected, cancelled, err = selectExplicitTargets([]string{"/tmp/one"}, true, forcePrompter)
	if err != nil || cancelled || !reflect.DeepEqual(selected, []string{"/tmp/one"}) || forcePrompter.explicitCalls != 0 {
		t.Fatalf("forced explicit targets = (%#v, %t, %v), calls = %d", selected, cancelled, err, forcePrompter.explicitCalls)
	}
}

func TestSelectExplicitTargetsTreatsDeclineAndCancellationAsCancellation(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		prompter  scriptedPrompter
		wantCalls int
	}{
		{name: "declined", prompter: scriptedPrompter{}},
		{name: "cancelled", prompter: scriptedPrompter{confirmCancelled: true}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			selected, cancelled, err := selectExplicitTargets([]string{"/tmp/one"}, false, &testCase.prompter)
			if err != nil || !cancelled || len(selected) != 0 || testCase.prompter.explicitCalls != 1 {
				t.Fatalf("selected explicit targets = (%#v, %t, %v), calls = %d", selected, cancelled, err, testCase.prompter.explicitCalls)
			}
			if testCase.prompter.explicitPrompt.Message != "Delete 1 item?" {
				t.Fatalf("singular prompt = %#v", testCase.prompter.explicitPrompt)
			}
		})
	}
}

func TestSelectSmartActionPresentsAllActionsAndReturnsCancellation(t *testing.T) {
	prompter := &scriptedPrompter{action: smartActions[3]}

	action, cancelled, err := selectSmartAction(prompter)

	if err != nil || cancelled || action != smartActions[3] {
		t.Fatalf("smart action = (%#v, %t, %v), want (%#v, false, nil)", action, cancelled, err, smartActions[3])
	}
	wantPrompt := SmartActionPrompt{Message: "Select a clean action", Options: smartActions}
	if !reflect.DeepEqual(prompter.actionPrompt, wantPrompt) {
		t.Fatalf("action prompt = %#v, want %#v", prompter.actionPrompt, wantPrompt)
	}

	_, cancelled, err = selectSmartAction(&scriptedPrompter{actionCancelled: true})
	if err != nil || !cancelled {
		t.Fatal("cancelled smart action = false, want true")
	}
}

func TestSelectSmartTargetsDefaultsToAllAndForceBypassesMultiselect(t *testing.T) {
	workingDirectory := t.TempDir()
	targets := []string{
		filepath.Join(workingDirectory, "dist"),
		filepath.Join(workingDirectory, "nested", "node_modules"),
	}
	prompter := &scriptedPrompter{targets: []string{targets[1]}}

	selected, cancelled, err := selectSmartTargets(workingDirectory, targets, false, prompter)

	if err != nil || cancelled || !reflect.DeepEqual(selected, []string{targets[1]}) {
		t.Fatalf("smart targets = (%#v, %t, %v)", selected, cancelled, err)
	}
	wantPrompt := SmartTargetPrompt{
		Message: "Select items to delete",
		Options: []SmartTargetChoice{
			{Value: targets[0], Label: "dist"},
			{Value: targets[1], Label: filepath.Join("nested", "node_modules")},
		},
		InitialValues: targets,
	}
	if !reflect.DeepEqual(prompter.targetPrompt, wantPrompt) {
		t.Fatalf("target prompt = %#v, want %#v", prompter.targetPrompt, wantPrompt)
	}

	forcePrompter := &scriptedPrompter{}
	selected, cancelled, err = selectSmartTargets(workingDirectory, targets, true, forcePrompter)
	if err != nil || cancelled || !reflect.DeepEqual(selected, targets) || forcePrompter.targetCalls != 0 {
		t.Fatalf("forced smart targets = (%#v, %t, %v), calls = %d", selected, cancelled, err, forcePrompter.targetCalls)
	}
}

func TestSelectSmartTargetsKeepsEmptySelectionDistinctFromCancellation(t *testing.T) {
	workingDirectory := t.TempDir()
	target := filepath.Join(workingDirectory, "dist")

	selected, cancelled, err := selectSmartTargets(workingDirectory, []string{target}, false, &scriptedPrompter{})

	if err != nil || cancelled || len(selected) != 0 {
		t.Fatalf("empty smart selection = (%#v, %t, %v)", selected, cancelled, err)
	}

	_, cancelled, err = selectSmartTargets(workingDirectory, []string{target}, false, &scriptedPrompter{targetsCancelled: true})
	if err != nil || !cancelled {
		t.Fatalf("cancelled smart selection = (%t, %v)", cancelled, err)
	}
}

func TestSelectionReturnsInteractionFailuresWithoutChangingThePromptOutcome(t *testing.T) {
	failure := errors.New("interactive terminal unavailable")
	prompter := &scriptedPrompter{err: failure}
	if _, cancelled, err := selectExplicitTargets([]string{"/tmp/one"}, false, prompter); cancelled || !errors.Is(err, failure) {
		t.Fatalf("explicit selection = (cancelled=%t, err=%v)", cancelled, err)
	}
	if _, cancelled, err := selectSmartAction(prompter); cancelled || !errors.Is(err, failure) {
		t.Fatalf("smart action = (cancelled=%t, err=%v)", cancelled, err)
	}
	if _, cancelled, err := selectSmartTargets(t.TempDir(), []string{"/tmp/one"}, false, prompter); cancelled || !errors.Is(err, failure) {
		t.Fatalf("smart targets = (cancelled=%t, err=%v)", cancelled, err)
	}
}

type scriptedPrompter struct {
	confirmed        bool
	confirmCancelled bool
	action           SmartAction
	actionCancelled  bool
	targets          []string
	targetsCancelled bool
	err              error

	explicitPrompt ExplicitConfirmationPrompt
	actionPrompt   SmartActionPrompt
	targetPrompt   SmartTargetPrompt
	explicitCalls  int
	targetCalls    int
}

func (prompter *scriptedPrompter) ConfirmExplicit(prompt ExplicitConfirmationPrompt) (bool, bool, error) {
	prompter.explicitCalls++
	prompter.explicitPrompt = prompt
	return prompter.confirmed, prompter.confirmCancelled, prompter.err
}

func (prompter *scriptedPrompter) SelectSmartAction(prompt SmartActionPrompt) (SmartAction, bool, error) {
	prompter.actionPrompt = prompt
	return prompter.action, prompter.actionCancelled, prompter.err
}

func (prompter *scriptedPrompter) SelectSmartTargets(prompt SmartTargetPrompt) ([]string, bool, error) {
	prompter.targetCalls++
	prompter.targetPrompt = prompt
	return prompter.targets, prompter.targetsCancelled, prompter.err
}
