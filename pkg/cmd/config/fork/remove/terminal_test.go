package remove

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalForkRemoveAdapterTranslatesSelectionAndConfirmation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "work"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Confirmed: true}},
	)
	run := experience.Open(context.Background())
	adapter := newTerminalForkRemoveAdapter(run)

	selected, cancelled, err := adapter.Select(SelectPrompt{
		Message: "Select instance to remove",
		Choices: []Choice{
			{Value: "work", Label: "work", Description: "gitlab.example"},
			{Value: "personal", Label: "personal", Description: "github.example"},
		},
	})
	if err != nil || cancelled || selected != "work" {
		t.Fatalf("Select() = (%q, %t, %v)", selected, cancelled, err)
	}
	confirmed, cancelled, err := adapter.Confirm(ConfirmPrompt{Message: `Remove instance "work"?`})
	if err != nil || cancelled || !confirmed {
		t.Fatalf("Confirm() = (%t, %t, %v)", confirmed, cancelled, err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 3 || operations[0].Kind != terminaltest.AskOperation || operations[1].Kind != terminaltest.AskOperation || operations[2].Kind != terminaltest.CloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	selection := operations[0].Value.(terminalexperience.InteractionRequest)
	if selection.Kind != terminalexperience.InteractionSelect || selection.Message != "Select instance to remove" || selection.ConsoleStepID != forkRemoveSelectionFormID || selection.TranscriptLabel != "Selected instance" || selection.TranscriptProject == nil || !selection.HasDefault || selection.Default.Value != "work" || !reflect.DeepEqual(selection.Options, []terminalexperience.InteractionOption{{Label: "work", Value: "work", Description: "gitlab.example"}, {Label: "personal", Value: "personal", Description: "github.example"}}) {
		t.Fatalf("selection request = %#v", selection)
	}
	if got := selection.TranscriptProject(terminalexperience.InteractionAnswer{Value: "unsafe\nname"}); got != "Selected instance" {
		t.Fatalf("selection transcript projection = %q", got)
	}
	confirmation := operations[1].Value.(terminalexperience.InteractionRequest)
	if confirmation.Kind != terminalexperience.InteractionConfirm || confirmation.Message != `Remove instance "work"?` || confirmation.ConsoleStepID != forkRemoveConfirmationFormID || confirmation.TranscriptProject == nil || !confirmation.HasDefault || confirmation.Default.Confirmed {
		t.Fatalf("confirmation request = %#v", confirmation)
	}
	if got := confirmation.TranscriptProject(terminalexperience.InteractionAnswer{Confirmed: true}); got != "" {
		t.Fatalf("confirmation transcript projection = %q, want empty milestone placeholder", got)
	}
}

func TestConfigForkRemoveConsoleDescriptorProvidesSafeBoundedContext(t *testing.T) {
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / config fork remove",
		Target:  "Remove fork provider instance - Choose a configured provider connection to remove",
		Status:  "READY",
		FormCatalog: []terminalexperience.ConsoleFormStep{
			{ID: forkRemoveSelectionFormID, Name: "Selection", Detail: "provider instance"},
			{ID: forkRemoveConfirmationFormID, Name: "Confirmation", Detail: "default No"},
		},
	}
	if got := terminalForkRemoveConsoleDescriptor(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
	for _, field := range []string{want.Command, want.Target, want.Status} {
		if strings.ContainsAny(field, "\r\n\t\x1b") {
			t.Fatalf("descriptor field contains terminal control: %q", field)
		}
	}
}

func TestTerminalForkRemoveAdapterMapsTerminalCancellation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrInteractionCancelled})
	adapter := newTerminalForkRemoveAdapter(experience.Open(context.Background()))

	value, cancelled, err := adapter.Select(SelectPrompt{Message: "Select instance to remove"})
	if err != nil || !cancelled || value != "" {
		t.Fatalf("Select() = (%q, %t, %v)", value, cancelled, err)
	}

	contextExperience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: context.Canceled})
	contextAdapter := newTerminalForkRemoveAdapter(contextExperience.Open(context.Background()))
	if _, cancelled, err := contextAdapter.Confirm(ConfirmPrompt{Message: `Remove instance "work"?`}); !errors.Is(err, context.Canceled) || cancelled {
		t.Fatalf("context Confirm() = (cancelled=%t, err=%v), want original context error", cancelled, err)
	}
}

