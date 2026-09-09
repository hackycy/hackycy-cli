package set

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestConfigCMSetConsoleDescriptorProvidesSafeBoundedContext(t *testing.T) {
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / config cm set",
		Target:  "commit message profile update",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "scope", Value: "commit message configuration"},
			{Label: "profile", Value: "work"},
			{Label: "setting", Value: "apiKey"},
		},
	}
	if got := terminalCMSetConsoleDescriptor("work", "apiKey"); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
	unsafe := terminalCMSetConsoleDescriptor("bad\nprofile", "bad\tsetting")
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
	if got := unsafe.Metadata[1].Value; got != "Profile" {
		t.Fatalf("unsafe profile projection = %q, want Profile", got)
	}
	if got := unsafe.Metadata[2].Value; got != "Setting" {
		t.Fatalf("unsafe setting projection = %q, want Setting", got)
	}
}

func TestRunSetTracksOneAtomicUpdateAndKeepsPlainStreamsControlFree(t *testing.T) {
	var stdout, diagnostics bytes.Buffer
	var requests []SetRequest
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       &stdout,
		Diagnostics:  &diagnostics,
	})
	err := runSet(&Options{
		Context:  context.Background(),
		Profile:  "work",
		Key:      "model",
		Value:    "next-model",
		Terminal: experience,
		Store: func() (SetWriter, error) {
			return setWriterFunc(func(name, key, value string) error {
				requests = append(requests, SetRequest{Profile: name, Key: key, Value: value})
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("runSet() error = %v", err)
	}
	if got, want := requests, []SetRequest{{Profile: "work", Key: "model", Value: "next-model"}}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("writer requests = %#v, want %#v", got, want)
	}
	if got, want := stdout.String(), "Profile work updated\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := diagnostics.String(), "Updating CM profile...\n"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), diagnostics.Bytes()...)) {
		t.Fatalf("streams contain terminal controls: stdout=%q diagnostics=%q", stdout.String(), diagnostics.String())
	}
}

func TestRunSetAutomationStaysSilentWhilePreservingTheSingleWriterCall(t *testing.T) {
	var stdout, diagnostics bytes.Buffer
	var calls int
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       &stdout,
		Diagnostics:  &diagnostics,
	})
	err := runSet(&Options{
		Context:  context.Background(),
		Profile:  "work",
		Key:      "apiKey",
		Value:    "secret-that-must-not-be-printed",
		Terminal: experience,
		Store: func() (SetWriter, error) {
			return setWriterFunc(func(_, _, _ string) error {
				calls++
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("runSet() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("writer calls = %d, want 1", calls)
	}
	if got, want := stdout.String(), "Profile work updated\n"; got != want {
		t.Fatalf("Automation result = %q, want %q", got, want)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("Automation emitted diagnostics: %q", diagnostics.String())
	}
}

func TestRunSetFailureDoesNotProjectTheRequestedValue(t *testing.T) {
	var stdout, diagnostics bytes.Buffer
	failure := errors.New("storage failed: secret-that-must-not-be-printed")
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       &stdout,
		Diagnostics:  &diagnostics,
	})
	err := runSet(&Options{
		Context:  context.Background(),
		Profile:  "work",
		Key:      "apiKey",
		Value:    "secret-that-must-not-be-printed",
		Terminal: experience,
		Store: func() (SetWriter, error) {
			return setWriterFunc(func(_, _, _ string) error { return failure }), nil
		},
	})
	if !errors.Is(err, failure) {
		t.Fatalf("runSet() error = %v, want %v", err, failure)
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed update wrote stdout: %q", stdout.String())
	}
	if strings.Contains(diagnostics.String(), "secret-that-must-not-be-printed") {
		t.Fatalf("failed update leaked value in diagnostics: %q", diagnostics.String())
	}
}

func TestCMSetSuccessDetailUsesSafeKeySpecificProjections(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		contains []string
		absent   []string
	}{
		{name: "api key", key: "apiKey", value: "secret", contains: []string{"API key: [redacted]"}, absent: []string{"secret"}},
		{name: "url", key: "baseURL", value: "https://user:pass@example.test/v1?token=hidden#frag", contains: []string{"Base URL: https://example.test/v1"}, absent: []string{"user", "pass", "hidden", "frag"}},
		{name: "unsafe model", key: "model", value: "bad\nmodel", contains: []string{"Model configured"}, absent: []string{"bad\n"}},
		{name: "numeric", key: "timeoutMs", value: "1500suffix", contains: []string{"Requested value: 1500suffix"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := cmSetSuccessDetail("work", test.key, test.value)
			for _, expected := range test.contains {
				if !strings.Contains(got, expected) {
					t.Fatalf("detail = %q, missing %q", got, expected)
				}
			}
			for _, forbidden := range test.absent {
				if strings.Contains(got, forbidden) {
					t.Fatalf("detail = %q, leaked %q", got, forbidden)
				}
			}
		})
	}
}

func TestCMSetPhaseSinkUsesOneControlledWorkCatalog(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	sink := newCMSetPhaseSink(run, terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive})
	if err := sink.begin(); err != nil {
		t.Fatalf("begin() error = %v", err)
	}
	if err := sink.end(terminalexperience.PhaseCompleted, "safe success detail"); err != nil {
		t.Fatalf("end() error = %v", err)
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
			if got, want := operation.Value.(terminalexperience.WorkCatalog), terminalCMSetWorkCatalog(); !reflect.DeepEqual(got, want) {
				t.Fatalf("Work Catalog = %#v, want %#v", got, want)
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
	want := []terminalexperience.OperationPhase{
		{ID: cmSetPhaseID, State: terminalexperience.PhaseActive, Detail: "Validating setting and saving profile"},
		{ID: cmSetPhaseID, State: terminalexperience.PhaseCompleted, Detail: "safe success detail"},
	}
	if !reflect.DeepEqual(updates, want) {
		t.Fatalf("phase updates = %#v, want %#v", updates, want)
	}
}

func TestCMSetFinishRequestUsesCommandOwnedSafeOutcomeSummaries(t *testing.T) {
	tests := []struct {
		name    string
		outcome terminalexperience.FinishOutcome
		want    terminalexperience.FinishRequest
	}{
		{
			name:    "success",
			outcome: terminalexperience.Succeeded,
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Succeeded,
				Summary: terminalCMSetOutcomeDocument("Profile updated", terminalexperience.VisualRoleSuccess),
			},
		},
		{
			name:    "failure",
			outcome: terminalexperience.Failed,
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Failed,
				Summary: terminalCMSetOutcomeDocument("Unable to update CM profile", terminalexperience.VisualRoleError),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := terminalCMSetFinishRequest(test.outcome)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Finish request = %#v, want %#v", got, test.want)
			}
			if terminaltest.ContainsTerminalControl([]byte(terminalexperience.RenderPlain(got.Summary))) {
				t.Fatalf("summary contains terminal controls: %#v", got)
			}
		})
	}
}

