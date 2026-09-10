package pulse

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestGitPulseConsoleDescriptorUsesSafeBoundedContext(t *testing.T) {
	days := 7
	got := terminalPulseConsoleDescriptor(&Options{Directory: "/private/workspace", Days: &days})
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / git pulse",
		Target:  "workspace commit activity",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "scope", Value: "workspace Git history"},
			{Label: "directory", Value: "workspace"},
			{Label: "range", Value: "7 days"},
		},
		FormCatalog: []terminalexperience.ConsoleFormStep{{
			ID:     pulseAuthorFormID,
			Name:   "Filter by authors",
			Detail: "choose authors",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
	unsafe := terminalPulseConsoleDescriptor(&Options{Directory: "bad\x1b[31m\npath"})
	for _, field := range []string{unsafe.Command, unsafe.Target, unsafe.Status, unsafe.Metadata[0].Label, unsafe.Metadata[0].Value, unsafe.Metadata[1].Label, unsafe.Metadata[1].Value, unsafe.Metadata[2].Label, unsafe.Metadata[2].Value} {
		if strings.ContainsAny(field, "\r\n\t\x1b") {
			t.Fatalf("unsafe descriptor field contains terminal control: %q", field)
		}
	}
	interactive := terminalPulseConsoleDescriptor(&Options{})
	if got, want := interactive.FormCatalog, []terminalexperience.ConsoleFormStep{
		{ID: pulseDateFormID, Name: "Select date range", Detail: "choose calendar range"},
		{ID: pulseAuthorFormID, Name: "Filter by authors", Detail: "choose authors"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("interactive FormCatalog = %#v, want %#v", got, want)
	}
}

func TestTerminalPulseAdapterTranslatesFormsPhasesAndPresentation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "7"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Values: []string{"Ada"}}},
	)
	run := experience.Open(context.Background())
	adapter := newTerminalPulseAdapter(run, func() {}, terminalPulseAdapterConfig{Capabilities: terminalexperience.Capabilities{
		Interaction: terminalexperience.RichInteractive,
		Stdout:      terminalexperience.StreamCapability{Terminal: true},
	}})

	days, cancelled, err := adapter.SelectDays(DayPrompt{Message: "Select date range:", Options: []DayChoice{{Value: 1, Label: "Today"}, {Value: 7, Label: "Last 7 days"}}})
	if err != nil || cancelled || days != 7 {
		t.Fatalf("SelectDays() = (%d, %t, %v)", days, cancelled, err)
	}
	authors, cancelled, err := adapter.SelectAuthors(AuthorPrompt{Message: "Filter by authors:", Options: []AuthorChoice{{Value: "Ada", Label: "Ada"}, {Value: "Ben", Label: "Ben"}}, InitialValues: []string{"Ada", "Ben"}, Required: true})
	if err != nil || cancelled || !reflect.DeepEqual(authors, []string{"Ada"}) {
		t.Fatalf("SelectAuthors() = (%#v, %t, %v)", authors, cancelled, err)
	}
	adapter.Introduction("/workspace")
	adapter.RepositoriesFound(2)
	adapter.Present(Report{CommitCount: 1, Repositories: []RepositoryReport{{Path: "/workspace/project", Commits: []Commit{{Date: "2026-08-23 10:00:00", Author: "Ada", Subject: "message"}}}}})
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 6 || operations[0].Kind != terminaltest.AskOperation || operations[1].Kind != terminaltest.AskOperation || operations[2].Kind != terminaltest.MilestoneOperation || operations[3].Kind != terminaltest.NoticeOperation || operations[4].Kind != terminaltest.MilestoneOperation || operations[5].Kind != terminaltest.CloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	daysRequest := operations[0].Value.(terminalexperience.InteractionRequest)
	if daysRequest.Kind != terminalexperience.InteractionSelect || daysRequest.Message != "Select date range:" || daysRequest.ConsoleStepID != pulseDateFormID || daysRequest.TranscriptLabel != "Date range" || !reflect.DeepEqual(daysRequest.CancelValues, []string{"", "q", "quit", "cancel"}) {
		t.Fatalf("days request = %#v", daysRequest)
	}
	authorRequest := operations[1].Value.(terminalexperience.InteractionRequest)
	if authorRequest.Kind != terminalexperience.InteractionMultiSelect || authorRequest.ConsoleStepID != pulseAuthorFormID || authorRequest.TranscriptLabel != "Author filter" || !authorRequest.HasDefault || !reflect.DeepEqual(authorRequest.Default.Values, []string{"Ada", "Ben"}) {
		t.Fatalf("author request = %#v", authorRequest)
	}
	intro := operations[2].Value.(terminalexperience.PresentationDocument)
	if !reflect.DeepEqual(intro.Blocks, []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleMuted, Text: "YCY / git pulse"}, {Role: terminalexperience.VisualRoleTitle, Text: "Workspace commit activity"}, {Role: terminalexperience.VisualRoleMuted, Text: "Inspect repositories and group recent commits"}}) {
		t.Fatalf("intro = %#v", intro)
	}
	if workspace := operations[3].Value.(terminalexperience.PresentationDocument); workspace.Blocks[0].Text != "Workspace: /workspace" {
		t.Fatalf("workspace notice = %#v", workspace)
	}
	if milestone := operations[4].Value.(terminalexperience.PresentationDocument); milestone.Blocks[0].Text != "Found 2 repositories" {
		t.Fatalf("repository milestone = %#v", milestone)
	}
	if report := adapter.FinishDocument(); report == nil || report.Blocks[0].Role != terminalexperience.VisualRoleMuted || report.Blocks[0].Text != "YCY / git pulse" {
		t.Fatalf("report = %#v", report)
	}
	request := adapter.FinishRequest(adapter.FinishOutcome())
	if request.Outcome != terminalexperience.Succeeded || len(request.Summary.Blocks) != 1 || request.Summary.Blocks[0].Text != "Found 1 commit in 1 repository" || strings.Contains(request.Summary.Blocks[0].Text, "message") {
		t.Fatalf("FinishRequest = %#v", request)
	}
}