func TestTerminalForkRemovePresentationUsesTheSharedOutputBoundary(t *testing.T) {
	var output, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       &output,
		Diagnostics:  &diagnostics,
	})
	run := experience.Open(context.Background())
	adapter := newTerminalForkRemoveAdapter(run)
	adapter.Info("No instances configured")
	adapter.Outcome("Nothing to remove")
	adapter.Outcome("Cancelled")
	adapter.Outcome("Instance work removed")
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got, want := output.String(), "Nothing to remove\nCancelled\nInstance work removed\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if got, want := diagnostics.String(), "No instances configured\n"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
	if terminaltest.ContainsTerminalControl(append(output.Bytes(), diagnostics.Bytes()...)) {
		t.Fatalf("plain streams contain terminal control: (%q, %q)", output.String(), diagnostics.String())
	}
	for _, testCase := range []struct {
		message string
		role    terminalexperience.VisualRole
	}{
		{message: "No instances configured", role: terminalexperience.VisualRoleMuted},
		{message: "Cancelled", role: terminalexperience.VisualRoleWarning},
		{message: "Instance work removed", role: terminalexperience.VisualRoleSuccess},
	} {
		document := terminalForkRemoveDocument(testCase.message, testCase.role)
		if got := document.Blocks[0].Role; got != testCase.role {
			t.Fatalf("Rich role = %v, want %v", got, testCase.role)
		}
	}
}

func TestTerminalForkRemoveAdapterReportsAutomationInteraction(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrAutomationInteraction})
	adapter := newTerminalForkRemoveAdapter(experience.Open(context.Background()))
	if _, _, err := adapter.Select(SelectPrompt{Message: "Select instance to remove"}); !errors.Is(err, errConfigForkRemoveRequiresInteractive) {
		t.Fatalf("Select() error = %v", err)
	}
}

func TestConfigForkRemoveAutomationPreservesEmptyAndRejectsNonemptyBeforeMutation(t *testing.T) {
	t.Run("empty configuration", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", "")
		stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
			Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
			Input:        panicForkRemoveReader{},
			Output:       stdout,
			Diagnostics:  diagnostics,
		})

		result, err := executeRemove(newRemoveOptions(experience))
		if err != nil || result != (RemoveResult{Empty: true}) {
			t.Fatalf("executeRemove() = (%#v, %v)", result, err)
		}
		if got, want := stdout.String(), "Nothing to remove\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if diagnostics.Len() != 0 || terminaltest.ContainsTerminalControl(stdout.Bytes()) {
			t.Fatalf("Automation streams = (%q, %q)", stdout.String(), diagnostics.String())
		}
		if _, err := os.Stat(filepath.Join(home, ".ycy-cli", "config.json")); !os.IsNotExist(err) {
			t.Fatalf("empty removal created configuration: %v", err)
		}
	})

	t.Run("nonempty configuration", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", "")
		configPath := writeForkRemoveConfig(t, home)
		before, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
			Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
			Input:        panicForkRemoveReader{},
			Output:       stdout,
			Diagnostics:  diagnostics,
		})

		result, err := executeRemove(newRemoveOptions(experience))
		if !errors.Is(err, errConfigForkRemoveRequiresInteractive) || result != (RemoveResult{}) {
			t.Fatalf("executeRemove() = (%#v, %v)", result, err)
		}
		if stdout.Len() != 0 || diagnostics.Len() != 0 {
			t.Fatalf("Automation streams = (%q, %q)", stdout.String(), diagnostics.String())
		}
		after, err := os.ReadFile(configPath)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("Automation failure changed configuration = (%v, %q)", err, after)
		}
	})
}

