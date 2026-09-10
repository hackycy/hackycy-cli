package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalExportEnvAdapterTranslatesSelectionAndPresentation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: ".env.production"}})
	run := experience.Open(context.Background())
	adapter := newTerminalExportEnvAdapter(run, false)
	choices := []EnvironmentChoice{
		{Value: ".env", Label: "default"},
		{Value: ".env.production", Label: "production"},
	}

	value, cancelled, err := adapter.SelectEnvironment("Select environment", choices)
	if err != nil || cancelled || value != ".env.production" {
		t.Fatalf("SelectEnvironment() = (%q, %t, %v)", value, cancelled, err)
	}
	adapter.Outro("Exported variables:")
	adapter.Print("{\n  \"VALUE\": \"production\"\n}")
	adapter.Cancel("Cancelled")
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 5 || operations[0].Kind != terminaltest.AskOperation || operations[4].Kind != terminaltest.CloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	request := operations[0].Value.(terminalexperience.InteractionRequest)
	if request.Kind != terminalexperience.InteractionSelect || request.Message != "Select environment" || request.HasDefault || !reflect.DeepEqual(request.Options, []terminalexperience.InteractionOption{
		{Value: ".env", Label: "default", Description: ".env"},
		{Value: ".env.production", Label: "production", Description: ".env.production"},
	}) || !reflect.DeepEqual(request.CancelValues, []string{"", "q", "quit", "cancel"}) || request.ConsoleStepID != exportEnvSelectFormID || request.TranscriptLabel != "Selected environment" {
		t.Fatalf("selection request = %#v", request)
	}
	for index, want := range []terminalexperience.PresentationDocument{
		{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleMuted, Text: "Exported variables:"}}},
		{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRolePlain, Text: "{\n  \"VALUE\": \"production\"\n}"}}},
		{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "Cancelled"}}},
	} {
		if operations[index+1].Kind != terminaltest.ResultOperation || !reflect.DeepEqual(operations[index+1].Value, want) {
			t.Fatalf("presentation %d = %#v, want %#v", index, operations[index+1], want)
		}
	}
}

func TestExportEnvConsoleDescriptorProvidesBoundedSafeContext(t *testing.T) {
	options := &Options{Directory: " ./workspace ", Merge: true}
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / export env",
		Target:  "environment JSON",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "directory", Value: "./workspace"},
			{Label: "merge base .env", Value: "on"},
		},
		FormCatalog: []terminalexperience.ConsoleFormStep{{
			ID:     exportEnvSelectFormID,
			Name:   "Select environment",
			Detail: "choose an environment file",
		}},
	}
	if got := terminalExportEnvConsoleDescriptor(options); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}

	unsafe := terminalExportEnvConsoleDescriptor(&Options{Directory: "secret\x1b[31m\nvalue"})
	for _, field := range append([]string{unsafe.Command, unsafe.Target, unsafe.Status}, func() []string {
		values := make([]string, 0, len(unsafe.Metadata)*2)
		for _, metadata := range unsafe.Metadata {
			values = append(values, metadata.Label, metadata.Value)
		}
		return values
	}()...) {
		if strings.ContainsAny(field, "\r\n\t\x1b") {
			t.Fatalf("unsafe descriptor field contains terminal control: %q", field)
		}
	}
}