func TestTerminalPulseAdapterMapsCancellationAndAutomationInteraction(t *testing.T) {
	cancelledExperience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrInteractionCancelled})
	adapter := newTerminalPulseAdapter(cancelledExperience.Open(context.Background()), func() {})
	if _, cancelled, err := adapter.SelectDays(DayPrompt{}); err != nil || !cancelled {
		t.Fatalf("cancelled SelectDays() = (%t, %v)", cancelled, err)
	}

	automationExperience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrAutomationInteraction})
	automation := newTerminalPulseAdapter(automationExperience.Open(context.Background()), func() {})
	if _, _, err := automation.SelectDays(DayPrompt{}); !errors.Is(err, errGitPulseRequiresInteractive) {
		t.Fatalf("Automation SelectDays() error = %v", err)
	}
}

func TestTerminalPulseFinishRequestKeepsCallerOutcomeAfterResultSelection(t *testing.T) {
	adapter := newTerminalPulseAdapter(terminaltest.NewRecordingExperience().Open(context.Background()), func() {})
	adapter.NoRepositories()
	request := adapter.FinishRequest(terminalexperience.Failed)
	if request.Outcome != terminalexperience.Failed || len(request.Summary.Blocks) != 1 || request.Summary.Blocks[0].Text != "No Git repositories found." {
		t.Fatalf("FinishRequest = %#v", request)
	}
}