func TestConfigForkRemovePlainConfirmationAndCancellationPreserveMutationBoundary(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		input          string
		wantOutput     string
		wantResult     RemoveResult
		wantDiagnostic string
		removed        bool
	}{
		{name: "confirmed", input: "1\ny\n", wantOutput: "Instance work removed\n", removed: true},
		{name: "declined after invalid selection", input: "invalid\n1\n\n", wantOutput: "Cancelled\n", wantResult: RemoveResult{Declined: true}, wantDiagnostic: "invalid selection"},
		{name: "declined after invalid confirmation", input: "1\nmaybe\n\n", wantOutput: "Cancelled\n", wantResult: RemoveResult{Declined: true}, wantDiagnostic: "please answer yes or no"},
		{name: "cancelled selection", input: "", wantOutput: "Cancelled\n", wantResult: RemoveResult{Cancelled: true}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", "")
			configPath := writeForkRemoveConfig(t, home)
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
				Input:        strings.NewReader(testCase.input),
				Output:       stdout,
				Diagnostics:  diagnostics,
			})

			result, err := executeRemove(newRemoveOptions(experience))
			if err != nil || result != testCase.wantResult {
				t.Fatalf("executeRemove() = (%#v, %v), want (%#v, nil)", result, err, testCase.wantResult)
			}
			if got := stdout.String(); got != testCase.wantOutput {
				t.Fatalf("stdout = %q, want %q", got, testCase.wantOutput)
			}
			if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), diagnostics.Bytes()...)) {
				t.Fatalf("Plain output contains terminal control: (%q, %q)", stdout.String(), diagnostics.String())
			}
			if testCase.wantDiagnostic != "" && !strings.Contains(diagnostics.String(), testCase.wantDiagnostic) {
				t.Fatalf("diagnostics = %q, missing %q", diagnostics.String(), testCase.wantDiagnostic)
			}
			after, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read result configuration: %v", err)
			}
			if testCase.removed {
				if bytes.Equal(after, before) || bytes.Contains(after, []byte(`"work"`)) {
					t.Fatalf("confirmed result changed configuration = %q", after)
				}
				return
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("non-mutating result changed configuration: %q", after)
			}
		})
	}
}

func TestConfigForkRemoveTreatsAlreadyMissingWriteAsSuccess(t *testing.T) {
	var output, diagnostics bytes.Buffer
	var names []string
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("1\ny\n"),
		Output:       &output,
		Diagnostics:  &diagnostics,
	})
	result, err := executeRemove(&Options{
		Context:  context.Background(),
		Terminal: experience,
		Store: func() (RemoveReader, RemoveWriter, error) {
			return forkRemoveReaderFunc(func() ([]appconfig.ForkInstance, error) {
					return []appconfig.ForkInstance{{Name: "work", Host: "gitlab.example"}}, nil
				}), forkRemoveWriterFunc(func(name string) (bool, error) {
					names = append(names, name)
					return false, nil
				}), nil
		},
	})
	if err != nil || result != (RemoveResult{}) {
		t.Fatalf("executeRemove() = (%#v, %v), want success", result, err)
	}
	if got, want := names, []string{"work"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RemoveForkInstance names = %#v, want %#v", got, want)
	}
	if got, want := output.String(), "Instance work removed\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	for _, expected := range []string{"Loading fork provider instances...", "Loaded fork provider instances", "Removing provider instance..."} {
		if !strings.Contains(diagnostics.String(), expected) {
			t.Fatalf("diagnostics = %q, missing %q", diagnostics.String(), expected)
		}
	}
}