func TestTerminalExportEnvAdapterRoutesPlainSelectionValidationAndCancellation(t *testing.T) {
	choices := []EnvironmentChoice{
		{Value: ".env.local", Label: "local"},
		{Value: ".env.production", Label: "production"},
	}
	stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("invalid\n2\n"),
		Output:       stdout,
		Diagnostics:  diagnostics,
	})
	run := experience.Open(context.Background())
	adapter := newTerminalExportEnvAdapter(run, experience.Capabilities().Interaction == terminalexperience.Automation)
	value, cancelled, err := adapter.SelectEnvironment("Select environment", choices)
	if err != nil || cancelled || value != ".env.production" {
		t.Fatalf("SelectEnvironment() = (%q, %t, %v)", value, cancelled, err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if stdout.Len() != 0 || !strings.Contains(diagnostics.String(), "invalid selection") || terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
		t.Fatalf("Plain streams = (%q, %q)", stdout.String(), diagnostics.String())
	}

	cancelledExperience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("quit\n"),
		Diagnostics:  &bytes.Buffer{},
	})
	cancelledRun := cancelledExperience.Open(context.Background())
	cancelledAdapter := newTerminalExportEnvAdapter(cancelledRun, cancelledExperience.Capabilities().Interaction == terminalexperience.Automation)
	value, cancelled, err = cancelledAdapter.SelectEnvironment("Select environment", choices)
	if err != nil || !cancelled || value != "" {
		t.Fatalf("cancelled SelectEnvironment() = (%q, %t, %v)", value, cancelled, err)
	}
	if err := cancelledRun.Close(); err != nil {
		t.Fatalf("cancelled Close() error = %v", err)
	}
}

func TestTerminalExportEnvAdapterPreservesAutomationResolutionAndRejectsInteraction(t *testing.T) {
	uniqueExperience := terminaltest.NewRecordingExperience()
	uniqueAdapter := newTerminalExportEnvAdapter(uniqueExperience.Open(context.Background()), true)
	value, cancelled, err := uniqueAdapter.SelectEnvironment("Select environment", []EnvironmentChoice{{Value: ".env.production", Label: "production"}})
	if err != nil || cancelled || value != ".env.production" || len(uniqueExperience.Run.Operations()) != 0 {
		t.Fatalf("unique Automation selection = (%q, %t, %v), operations=%#v", value, cancelled, err, uniqueExperience.Run.Operations())
	}

	automationExperience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrAutomationInteraction})
	automationAdapter := newTerminalExportEnvAdapter(automationExperience.Open(context.Background()), true)
	if _, _, err := automationAdapter.SelectEnvironment("Select environment", []EnvironmentChoice{{Value: ".env", Label: "default"}, {Value: ".env.production", Label: "production"}}); !errors.Is(err, errExportEnvRequiresInteractive) {
		t.Fatalf("ambiguous Automation selection error = %v", err)
	}
}

