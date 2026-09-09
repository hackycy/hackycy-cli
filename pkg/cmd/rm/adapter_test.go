package rm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalRMAdapterTranslatesPromptClusterAndPresentation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Confirmed: true}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "node-dist"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Values: []string{"/project/dist"}}},
	)
	run := experience.Open(context.Background())
	adapter := newTerminalRMAdapter(run)

	confirmed, cancelled, err := adapter.ConfirmExplicit(ExplicitConfirmationPrompt{Message: "Delete 1 item?"})
	if err != nil || cancelled || !confirmed {
		t.Fatalf("ConfirmExplicit() = (%t, %t, %v)", confirmed, cancelled, err)
	}
	action, cancelled, err := adapter.SelectSmartAction(SmartActionPrompt{Message: "Select a clean action", Options: []SmartAction{{ID: "node-dist", Label: "Node project - delete ./dist"}}})
	if err != nil || cancelled || action.ID != "node-dist" {
		t.Fatalf("SelectSmartAction() = (%#v, %t, %v)", action, cancelled, err)
	}
	targets, cancelled, err := adapter.SelectSmartTargets(SmartTargetPrompt{
		Message:       "Select items to delete",
		Options:       []SmartTargetChoice{{Value: "/project/dist", Label: "dist"}},
		InitialValues: []string{"/project/dist"},
	})
	if err != nil || cancelled || !reflect.DeepEqual(targets, []string{"/project/dist"}) {
		t.Fatalf("SelectSmartTargets() = (%#v, %t, %v)", targets, cancelled, err)
	}
	adapter.Intro("Remove")
	adapter.Paths([]string{"/project/dist"})
	adapter.Notice("  not found, skipping: /project/missing")
	adapter.ProgressStart("Scanning...")
	adapter.ProgressStop("Found 1 target")
	adapter.Cancel("Cancelled.")
	adapter.Outro("Done!")
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 11 || operations[0].Kind != terminaltest.AskOperation || operations[3].Kind != terminaltest.NoticeOperation || operations[8].Kind != terminaltest.ResultOperation || operations[9].Kind != terminaltest.ResultOperation || operations[10].Kind != terminaltest.CloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	confirm := operations[0].Value.(terminalexperience.InteractionRequest)
	if confirm.Kind != terminalexperience.InteractionConfirm || confirm.Message != "Delete 1 item?" || confirm.ConsoleStepID != rmExplicitConfirmationFormID || !confirm.HasDefault || confirm.Default.Confirmed {
		t.Fatalf("confirmation request = %#v", confirm)
	}
	actionRequest := operations[1].Value.(terminalexperience.InteractionRequest)
	if actionRequest.Kind != terminalexperience.InteractionSelect || actionRequest.Message != "Select a clean action" || actionRequest.ConsoleStepID != rmSmartActionFormID || !actionRequest.HasDefault || actionRequest.Default.Value != "node-dist" || !reflect.DeepEqual(actionRequest.CancelValues, []string{"q", "quit", "cancel"}) {
		t.Fatalf("action request = %#v", actionRequest)
	}
	targetRequest := operations[2].Value.(terminalexperience.InteractionRequest)
	if targetRequest.Kind != terminalexperience.InteractionMultiSelect || targetRequest.Message != "Select items to delete" || targetRequest.ConsoleStepID != rmSmartTargetsFormID || !targetRequest.HasDefault || !reflect.DeepEqual(targetRequest.Default.Values, []string{"/project/dist"}) {
		t.Fatalf("target request = %#v", targetRequest)
	}
	intro := operations[3].Value.(terminalexperience.PresentationDocument)
	if !reflect.DeepEqual(intro.Blocks, []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleTitle, Text: "HACKYCY CLI"}, {Role: terminalexperience.VisualRoleActive, Text: "Remove"}}) {
		t.Fatalf("intro document = %#v", intro)
	}
	if got := operations[9].Value.(terminalexperience.PresentationDocument).Blocks[0].Role; got != terminalexperience.VisualRoleSuccess {
		t.Fatalf("success role = %v", got)
	}
}

