package rm

import (
	"context"
	"reflect"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRMConsoleDescriptorProvidesSafeRouteAndModeContext(t *testing.T) {
	force := true
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / rm",
		Target:  "Remove selected files or clean project artifacts",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "scope", Value: "destructive filesystem mutation"},
			{Label: "route", Value: "explicit path removal"},
			{Label: "mode", Value: "force"},
		},
		FormCatalog: []terminalexperience.ConsoleFormStep{{
			ID:     rmExplicitConfirmationFormID,
			Name:   "Confirmation",
			Detail: "default No",
		}},
	}
	if got := terminalRMConsoleDescriptor(&Options{Paths: []string{"/private/absolute/path"}, Force: force}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
	if got := terminalRMConsoleDescriptor(&Options{}); got.Metadata[1].Value != "smart cleanup" || got.Metadata[2].Value != "default-negative confirmation" || !reflect.DeepEqual(got.FormCatalog, []terminalexperience.ConsoleFormStep{
		{ID: rmSmartActionFormID, Name: "Clean action", Detail: "choose a cleanup action"},
		{ID: rmSmartTargetsFormID, Name: "Cleanup targets", Detail: "select paths to delete"},
	}) {
		t.Fatalf("smart descriptor metadata = %#v", got.Metadata)
	}
	unsafe := terminalRMConsoleDescriptor(&Options{Paths: []string{"bad\npath"}})
	for _, field := range []string{unsafe.Command, unsafe.Target, unsafe.Status} {
		if terminaltest.ContainsTerminalControl([]byte(field)) {
			t.Fatalf("descriptor field contains terminal control: %q", field)
		}
	}
	for _, metadata := range unsafe.Metadata {
		if terminaltest.ContainsTerminalControl([]byte(metadata.Label)) || terminaltest.ContainsTerminalControl([]byte(metadata.Value)) {
			t.Fatalf("descriptor metadata contains terminal control: %#v", metadata)
		}
	}
}

func TestRMFinishRequestUsesSafeOutcomeSummaries(t *testing.T) {
	tests := []struct {
		name     string
		outcome  terminalexperience.FinishOutcome
		explicit bool
		location string
		want     terminalexperience.FinishRequest
	}{
		{
			name:     "explicit success",
			outcome:  terminalexperience.Succeeded,
			explicit: true,
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Succeeded,
				Summary: terminalRMDocument("Removal complete", terminalexperience.VisualRoleSuccess),
			},
		},
		{
			name:    "smart success",
			outcome: terminalexperience.Succeeded,
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Succeeded,
				Summary: terminalRMDocument("Cleanup complete", terminalexperience.VisualRoleSuccess),
			},
		},
		{
			name:     "cancellation",
			outcome:  terminalexperience.Cancelled,
			explicit: true,
			location: rmResolvePhaseName,
			want: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Cancelled,
				Location: rmResolvePhaseName,
				Summary:  terminalRMDocument("Removal cancelled", terminalexperience.VisualRoleWarning),
			},
		},
		{
			name:     "explicit planning failure",
			outcome:  terminalexperience.Failed,
			explicit: true,
			location: rmResolvePhaseName,
			want: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Failed,
				Location: rmResolvePhaseName,
				Summary:  terminalRMDocument("Unable to resolve explicit targets", terminalexperience.VisualRoleError),
			},
		},
		{
			name:     "smart scan failure",
			outcome:  terminalexperience.Failed,
			location: rmScanPhaseName,
			want: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Failed,
				Location: rmScanPhaseName,
				Summary:  terminalRMDocument("Unable to scan cleanup targets", terminalexperience.VisualRoleError),
			},
		},
		{
			name:     "deletion failure",
			outcome:  terminalexperience.Failed,
			explicit: true,
			location: rmDeletePhaseName,
			want: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Failed,
				Location: rmDeletePhaseName,
				Summary:  terminalRMDocument("Unable to delete selected paths", terminalexperience.VisualRoleError),
			},
		},
		{
			name:     "unsafe location",
			outcome:  terminalexperience.Failed,
			explicit: true,
			location: "unsafe\nlocation",
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Failed,
				Summary: terminalRMDocument("Removal failed", terminalexperience.VisualRoleError),
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := terminalRMFinishRequest(testCase.outcome, testCase.explicit, testCase.location)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("Finish request = %#v, want %#v", got, testCase.want)
			}
			if terminaltest.ContainsTerminalControl([]byte(terminalexperience.RenderPlain(got.Summary))) {
				t.Fatalf("summary contains terminal controls: %#v", got)
			}
		})
	}
}

func TestFinishRMSubmitsFinishRequestAndSeparateResult(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	sink := newRMPhaseSink(run, terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive}, true)
	document := terminalRMDocument("Done!", terminalexperience.VisualRoleSuccess)
	if err := finishRMAt(run, sink, terminalexperience.Succeeded, rmDeletePhaseName, &document, nil); err != nil {
		t.Fatalf("finishRMAt() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 1 || operations[0].Kind != terminaltest.FinishOperation {
		t.Fatalf("operations = %#v", operations)
	}
	finish := operations[0].Value.(terminaltest.Finish)
	wantRequest := terminalRMFinishRequest(terminalexperience.Succeeded, true, rmDeletePhaseName)
	if !reflect.DeepEqual(finish.Request, wantRequest) || !reflect.DeepEqual(finish.Value.(terminalexperience.FinishRequest), wantRequest) {
		t.Fatalf("Finish = %#v, want request %#v", finish, wantRequest)
	}
	if len(finish.Documents) != 1 || finish.Documents[0] == nil || !reflect.DeepEqual(*finish.Documents[0], document) {
		t.Fatalf("durable Result = %#v, want %#v", finish.Documents, document)
	}
}