func TestConfigForkRemoveContextCancellationDuringLoadPreservesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var writes int
	var output, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        panicForkRemoveReader{},
		Output:       &output,
		Diagnostics:  &diagnostics,
	})
	_, err := executeRemove(&Options{
		Context:  ctx,
		Terminal: experience,
		Store: func() (RemoveReader, RemoveWriter, error) {
			return forkRemoveReaderFunc(func() ([]appconfig.ForkInstance, error) {
					cancel()
					return []appconfig.ForkInstance{{Name: "work", Host: "gitlab.example"}}, nil
				}), forkRemoveWriterFunc(func(string) (bool, error) {
					writes++
					return true, nil
				}), nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("executeRemove() error = %v, want context cancellation", err)
	}
	if writes != 0 || output.Len() != 0 {
		t.Fatalf("context cancellation mutated/result: writes=%d stdout=%q", writes, output.String())
	}
	if got := diagnostics.String(); !strings.Contains(got, "Loading fork provider instances...") || strings.Contains(got, "Loaded fork provider instances") {
		t.Fatalf("load cancellation diagnostics = %q", got)
	}
}

func TestConfigForkRemoveRejectsNilStoreAdapters(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		store StoreProvider
		want  string
	}{
		{name: "reader", store: func() (RemoveReader, RemoveWriter, error) {
			return nil, forkRemoveWriterFunc(func(string) (bool, error) { return true, nil }), nil
		}, want: "config fork remove reader is nil"},
		{name: "writer", store: func() (RemoveReader, RemoveWriter, error) {
			return forkRemoveReaderFunc(func() ([]appconfig.ForkInstance, error) { return nil, nil }), nil, nil
		}, want: "config fork remove writer is nil"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
				Output:       &output,
			})
			result, err := executeRemove(&Options{Context: context.Background(), Store: testCase.store, Terminal: experience})
			if err == nil || !strings.Contains(err.Error(), testCase.want) || result != (RemoveResult{}) {
				t.Fatalf("executeRemove() = (%#v, %v), want %q", result, err, testCase.want)
			}
			if output.Len() != 0 {
				t.Fatalf("nil adapter emitted result: %q", output.String())
			}
		})
	}
}

func TestForkRemovePhaseSinkUsesOneWorkCatalogForLoadAndRemoval(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	sink := newForkRemovePhaseSink(run, terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive})
	if err := sink.beginLoad(); err != nil {
		t.Fatalf("beginLoad() error = %v", err)
	}
	if err := sink.endLoad(terminalexperience.PhaseCompleted, "Loaded 1 provider instance"); err != nil {
		t.Fatalf("endLoad() error = %v", err)
	}
	if err := sink.beginRemoval(); err != nil {
		t.Fatalf("beginRemoval() error = %v", err)
	}
	if err := sink.endRemoval(terminalexperience.PhaseCompleted, "Provider instance removed"); err != nil {
		t.Fatalf("endRemoval() error = %v", err)
	}
	if err := sink.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}

	operations := experience.Run.Operations()
	var startWork, workClose int
	var updates []terminalexperience.OperationPhase
	for _, operation := range operations {
		switch operation.Kind {
		case terminaltest.StartWorkOperation:
			startWork++
			if got, want := operation.Value.(terminalexperience.WorkCatalog), terminalForkRemoveWorkCatalog(); !reflect.DeepEqual(got, want) {
				t.Fatalf("Work catalog = %#v, want %#v", got, want)
			}
		case terminaltest.WorkUpdateOperation:
			updates = append(updates, operation.Value.(terminalexperience.OperationPhase))
		case terminaltest.WorkCloseOperation:
			workClose++
		case terminaltest.TrackOperation:
			t.Fatalf("legacy Track operation = %#v, want one controlled Work Catalog", operations)
		}
	}
	if startWork != 1 || workClose != 1 {
		t.Fatalf("operations = %#v, startWork=%d workClose=%d", operations, startWork, workClose)
	}
	if got, want := updates, []terminalexperience.OperationPhase{
		{ID: forkRemoveLoadPhaseID, State: terminalexperience.PhaseActive, Detail: "Reading provider configuration"},
		{ID: forkRemoveLoadPhaseID, State: terminalexperience.PhaseCompleted, Detail: "Loaded 1 provider instance"},
		{ID: forkRemovePhaseID, State: terminalexperience.PhaseActive, Detail: "Deleting stored provider instance"},
		{ID: forkRemovePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Provider instance removed"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("work updates = %#v, want %#v", got, want)
	}
}