func TestTerminalRMAdapterPlainPreservesLegacyInputGrammarAndMutationBoundaries(t *testing.T) {
	root := t.TempDir()
	confirmed := writeStandaloneRMFile(t, root, "confirmed.txt")
	cancelledTarget := writeStandaloneRMFile(t, root, "cancelled.txt")

	for _, testCase := range []struct {
		name       string
		input      string
		target     string
		shouldGone bool
		contains   []string
	}{
		{name: "confirmed", input: "maybe\nyes\n", target: confirmed, shouldGone: true, contains: []string{"Invalid confirmation", "Delete 1 item? [y/N]:", "Deleted 1 item", "Done!"}},
		{name: "declined", input: "\n", target: cancelledTarget, contains: []string{"Delete 1 item? [y/N]:", "Cancelled."}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
				Input:        strings.NewReader(testCase.input),
				Output:       stdout,
				Diagnostics:  diagnostics,
			})
			withRMWorkingDirectory(t, root)

			err := runRMForTest(context.Background(), experience, Input{Paths: []string{filepath.Base(testCase.target)}})
			if err != nil {
				t.Fatalf("runRM() error = %v", err)
			}
			allOutput := append(append([]byte{}, stdout.Bytes()...), diagnostics.Bytes()...)
			if terminaltest.ContainsTerminalControl(allOutput) {
				t.Fatalf("Plain streams contain terminal control: (%q, %q)", stdout.String(), diagnostics.String())
			}
			for _, want := range testCase.contains {
				if !strings.Contains(stdout.String()+diagnostics.String(), want) {
					t.Fatalf("streams = (%q, %q), missing %q", stdout.String(), diagnostics.String(), want)
				}
			}
			_, statErr := os.Stat(testCase.target)
			if testCase.shouldGone && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("confirmed target = %v, want missing", statErr)
			}
			if !testCase.shouldGone && statErr != nil {
				t.Fatalf("declined target = %v, want retained", statErr)
			}
		})
	}
}

func TestRMAutomationPreservesForceAndNoTargetPathsAndFailsPromptPathsBeforeEffects(t *testing.T) {
	root := t.TempDir()
	forcedTarget := writeStandaloneRMFile(t, root, "forced.txt")
	promptTarget := writeStandaloneRMFile(t, root, "prompt.txt")
	withRMWorkingDirectory(t, root)
	for _, testCase := range []struct {
		name      string
		input     Input
		wantOut   string
		wantError error
	}{
		{name: "force explicit", input: Input{Paths: []string{"forced.txt"}, Force: true}, wantOut: "Deleted 1 item\nDone!\n"},
		{name: "missing explicit", input: Input{Paths: []string{"missing.txt"}}, wantOut: "  not found, skipping: missing.txt\nNo valid paths to delete.\n"},
		{name: "explicit confirmation", input: Input{Paths: []string{"prompt.txt"}}, wantError: errRMRequiresInteractive},
		{name: "smart selection", input: Input{}, wantError: errRMRequiresInteractive},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
				Input:        panicRMReader{},
				Output:       stdout,
				Diagnostics:  diagnostics,
			})
			err := runRMForTest(context.Background(), experience, testCase.input)
			if testCase.wantError == nil {
				if err != nil || stdout.String() != testCase.wantOut || diagnostics.Len() != 0 {
					t.Fatalf("Automation error = %v, streams = (%q, %q)", err, stdout.String(), diagnostics.String())
				}
				return
			}
			if !errors.Is(err, testCase.wantError) || stdout.Len() != 0 || diagnostics.Len() != 0 {
				t.Fatalf("Automation failure = (%v), streams = (%q, %q)", err, stdout.String(), diagnostics.String())
			}
		})
	}
	if _, err := os.Stat(promptTarget); err != nil {
		t.Fatalf("Automation prompt failure changed target: %v", err)
	}
	if _, err := os.Stat(forcedTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("forced target = %v, want missing", err)
	}
}

func runRMForTest(context context.Context, experience *terminalexperience.Runtime, input Input) error {
	return runRM(&Options{
		Context:          context,
		Paths:            input.Paths,
		Force:            input.Force,
		Depth:            input.Depth,
		WorkingDirectory: os.Getwd,
		Terminal:         experience,
		Remover:          osRMRemover{},
	})
}

type panicRMReader struct{}

func (panicRMReader) Read([]byte) (int, error) {
	panic("rm attempted to read Automation input")
}