func TestTerminalExportEnvPhaseSinkUsesOneWorkCatalogAndSafeMilestones(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	work, err := terminalexperience.StartWork(run, terminalExportEnvWorkCatalog(false))
	if err != nil {
		t.Fatalf("StartWork() error = %v", err)
	}
	sink := newExportEnvPhaseSink(run, work, terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive}, false)
	sink.phase("resolve-directory", "Resolve directory", terminalPhaseActive, "")
	sink.phase("resolve-directory", "Resolve directory", terminalPhaseSucceeded, "Directory ready")
	sink.phase("discover-environment-files", "Discover environment files", terminalPhaseActive, "")
	sink.phase("discover-environment-files", "Discover environment files", terminalPhaseSucceeded, "Found 2 environment files")
	sink.selected(Selection{Files: []string{".env", ".env.production"}}, "user selection", true)
	sink.variables(2)
	sink.phase("read-selected-files", "Read selected files", terminalPhaseActive, "")
	sink.phase("read-selected-files", "Read selected files", terminalPhaseSucceeded, "Read 2 files")
	sink.phase("parse-and-merge-values", "Parse and merge values", terminalPhaseActive, "")
	sink.phase("parse-and-merge-values", "Parse and merge values", terminalPhaseSucceeded, "Parsed 2 variables")
	sink.phase("encode-json", "Encode JSON", terminalPhaseActive, "")
	sink.phase("encode-json", "Encode JSON", terminalPhaseSucceeded, "JSON ready")
	if err := sink.close(); err != nil {
		t.Fatalf("sink.close() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	operations := experience.Run.Operations()
	var startWork, workClose, milestones int
	var updates []terminalexperience.OperationPhase
	for _, operation := range operations {
		switch operation.Kind {
		case terminaltest.StartWorkOperation:
			startWork++
			catalog := operation.Value.(terminalexperience.WorkCatalog)
			if !reflect.DeepEqual(catalog, terminalExportEnvWorkCatalog(false)) {
				t.Fatalf("Work catalog = %#v, want %#v", catalog, terminalExportEnvWorkCatalog(false))
			}
		case terminaltest.WorkUpdateOperation:
			updates = append(updates, operation.Value.(terminalexperience.OperationPhase))
		case terminaltest.WorkCloseOperation:
			workClose++
		case terminaltest.MilestoneOperation:
			milestones++
			if strings.Contains(fmt.Sprint(operation.Value), "do-not-project") {
				t.Fatalf("milestone leaked secret: %#v", operation.Value)
			}
		case terminaltest.TrackOperation:
			t.Fatalf("legacy Track operation = %#v, want one controlled Work Catalog", operations)
		}
	}
	if startWork != 1 || workClose != 1 || milestones != 2 {
		t.Fatalf("operations = %#v, startWork=%d workClose=%d milestones=%d", operations, startWork, workClose, milestones)
	}
	if len(updates) != 10 {
		t.Fatalf("Work updates = %#v, want ten transitions for one five-phase catalog", updates)
	}
}

func TestTerminalExportEnvFinishRequestSeparatesSafeOutcomeFromDurableResult(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		outcome  terminalexperience.FinishOutcome
		location string
		count    int
		hasCount bool
		withOut  bool
		want     terminalexperience.FinishRequest
	}{
		{
			name:     "JSON success",
			outcome:  terminalexperience.Succeeded,
			count:    2,
			hasCount: true,
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Succeeded,
				Summary: terminalExportEnvVariableDocument(2),
			},
		},
		{
			name:    "output success",
			outcome: terminalexperience.Succeeded,
			withOut: true,
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Succeeded,
				Summary: terminalExportEnvResultDocument("Environment export written", terminalexperience.VisualRoleSuccess),
			},
		},
		{
			name:     "selection cancellation",
			outcome:  terminalexperience.Cancelled,
			location: "Select environment",
			want: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Cancelled,
				Location: "Select environment",
				Summary:  terminalExportEnvResultDocument("Environment export cancelled", terminalexperience.VisualRoleWarning),
			},
		},
		{
			name:     "read failure",
			outcome:  terminalexperience.Failed,
			location: "Read selected files",
			want: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Failed,
				Location: "Read selected files",
				Summary:  terminalExportEnvResultDocument("Unable to export environment", terminalexperience.VisualRoleError),
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := terminalExportEnvFinishRequest(testCase.outcome, testCase.location, testCase.count, testCase.hasCount, testCase.withOut)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("Finish request = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestTerminalExportEnvPresentationPreservesPlainAndAutomationResults(t *testing.T) {
	for _, session := range []terminalexperience.Capabilities{
		{Interaction: terminalexperience.PlainInteractive},
		{Interaction: terminalexperience.Automation},
	} {
		var output bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
		run := experience.Open(context.Background())
		adapter := newTerminalExportEnvAdapter(run, session.Interaction == terminalexperience.Automation)
		adapter.Outro("Exported variables:")
		adapter.Print("{\n  \"VALUE\": \"production\"\n}")
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got, want := output.String(), "Exported variables:\n{\n  \"VALUE\": \"production\"\n}\n"; got != want {
			t.Fatalf("%v output = %q, want %q", session.Interaction, got, want)
		}
		if terminaltest.ContainsTerminalControl(output.Bytes()) {
			t.Fatalf("%v output contains terminal control: %q", session.Interaction, output.String())
		}
	}

	for _, testCase := range []struct {
		role terminalexperience.VisualRole
	}{
		{role: terminalexperience.VisualRoleMuted},
		{role: terminalexperience.VisualRolePlain},
		{role: terminalexperience.VisualRoleWarning},
	} {
		document := terminalExportEnvDocument("result", testCase.role)
		if got := document.Blocks[0].Role; got != testCase.role {
			t.Fatalf("Rich role = %v, want %v", got, testCase.role)
		}
	}
}