func TestForkRemoveFinishRequestUsesSafeOutcomeSummaries(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		request terminalexperience.FinishRequest
	}{
		{
			name: "successful removal",
			request: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Succeeded,
				Summary: terminalForkRemoveDocument("Provider instance removed", terminalexperience.VisualRoleSuccess),
			},
		},
		{
			name: "empty configuration",
			request: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Succeeded,
				Location: forkRemoveLoadPhaseName,
				Summary:  terminalForkRemoveDocument("No instances configured", terminalexperience.VisualRoleSuccess),
			},
		},
		{
			name: "cancelled interaction",
			request: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Cancelled,
				Summary: terminalForkRemoveDocument("Provider removal cancelled", terminalexperience.VisualRoleWarning),
			},
		},
		{
			name: "load failure",
			request: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Failed,
				Location: forkRemoveLoadPhaseName,
				Summary:  terminalForkRemoveDocument("Unable to load fork provider instances", terminalexperience.VisualRoleError),
			},
		},
		{
			name: "removal failure",
			request: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Failed,
				Location: forkRemovePhaseName,
				Summary:  terminalForkRemoveDocument("Unable to remove provider instance", terminalexperience.VisualRoleError),
			},
		},
		{
			name: "invalid failure location",
			request: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Failed,
				Summary: terminalForkRemoveDocument("Provider removal failed", terminalexperience.VisualRoleError),
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var got terminalexperience.FinishRequest
			switch testCase.name {
			case "successful removal":
				got = terminalForkRemoveFinishRequest(terminalexperience.Succeeded, "", false)
			case "empty configuration":
				got = terminalForkRemoveFinishRequest(terminalexperience.Succeeded, forkRemoveLoadPhaseName, true)
			case "cancelled interaction":
				got = terminalForkRemoveFinishRequest(terminalexperience.Cancelled, "", false)
			case "load failure":
				got = terminalForkRemoveFinishRequest(terminalexperience.Failed, forkRemoveLoadPhaseName, false)
			case "removal failure":
				got = terminalForkRemoveFinishRequest(terminalexperience.Failed, forkRemovePhaseName, false)
			case "invalid failure location":
				got = terminalForkRemoveFinishRequest(terminalexperience.Failed, "unsafe\nlocation", false)
			}
			if !reflect.DeepEqual(got, testCase.request) {
				t.Fatalf("Finish request = %#v, want %#v", got, testCase.request)
			}
		})
	}
}

func TestForkRemoveSafetyProjectionsStripUnsafeHostAndNameValues(t *testing.T) {
	host := "https://user:password@example.test/path?token=hidden#fragment"
	if got, want := safeForkRemoveHost(host), "example.test/path"; got != want {
		t.Fatalf("safeForkRemoveHost() = %q, want %q", got, want)
	}
	if got := safeForkRemoveHost("unsafe\nhost"); got != "Host configured" {
		t.Fatalf("unsafe host projection = %q", got)
	}
	if got := safeForkRemoveName("unsafe\nname"); got != "Selected instance" {
		t.Fatalf("unsafe name projection = %q", got)
	}
	if got := forkRemoveSuccessMessage("unsafe\nname"); got != "Instance removed" {
		t.Fatalf("unsafe success message = %q", got)
	}
}

func writeForkRemoveConfig(t *testing.T, home string) string {
	t.Helper()
	directory := filepath.Join(home, ".ycy-cli")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	path := filepath.Join(directory, "config.json")
	contents := `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "fork": {"instances": {
    "work": {"host": "gitlab.example", "type": "gitlab", "token": "MDEyMzQ1Njc4OWFiY2RlZg==:tag:ciphertext"},
    "keep": {"host": "github.example", "type": "github", "token": "QWVy:second:ciphertext"}
  }}
}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return path
}

type panicForkRemoveReader struct{}

func (panicForkRemoveReader) Read([]byte) (int, error) {
	panic("config fork remove attempted to read Automation input")
}

type forkRemoveReaderFunc func() ([]appconfig.ForkInstance, error)

func (function forkRemoveReaderFunc) ListForkInstances() ([]appconfig.ForkInstance, error) {
	return function()
}

type forkRemoveWriterFunc func(string) (bool, error)

func (function forkRemoveWriterFunc) RemoveForkInstance(name string) (bool, error) {
	return function(name)
}

func newRemoveOptions(experience *terminalexperience.Runtime) *Options {
	return &Options{
		Context:  context.Background(),
		Terminal: experience,
		Store: func() (RemoveReader, RemoveWriter, error) {
			store, err := appconfig.New(appconfig.Dependencies{})
			if err != nil {
				return nil, nil, err
			}
			return store, store, nil
		},
	}
}
