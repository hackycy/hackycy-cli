package run

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectScriptPreservesTheLegacyPromptAndScriptHints(t *testing.T) {
	prompter := &recordingRunPrompter{script: "build"}
	scripts := []Script{
		{Name: "check", Command: "go test ./..."},
		{Name: "build", Command: "go build ./cmd/ycy"},
	}

	selected, cancelled, err := selectScript(prompter, scripts)

	if err != nil || cancelled || selected != "build" {
		t.Fatalf("selectScript() = (%q, %t, %v)", selected, cancelled, err)
	}
	want := ScriptPrompt{
		Message: "Select a script to run:",
		Options: []ScriptChoice{
			{Value: "check", Label: "check", Hint: "go test ./..."},
			{Value: "build", Label: "build", Hint: "go build ./cmd/ycy"},
		},
	}
	if !reflect.DeepEqual(prompter.scriptPrompt, want) {
		t.Fatalf("script prompt = %#v, want %#v", prompter.scriptPrompt, want)
	}
}

func TestSelectScriptReportsCancellation(t *testing.T) {
	prompter := &recordingRunPrompter{scriptCancelled: true}
	_, cancelled, err := selectScript(prompter, []Script{{Name: "check", Command: "go test ./..."}})
	if err != nil || !cancelled {
		t.Fatal("selectScript() did not report cancellation")
	}
}

func TestSelectPackageManagerPreservesTheComputedOrder(t *testing.T) {
	prompter := &recordingRunPrompter{manager: PackageManagerExternal}
	order := []PackageManager{PackageManagerExternal, PackageManagerPNPM, PackageManagerNPM, PackageManagerYarn}

	selected, cancelled, err := selectPackageManager(prompter, order)

	if err != nil || cancelled || selected != PackageManagerExternal {
		t.Fatalf("selectPackageManager() = (%q, %t, %v)", selected, cancelled, err)
	}
	want := PackageManagerPrompt{
		Message: "Select a package manager:",
		Options: []PackageManagerChoice{
			{Value: PackageManagerExternal, Label: string(PackageManagerExternal)},
			{Value: PackageManagerPNPM, Label: "pnpm"},
			{Value: PackageManagerNPM, Label: "npm"},
			{Value: PackageManagerYarn, Label: "yarn"},
		},
	}
	if !reflect.DeepEqual(prompter.managerPrompt, want) {
		t.Fatalf("manager prompt = %#v, want %#v", prompter.managerPrompt, want)
	}
}

func TestSelectPackageManagerReportsCancellation(t *testing.T) {
	prompter := &recordingRunPrompter{managerCancelled: true}
	_, cancelled, err := selectPackageManager(prompter, []PackageManager{PackageManagerPNPM})
	if err != nil || !cancelled {
		t.Fatal("selectPackageManager() did not report cancellation")
	}
}

func TestSelectorsReturnPromptFailures(t *testing.T) {
	failure := errors.New("interactive terminal unavailable")
	prompter := &recordingRunPrompter{err: failure}
	if _, cancelled, err := selectScript(prompter, []Script{{Name: "check"}}); cancelled || !errors.Is(err, failure) {
		t.Fatalf("selectScript() = (cancelled=%t, err=%v)", cancelled, err)
	}
	if _, cancelled, err := selectPackageManager(prompter, []PackageManager{PackageManagerNPM}); cancelled || !errors.Is(err, failure) {
		t.Fatalf("selectPackageManager() = (cancelled=%t, err=%v)", cancelled, err)
	}
}

type recordingRunPrompter struct {
	script           string
	scriptCancelled  bool
	manager          PackageManager
	managerCancelled bool
	err              error
	scriptPrompt     ScriptPrompt
	managerPrompt    PackageManagerPrompt
}

func (prompter *recordingRunPrompter) SelectScript(prompt ScriptPrompt) (string, bool, error) {
	prompter.scriptPrompt = prompt
	return prompter.script, prompter.scriptCancelled, prompter.err
}

func (prompter *recordingRunPrompter) SelectPackageManager(prompt PackageManagerPrompt) (PackageManager, bool, error) {
	prompter.managerPrompt = prompt
	return prompter.manager, prompter.managerCancelled, prompter.err
}