func TestTerminalPulseAdapterBridgesTypedPhasesToOneControlledWorkCatalog(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	adapter := newTerminalPulseAdapter(experience.Open(context.Background()), func() {})
	for _, phase := range []PhaseKind{PhasePrepare, PhaseScan, PhaseFetch, PhaseBuild} {
		reporter, err := adapter.Start(context.Background(), phase)
		if err != nil {
			t.Fatalf("Start(%d) error = %v", phase, err)
		}
		reporter.Report(Phase{Kind: phase, State: PhaseCompleted})
		if err := reporter.Close(); err != nil {
			t.Fatalf("Close(%d) error = %v", phase, err)
		}
	}
	if err := adapter.CloseWork(); err != nil {
		t.Fatalf("CloseWork() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 6 || operations[0].Kind != terminaltest.StartWorkOperation || operations[1].Kind != terminaltest.WorkUpdateOperation || operations[2].Kind != terminaltest.WorkUpdateOperation || operations[3].Kind != terminaltest.WorkUpdateOperation || operations[4].Kind != terminaltest.WorkUpdateOperation || operations[5].Kind != terminaltest.WorkCloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	catalog := operations[0].Value.(terminalexperience.WorkCatalog)
	if catalog.Label != "Git Pulse" || catalog.ID != pulseWorkCatalogID || !reflect.DeepEqual(catalog.Phases, pulseWorkPhaseCatalog()) {
		t.Fatalf("Work Catalog = %#v", catalog)
	}
	for index, phaseID := range []string{pulsePreparePhaseID, pulseScanPhaseID, pulseFetchPhaseID, pulseBuildPhaseID} {
		update := operations[index+1].Value.(terminalexperience.OperationPhase)
		if update.ID != phaseID || update.State != terminalexperience.PhaseCompleted {
			t.Fatalf("Work update %d = %#v", index, update)
		}
	}
}

func TestTerminalPulseTrackFailureRequestsCommandCancellation(t *testing.T) {
	trackFailure := errors.New("renderer stopped")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter := newTerminalPulseAdapter(&failingPulseTrackRun{trackFailure: trackFailure}, cancel)
	if _, err := adapter.Start(ctx, PhaseScan); !errors.Is(err, trackFailure) {
		t.Fatalf("Start() error = %v, want renderer failure", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("track failure did not cancel the command context")
	}
}

func TestRunPulseAutomationFailsBeforeReadingInputOrWritingAResult(t *testing.T) {
	workspace := t.TempDir()
	initializeStandalonePulseRepository(t, filepath.Join(workspace, "alpha"), "Ada", "ada@example.test", "alpha commit")
	initializeStandalonePulseRepository(t, filepath.Join(workspace, "beta"), "Ben", "ben@example.test", "beta commit")

	for _, testCase := range []struct {
		name string
		days *int
	}{
		{name: "date selection"},
		{name: "author selection", days: pulseInt(1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
				Input:        panicPulseReader{},
				Output:       stdout,
				Diagnostics:  stderr,
			})
			err := runPulse(&Options{
				Context:          context.Background(),
				Directory:        workspace,
				Days:             testCase.days,
				WorkingDirectory: func() (string, error) { return workspace, nil },
				Terminal:         experience,
				Git:              &gitprocess.Runner{},
				Now:              time.Now,
			})
			if !errors.Is(err, errGitPulseRequiresInteractive) || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("Run() error = %v, streams = (%q, %q)", err, stdout.String(), stderr.String())
			}
			if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
				t.Fatalf("Automation streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunPulsePlainJourneyKeepsPhasesOnStderrAndReportOnStdout(t *testing.T) {
	workspace := t.TempDir()
	initializeStandalonePulseRepository(t, filepath.Join(workspace, "alpha"), "Ada", "ada@example.test", "alpha commit")
	initializeStandalonePulseRepository(t, filepath.Join(workspace, "beta"), "Ben", "ben@example.test", "beta commit")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("1\n1,2\n"),
		Output:       stdout,
		Diagnostics:  stderr,
	})

	err := runPulse(&Options{
		Context:          context.Background(),
		Directory:        workspace,
		WorkingDirectory: func() (string, error) { return workspace, nil },
		Terminal:         experience,
		Git:              &gitprocess.Runner{},
		Now:              time.Now,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, expected := range []string{"Found 2 commits in 2 repositories", "alpha commit", "beta commit"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout missing %q: %q", expected, stdout.String())
		}
	}
	for _, expected := range []string{"YCY / git pulse", "Workspace commit activity", "Prepare workspace", "Scan repositories", "Select date range:", "Found 2 repositories", "Fetch commits", "Filter by authors:", "Build commit tree"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("stderr missing %q: %q", expected, stderr.String())
		}
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
		t.Fatalf("Plain streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
	}
}

func TestRunPulseSubmitsExactlyOneDurableResult(t *testing.T) {
	workspace := t.TempDir()
	initializeStandalonePulseRepository(t, filepath.Join(workspace, "alpha"), "Ada", "ada@example.test", "alpha commit")
	stdout := &countingPulseWriter{}
	stderr := &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runPulse(&Options{
		Context:          context.Background(),
		Directory:        workspace,
		Days:             pulseInt(1),
		WorkingDirectory: func() (string, error) { return workspace, nil },
		Terminal:         experience,
		Git:              &gitprocess.Runner{},
		Now:              time.Now,
	})
	if err != nil {
		t.Fatalf("runPulse() error = %v", err)
	}
	if stdout.writes != 1 || strings.Count(stdout.String(), "Found 1 commit in 1 repository") != 1 || !strings.Contains(stdout.String(), "alpha commit") {
		t.Fatalf("stdout writes/result = (%d, %q)", stdout.writes, stdout.String())
	}
}

func TestRunPulseDateCancellationUsesTheEstablishedExitZeroResult(t *testing.T) {
	workspace := t.TempDir()
	initializeStandalonePulseRepository(t, filepath.Join(workspace, "alpha"), "Ada", "ada@example.test", "alpha commit")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("cancel\n"),
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runPulse(&Options{
		Context:          context.Background(),
		Directory:        workspace,
		WorkingDirectory: func() (string, error) { return workspace, nil },
		Terminal:         experience,
		Git:              &gitprocess.Runner{},
		Now:              time.Now,
	})
	if err != nil || stdout.String() != "Operation cancelled.\n" || !strings.Contains(stderr.String(), "Date range selection cancelled") {
		t.Fatalf("date cancellation = (%v, stdout=%q, stderr=%q)", err, stdout.String(), stderr.String())
	}
}

func TestRunPulsePreservesEmptyAndPrepareFailureResultsWithControlledWork(t *testing.T) {
	newExperience := func(stdout, stderr *bytes.Buffer) *terminalexperience.Runtime {
		return terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
			Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
			Output:       stdout,
			Diagnostics:  stderr,
		})
	}
	t.Run("no repositories", func(t *testing.T) {
		workspace := t.TempDir()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		err := runPulse(&Options{
			Context:          context.Background(),
			Directory:        workspace,
			WorkingDirectory: func() (string, error) { return workspace, nil },
			Terminal:         newExperience(stdout, stderr),
			Git:              &gitprocess.Runner{},
			Now:              time.Now,
		})
		if err != nil || stdout.String() != "No Git repositories found.\n" {
			t.Fatalf("no repositories = (%v, stdout=%q, stderr=%q)", err, stdout.String(), stderr.String())
		}
	})
	t.Run("no commits", func(t *testing.T) {
		workspace := t.TempDir()
		initializeStandalonePulseRepository(t, workspace, "Ada", "ada@example.test", "old commit")
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		days := -1
		err := runPulse(&Options{
			Context:          context.Background(),
			Directory:        workspace,
			Days:             &days,
			WorkingDirectory: func() (string, error) { return workspace, nil },
			Terminal:         newExperience(stdout, stderr),
			Git:              &gitprocess.Runner{},
			Now:              time.Now,
		})
		if err != nil || stdout.String() != "No commits found in the specified date range.\n" {
			t.Fatalf("no commits = (%v, stdout=%q, stderr=%q)", err, stdout.String(), stderr.String())
		}
	})
	t.Run("prepare failure", func(t *testing.T) {
		workspace := t.TempDir()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		err := runPulse(&Options{
			Context:          context.Background(),
			Directory:        "missing",
			WorkingDirectory: func() (string, error) { return workspace, nil },
			Terminal:         newExperience(stdout, stderr),
			Git:              &gitprocess.Runner{},
			Now:              time.Now,
		})
		if err == nil || stdout.Len() != 0 {
			t.Fatalf("prepare failure = (%v, stdout=%q, stderr=%q)", err, stdout.String(), stderr.String())
		}
	})
}

func TestTerminalPulseAutomationWritesOnlySafePartialWarningsToDiagnostics(t *testing.T) {
	var stdout, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       &stdout,
		Diagnostics:  &diagnostics,
	})
	adapter := newTerminalPulseAdapter(experience.Open(context.Background()), func() {}, terminalPulseAdapterConfig{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Diagnostics:  experience.DiagnosticWriter(),
	})
	adapter.PulseFetchWarning("/workspace", []string{"/workspace/private\x1b[2K"})
	if stdout.Len() != 0 || !strings.Contains(diagnostics.String(), "private\\x1b[2K") || strings.Contains(diagnostics.String(), "\x1b") {
		t.Fatalf("Automation warning streams = (%q, %q)", stdout.String(), diagnostics.String())
	}
}

type failingPulseTrackRun struct {
	terminalexperience.ExperienceRun
	trackFailure error
}

func (run *failingPulseTrackRun) Track(terminalexperience.TrackedOperation) error {
	return run.trackFailure
}

func (run *failingPulseTrackRun) StartWork(terminalexperience.WorkCatalog) (terminalexperience.WorkSession, error) {
	return nil, run.trackFailure
}

type countingPulseWriter struct {
	bytes.Buffer
	writes int
}

func (writer *countingPulseWriter) Write(value []byte) (int, error) {
	writer.writes++
	return writer.Buffer.Write(value)
}

func (writer *countingPulseWriter) WriteString(value string) (int, error) {
	writer.writes++
	return writer.Buffer.WriteString(value)
}

type panicPulseReader struct{}

func (panicPulseReader) Read([]byte) (int, error) {
	panic("git pulse Automation must not read stdin")
}