type setWriterFunc func(name, key, value string) error

func (function setWriterFunc) SetCMProfileValue(name, key, value string) error {
	return function(name, key, value)
}

func TestTerminalCMSetPresentationPreservesPlainAndAutomationResults(t *testing.T) {
	result := SetResult{Profile: "work"}
	const want = "Profile work updated\n"

	for _, session := range []terminalexperience.Capabilities{
		{Interaction: terminalexperience.PlainInteractive},
		{Interaction: terminalexperience.Automation},
	} {
		var output bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
		run := experience.Open(context.Background())
		if err := run.Result(terminalCMSetDocument(result)); err != nil {
			t.Fatalf("Present() error = %v", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got := output.String(); got != want {
			t.Fatalf("%v result = %q, want %q", session.Interaction, got, want)
		}
		if terminaltest.ContainsTerminalControl(output.Bytes()) {
			t.Fatalf("%v result contains terminal control: %q", session.Interaction, output.String())
		}
	}
}

func TestTerminalCMSetPresentationUsesARichSuccessRole(t *testing.T) {
	for _, session := range []terminalexperience.Capabilities{
		{Interaction: terminalexperience.RichInteractive},
		{Interaction: terminalexperience.RichInteractive},
	} {
		document := terminalCMSetDocument(SetResult{Profile: "work"})
		if got, want := document.Blocks[0].Role, terminalexperience.VisualRoleSuccess; got != want {
			t.Fatalf("Rich role = %v, want %v", got, want)
		}
		var output bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
		run := experience.Open(context.Background())
		if err := run.Result(document); err != nil {
			t.Fatalf("Present() error = %v", err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got, want := output.String(), "Profile work updated\n"; got != want {
			t.Fatalf("Rich output = %q, want %q", got, want)
		}
		if terminaltest.ContainsTerminalControl(output.Bytes()) {
			t.Fatalf("non-terminal writer output contains terminal control: %q", output.String())
		}
	}
}
