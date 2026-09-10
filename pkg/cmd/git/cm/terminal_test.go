package cm

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalGitCMAdapterTranslatesFormsPhasesAndPresentation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Values: []string{"one.go"}}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Confirmed: true}},
	)
	run := experience.Open(context.Background())
	adapter := newTerminalGitCMAdapter(run, func() {})
	stagePrompt := StagePrompt{
		Message:       "Select files to stage",
		Options:       []StageOption{{Value: "one.go", Label: "M one.go"}, {Value: "two.go", Label: "A two.go"}},
		InitialValues: []string{"one.go", "two.go"},
	}
	selected, cancelled, err := adapter.SelectFiles(stagePrompt)
	if err != nil || cancelled || !reflect.DeepEqual(selected, []string{"one.go"}) {
		t.Fatalf("SelectFiles() = (%#v, %t, %v)", selected, cancelled, err)
	}
	commitPrompt := CommitPrompt{
		Message:   "Create this commit?",
		Generated: GeneratedMessage{Message: "feat(cm): present output", Evidence: EvidenceCoverage{EstimatedLocalPromptTokens: 456, RepresentedClusters: 1, TotalClusters: 1, IncludedFacts: 4}},
		Profile:   ProfileDiagnostic{Name: "work", Model: "model"},
	}
	confirmed, cancelled, err := adapter.ConfirmCommit(commitPrompt)
	if err != nil || cancelled || !confirmed {
		t.Fatalf("ConfirmCommit() = (%t, %t, %v)", confirmed, cancelled, err)
	}
	reporter, err := adapter.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	reporter.Report(Phase{Kind: PhaseCommit, State: PhaseActive})
	reporter.Report(Phase{Kind: PhaseCommit, State: PhaseCompleted})
	if err := reporter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	result := Result{Generated: &commitPrompt.Generated, Profile: commitPrompt.Profile, Committed: true}
	if err := adapter.PresentGenerated(result); err != nil {
		t.Fatalf("PresentGenerated() error = %v", err)
	}
	if err := adapter.PresentOutcome(result); err != nil {
		t.Fatalf("PresentOutcome() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 7 || operations[0].Kind != terminaltest.AskOperation || operations[1].Kind != terminaltest.NoticeOperation || operations[2].Kind != terminaltest.AskOperation || operations[3].Kind != terminaltest.TrackOperation || operations[4].Kind != terminaltest.ResultOperation || operations[5].Kind != terminaltest.ResultOperation || operations[6].Kind != terminaltest.CloseOperation {
		t.Fatalf("operations = %#v", operations)
	}
	stageRequest := operations[0].Value.(terminalexperience.InteractionRequest)
	if stageRequest.Kind != terminalexperience.InteractionMultiSelect || stageRequest.ConsoleStepID != gitCMStageFormID || stageRequest.TranscriptLabel != "Selected files" || stageRequest.TranscriptProject == nil || stageRequest.TranscriptProject(terminalexperience.InteractionAnswer{Values: []string{"one.go", "two.go"}}) != "2 files selected" || !stageRequest.HasDefault || !reflect.DeepEqual(stageRequest.Default.Values, stagePrompt.InitialValues) || !reflect.DeepEqual(stageRequest.CancelValues, []string{"q", "quit", "cancel"}) {
		t.Fatalf("stage request = %#v", stageRequest)
	}
	preview := operations[1].Value.(terminalexperience.PresentationDocument)
	if preview.Blocks[0].Role != terminalexperience.VisualRoleSuccess || !strings.Contains(preview.Blocks[0].Text, "feat(cm): present output") || !strings.Contains(terminalexperience.RenderPlain(preview), "Profile: work (model)") {
		t.Fatalf("preview document = %#v", preview)
	}
	commitRequest := operations[2].Value.(terminalexperience.InteractionRequest)
	if commitRequest.Kind != terminalexperience.InteractionConfirm || commitRequest.ConsoleStepID != gitCMCommitFormID || commitRequest.TranscriptLabel != "Commit confirmation" || commitRequest.TranscriptProject == nil || commitRequest.TranscriptProject(terminalexperience.InteractionAnswer{Confirmed: false}) != "declined" || !commitRequest.HasDefault || !commitRequest.Default.Confirmed || commitRequest.Description != "" {
		t.Fatalf("commit request = %#v", commitRequest)
	}
	operation := operations[3].Value.(terminalexperience.TrackedOperation)
	if operation.Label != "Git CM" {
		t.Fatalf("tracked operation = %#v", operation)
	}
	generated := operations[4].Value.(terminalexperience.PresentationDocument)
	if generated.Blocks[0].Role != terminalexperience.VisualRoleSuccess || !strings.Contains(generated.Blocks[0].Text, "feat(cm): present output") {
		t.Fatalf("generated document = %#v", generated)
	}
	outcome := operations[5].Value.(terminalexperience.PresentationDocument)
	if outcome.Blocks[0].Role != terminalexperience.VisualRoleSuccess || outcome.Blocks[0].Text != "Commit created" {
		t.Fatalf("outcome document = %#v", outcome)
	}
}

func TestTerminalGitCMAdapterMapsCancellationAndAutomationInteraction(t *testing.T) {
	cancelledExperience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrInteractionCancelled})
	cancelledAdapter := newTerminalGitCMAdapter(cancelledExperience.Open(context.Background()), func() {})
	if _, cancelled, err := cancelledAdapter.SelectFiles(StagePrompt{}); err != nil || !cancelled {
		t.Fatalf("SelectFiles() = (%t, %v)", cancelled, err)
	}

	operationFailure := errors.New("selection transport failed")
	joinedExperience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: errors.Join(terminalexperience.ErrInteractionCancelled, operationFailure)})
	joinedAdapter := newTerminalGitCMAdapter(joinedExperience.Open(context.Background()), func() {})
	if _, cancelled, err := joinedAdapter.SelectFiles(StagePrompt{}); cancelled || !errors.Is(err, operationFailure) {
		t.Fatalf("joined SelectFiles() = (%t, %v), want the operation failure", cancelled, err)
	}

	automationExperience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrAutomationInteraction})
	automationAdapter := newTerminalGitCMAdapter(automationExperience.Open(context.Background()), func() {})
	if _, _, err := automationAdapter.ConfirmCommit(CommitPrompt{}); !errors.Is(err, errGitCMRequiresInteractive) {
		t.Fatalf("ConfirmCommit() error = %v", err)
	}
}

func TestGitCMConsoleDescriptorDeclaresApplicableFormsBeforeInteraction(t *testing.T) {
	tests := []struct {
		name  string
		input Input
		want  []terminalexperience.ConsoleFormStep
	}{
		{
			name:  "stage and commit",
			input: Input{Stage: true},
			want: []terminalexperience.ConsoleFormStep{
				{ID: gitCMStageFormID, Name: "Select files to stage", Detail: "all changes selected by default"},
				{ID: gitCMCommitFormID, Name: "Commit confirmation", Detail: "default Yes"},
			},
		},
		{
			name:  "staged commit",
			input: Input{Staged: true},
			want: []terminalexperience.ConsoleFormStep{
				{ID: gitCMCommitFormID, Name: "Commit confirmation", Detail: "default Yes"},
			},
		},
		{
			name:  "generation only",
			input: Input{DryRun: true},
			want:  nil,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := gitCMConsoleDescriptor(testCase.input).FormCatalog; !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("FormCatalog = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestTerminalGitCMRichAdapterUsesOneCompleteControlledWorkCatalog(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	adapter := newTerminalGitCMAdapter(experience.Open(context.Background()), func() {})
	adapter.enableDetailed()
	adapter.enableControlledWork()
	adapter.reportCMPhase(cmInspectChangesPhaseID, PhaseActive, "Reading repository status")
	reporter, err := adapter.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	reporter.Report(Phase{Kind: PhaseCollect, State: PhaseActive})
	adapter.reportCMPhase(cmInspectChangesPhaseID, PhaseCompleted, "Repository status read")
	if err := reporter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := adapter.finishDetailed(); err != nil {
		t.Fatalf("finishDetailed() error = %v", err)
	}

	operations := experience.Run.Operations()
	var catalogs []terminalexperience.WorkCatalog
	var tracks int
	var updates []terminalexperience.OperationPhase
	var closes int
	for _, operation := range operations {
		switch operation.Kind {
		case terminaltest.StartWorkOperation:
			catalogs = append(catalogs, operation.Value.(terminalexperience.WorkCatalog))
		case terminaltest.TrackOperation:
			tracks++
		case terminaltest.WorkUpdateOperation:
			updates = append(updates, operation.Value.(terminalexperience.OperationPhase))
		case terminaltest.WorkCloseOperation:
			closes++
		}
	}
	if len(catalogs) != 1 || tracks != 0 || closes != 1 {
		t.Fatalf("controlled operations = %#v", operations)
	}
	wantCatalog := terminalexperience.WorkCatalog{
		ID:     cmWorkCatalogID,
		Label:  "Git CM",
		Phases: append([]terminalexperience.PhaseDefinition(nil), cmPhaseDefinitions...),
	}
	if catalogs[0].ID != wantCatalog.ID || catalogs[0].Label != wantCatalog.Label || !reflect.DeepEqual(catalogs[0].Phases, wantCatalog.Phases) {
		t.Fatalf("Work Catalog = %#v, want %#v", catalogs[0], wantCatalog)
	}
	if len(updates) != 2 || updates[0].ID != cmInspectChangesPhaseID || updates[1].ID != cmInspectChangesPhaseID || updates[1].State != terminalexperience.PhaseCompleted {
		t.Fatalf("Work updates = %#v", updates)
	}
}

func TestTerminalGitCMFinishRequestUsesSafeOutcomeSummaries(t *testing.T) {
	generated := &GeneratedMessage{Message: "feat(cm): generated-secret", FileCount: 2}
	unsafe := Result{
		Generated: generated,
		Profile: ProfileDiagnostic{
			Name:    "private-profile",
			BaseURL: "https://user:pass@example.invalid/private?token=secret",
			Model:   "private-model",
		},
	}
	tests := []struct {
		name     string
		outcome  terminalexperience.FinishOutcome
		result   Result
		location string
		want     string
		role     terminalexperience.VisualRole
		at       string
	}{
		{
			name:    "generation-only success",
			outcome: terminalexperience.Succeeded,
			result:  unsafe,
			want:    "Commit message generated",
			role:    terminalexperience.VisualRoleSuccess,
		},
		{
			name:    "no staged changes",
			outcome: terminalexperience.Succeeded,
			result:  Result{NoChanges: true, NoChangeScope: ScopeStaged},
			want:    "No staged changes",
			role:    terminalexperience.VisualRoleWarning,
		},
		{
			name:     "selection cancellation",
			outcome:  terminalexperience.Cancelled,
			location: cmInspectChangesPhaseID,
			result:   Result{Cancelled: true},
			want:     "File selection cancelled",
			role:     terminalexperience.VisualRoleWarning,
			at:       cmInspectChangesPhaseName,
		},
		{
			name:     "commit cancellation",
			outcome:  terminalexperience.Cancelled,
			location: cmGenerateMessagePhaseID,
			result:   Result{Generated: generated, PromptedCommit: true, Cancelled: true},
			want:     "Commit creation cancelled",
			role:     terminalexperience.VisualRoleWarning,
			at:       cmGenerateMessagePhaseName,
		},
		{
			name:     "provider failure",
			outcome:  terminalexperience.Failed,
			location: cmGenerateMessagePhaseID,
			result:   unsafe,
			want:     "Unable to generate commit message",
			role:     terminalexperience.VisualRoleError,
			at:       cmGenerateMessagePhaseName,
		},
		{
			name:     "scope failure",
			outcome:  terminalexperience.Failed,
			location: cmVerifyScopePhaseID,
			result:   Result{Generated: generated, PromptedCommit: true},
			want:     "Git scope changed; commit not created",
			role:     terminalexperience.VisualRoleError,
			at:       cmVerifyScopePhaseName,
		},
		{
			name:     "commit failure",
			outcome:  terminalexperience.Failed,
			location: cmCreateCommitPhaseID,
			result:   Result{Generated: generated, PromptedCommit: true},
			want:     "Unable to create commit",
			role:     terminalexperience.VisualRoleError,
			at:       cmCreateCommitPhaseName,
		},
		{
			name:     "partial push failure",
			outcome:  terminalexperience.Failed,
			location: cmPushCommitPhaseID,
			result:   Result{Generated: generated, PromptedCommit: true, Committed: true},
			want:     "Commit created locally; push not completed",
			role:     terminalexperience.VisualRoleError,
			at:       cmPushCommitPhaseName,
		},
		{
			name:     "unsafe location",
			outcome:  terminalexperience.Failed,
			location: "private\n/path",
			result:   unsafe,
			want:     "Git CM operation failed",
			role:     terminalexperience.VisualRoleError,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := terminalGitCMFinishRequest(testCase.outcome, testCase.result, testCase.location)
			if request.Outcome != testCase.outcome || request.Location != testCase.at {
				t.Fatalf("Finish request identity = %#v, want outcome=%v location=%q", request, testCase.outcome, testCase.at)
			}
			if len(request.Summary.Blocks) == 0 || request.Summary.Blocks[0].Text != testCase.want || request.Summary.Blocks[0].Role != testCase.role {
				t.Fatalf("Finish summary = %#v, want %q with role %v", request.Summary, testCase.want, testCase.role)
			}
			text := terminalexperience.RenderPlain(request.Summary)
			if terminaltest.ContainsTerminalControl([]byte(text)) {
				t.Fatalf("summary contains terminal controls: %q", text)
			}
			for _, forbidden := range []string{"generated-secret", "example.invalid", "user:pass", "token=secret", "private-profile", "private-model"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("summary leaked %q: %q", forbidden, text)
				}
			}
		})
	}
	if got := gitCMFinishLocation(cmPushCommitPhaseName); got != cmPushCommitPhaseName {
		t.Fatalf("phase-name location = %q", got)
	}
}

func TestTerminalGitCMFinishSubmitsRequestAndSeparateResult(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	adapter := newTerminalGitCMAdapter(run, func() {})
	adapter.enableDetailed()
	adapter.reportCMPhase(cmPushCommitPhaseID, PhaseActive, "Remote: origin")
	result := Result{
		Generated:      &GeneratedMessage{Message: "feat(cm): durable-result-only"},
		PromptedCommit: true,
		Committed:      true,
	}
	durable := gitCMOutcomeDocument(result)
	if err := adapter.finish(terminalexperience.Failed, result, &durable); err != nil {
		t.Fatalf("finish() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 1 || operations[0].Kind != terminaltest.FinishOperation {
		t.Fatalf("operations = %#v", operations)
	}
	finish := operations[0].Value.(terminaltest.Finish)
	wantRequest := terminalGitCMFinishRequest(terminalexperience.Failed, result, cmPushCommitPhaseID)
	if !reflect.DeepEqual(finish.Value.(terminalexperience.FinishRequest), wantRequest) || !reflect.DeepEqual(finish.Request, wantRequest) {
		t.Fatalf("Finish = %#v, want request %#v", finish, wantRequest)
	}
	if len(finish.Documents) != 1 || finish.Documents[0] == nil || !reflect.DeepEqual(*finish.Documents[0], durable) {
		t.Fatalf("durable Result = %#v, want %#v", finish.Documents, durable)
	}
	if reflect.DeepEqual(finish.Request.Summary, *finish.Documents[0]) || strings.Contains(terminalexperience.RenderPlain(finish.Request.Summary), "durable-result-only") {
		t.Fatalf("Finish summary must remain separate from durable Result: %#v", finish)
	}
}

func TestGitCMFinishOutcomeTreatsOnlyContextCancellationAsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := gitCMFinishOutcome(ctx, Result{}, context.Canceled); got != terminalexperience.Cancelled {
		t.Fatalf("context cancellation outcome = %v, want cancelled", got)
	}
	if got := gitCMFinishOutcome(ctx, Result{}, errors.Join(context.Canceled, errors.New("real operation failure"))); got != terminalexperience.Failed {
		t.Fatalf("joined failure outcome = %v, want failed", got)
	}
}

func TestTerminalGitCMFinishPreservesStagedChangesOnLaterFailureOrCancellation(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		outcome terminalexperience.FinishOutcome
		result  Result
	}{
		{name: "generation failure", outcome: terminalexperience.Failed, result: Result{Generated: &GeneratedMessage{}, Profile: ProfileDiagnostic{Name: "fixture"}}},
		{name: "commit cancellation", outcome: terminalexperience.Cancelled, result: Result{Generated: &GeneratedMessage{}, PromptedCommit: true, Cancelled: true}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			experience := terminaltest.NewRecordingExperience()
			adapter := newTerminalGitCMAdapter(experience.Open(context.Background()), func() {})
			adapter.enableDetailed()
			adapter.reportCMPhase(cmStageSelectedPhaseID, PhaseActive, "staging")
			adapter.reportCMPhase(cmStageSelectedPhaseID, PhaseCompleted, "staged")
			if err := adapter.finish(testCase.outcome, testCase.result, nil); err != nil {
				t.Fatalf("finish() error = %v", err)
			}
			operations := experience.Run.Operations()
			if len(operations) != 1 || operations[0].Kind != terminaltest.FinishOperation {
				t.Fatalf("operations = %#v", operations)
			}
			request := operations[0].Value.(terminaltest.Finish).Request
			text := terminalexperience.RenderPlain(request.Summary)
			if !strings.Contains(text, "Staged changes retained") {
				t.Fatalf("Finish summary = %q, want retained-staging fact", text)
			}
		})
	}
}

func TestGitCMPlainJourneyKeepsFormsAndPhasesOnStderr(t *testing.T) {
	repository := newGitCMRepository(t)
	withGitCMWorkingDirectory(t, repository)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "plain journey\n")
	server, provider := newGitCMMessageProvider(t, "feat(cm): plain tracked journey")
	defer server.Close()
	configureGitCMProvider(t, server.URL)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("1\n\n"),
		Output:       stdout,
		Diagnostics:  stderr,
	})

	result, err := executeCMForTest(context.Background(), experience, Input{Stage: true})
	if err != nil || !result.Committed || result.Pushed || provider.calls != 1 {
		t.Fatalf("Run() = (%#v, %v), provider calls = %d", result, err, provider.calls)
	}
	for _, expected := range []string{"Commit created"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout omitted %q: %q", expected, stdout.String())
		}
	}
	for _, expected := range []string{"Select files to stage", "1) A README.md", "Staging selected files", "Collecting changes", "Generating commit message", "feat(cm): plain tracked journey", "Profile: env (fixture-model)", "Create this commit? [Y/n]:", "Creating commit"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("stderr omitted %q: %q", expected, stderr.String())
		}
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
		t.Fatalf("Plain streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
	}
	if subject := strings.TrimSpace(gitCMOutput(t, repository, "log", "-1", "--format=%s")); subject != "feat(cm): plain tracked journey" {
		t.Fatalf("HEAD subject = %q", subject)
	}
}

func TestGitCMPlainSelectionCancellationReplaysReachedInspectionPhase(t *testing.T) {
	repository := newGitCMRepository(t)
	withGitCMWorkingDirectory(t, repository)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "cancel before staging\n")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("cancel\n"),
		Output:       stdout,
		Diagnostics:  stderr,
	})

	result, err := executeCMForTest(context.Background(), experience, Input{Stage: true})
	if err != nil || !result.Cancelled || result.Committed {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	if got, want := stdout.String(), "Cancelled\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "Inspect changes") || !strings.Contains(stderr.String(), "File selection cancelled") {
		t.Fatalf("stderr did not replay reached work: %q", stderr.String())
	}
	if status := gitCMOutput(t, repository, "status", "--short"); status != "?? README.md\n" {
		t.Fatalf("selection cancellation changed index = %q", status)
	}
}

func TestGitCMPlainStageAllCompletesCatalogBeforeMutation(t *testing.T) {
	repository := newGitCMRepository(t)
	withGitCMWorkingDirectory(t, repository)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "stage all\n")
	server, provider := newGitCMMessageProvider(t, "feat(cm): stage all catalog")
	defer server.Close()
	configureGitCMProvider(t, server.URL)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("\n"),
		Output:       stdout,
		Diagnostics:  stderr,
	})

	result, err := executeCMForTest(context.Background(), experience, Input{StageAll: true})
	if err != nil || !result.Committed || provider.calls != 1 {
		t.Fatalf("Run() = (%#v, %v), provider calls = %d", result, err, provider.calls)
	}
	if !strings.Contains(stderr.String(), "Inspect changes") || !strings.Contains(stderr.String(), "Stage all changes") {
		t.Fatalf("stage-all phases = %q", stderr.String())
	}
	if got, want := stdout.String(), "Commit created\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestGitCMAutomationFailsBeforePromptDependentMutationOrOutput(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		input      Input
		prepare    func(*testing.T, string)
		wantStatus string
	}{
		{
			name:  "file selection",
			input: Input{Stage: true},
			prepare: func(t *testing.T, repository string) {
				writeGitCMFile(t, filepath.Join(repository, "README.md"), "selection\n")
			},
			wantStatus: "?? README.md\n",
		},
		{
			name:  "commit confirmation",
			input: Input{Staged: true},
			prepare: func(t *testing.T, repository string) {
				writeGitCMFile(t, filepath.Join(repository, "README.md"), "confirmation\n")
				runGitCM(t, repository, "add", "README.md")
			},
			wantStatus: "A  README.md\n",
		},
		{
			name:  "stage all confirmation",
			input: Input{StageAll: true},
			prepare: func(t *testing.T, repository string) {
				writeGitCMFile(t, filepath.Join(repository, "README.md"), "stage all\n")
			},
			wantStatus: "?? README.md\n",
		},
		{
			name:  "push confirmation",
			input: Input{Staged: true, Push: stringPointer("origin")},
			prepare: func(t *testing.T, repository string) {
				writeGitCMFile(t, filepath.Join(repository, "README.md"), "push\n")
				runGitCM(t, repository, "add", "README.md")
			},
			wantStatus: "A  README.md\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newGitCMRepository(t)
			withGitCMWorkingDirectory(t, repository)
			testCase.prepare(t, repository)
			beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
				Input:        panicGitCMReader{},
				Output:       stdout,
				Diagnostics:  stderr,
			})
			_, err := executeCMForTest(context.Background(), experience, testCase.input)
			if !errors.Is(err, errGitCMRequiresInteractive) || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("Automation error = %v, streams = (%q, %q)", err, stdout.String(), stderr.String())
			}
			if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
				t.Fatalf("Automation failure changed HEAD from %q to %q", beforeHead, afterHead)
			}
			if status := gitCMOutput(t, repository, "status", "--short"); status != testCase.wantStatus {
				t.Fatalf("Automation failure status = %q, want %q", status, testCase.wantStatus)
			}
			if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
				t.Fatalf("Automation streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
			}
		})
	}
}

func TestGitCMGenerationOnlyAutomationRetainsTheDurableResult(t *testing.T) {
	repository := newGitCMRepository(t)
	withGitCMWorkingDirectory(t, repository)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "automation generation\n")
	server, provider := newGitCMMessageProvider(t, "feat(cm): automation generation")
	defer server.Close()
	configureGitCMProvider(t, server.URL)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Input:        panicGitCMReader{},
		Output:       stdout,
		Diagnostics:  stderr,
	})

	result, err := executeCMForTest(context.Background(), experience, Input{DryRun: true})
	if err != nil || result.Generated == nil || result.PromptedCommit || provider.calls != 1 {
		t.Fatalf("Run() = (%#v, %v), provider calls = %d", result, err, provider.calls)
	}
	if !strings.Contains(stdout.String(), "feat(cm): automation generation") || stderr.Len() != 0 {
		t.Fatalf("Automation streams = (%q, %q)", stdout.String(), stderr.String())
	}
	if status := gitCMOutput(t, repository, "status", "--short"); status != "?? README.md\n" {
		t.Fatalf("generation-only status = %q", status)
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
		t.Fatalf("Automation streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
	}
}

func TestGitCMRedirectedGenerationOnlyKeepsResultAndTranscriptSeparate(t *testing.T) {
	repository := newGitCMRepository(t)
	withGitCMWorkingDirectory(t, repository)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "redirected generation\n")
	server, provider := newGitCMMessageProvider(t, "feat(cm): redirected generation")
	defer server.Close()
	configureGitCMProvider(t, server.URL)
	streams := terminaltest.NewRedirectedStreams("")
	capabilities := terminalexperience.Classify(terminalexperience.Facts{
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if capabilities.Interaction != terminalexperience.Automation {
		t.Fatalf("redirected capabilities = %#v, want Automation", capabilities)
	}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: capabilities,
		Input:        streams.Stdin,
		Output:       streams.Stdout,
		Diagnostics:  streams.Stderr,
	})

	result, err := executeCMForTest(context.Background(), experience, Input{DryRun: true})
	if err != nil || result.Generated == nil || provider.calls != 1 {
		t.Fatalf("redirected Run() = (%#v, %v), provider calls = %d", result, err, provider.calls)
	}
	if !strings.Contains(streams.Stdout.String(), "feat(cm): redirected generation") || streams.Stderr.Len() != 0 {
		t.Fatalf("redirected streams = (%q, %q)", streams.Stdout.String(), streams.Stderr.String())
	}
	if terminaltest.ContainsTerminalControl(append(streams.Stdout.Bytes(), streams.Stderr.Bytes()...)) {
		t.Fatalf("redirected streams contain terminal control: (%q, %q)", streams.Stdout.String(), streams.Stderr.String())
	}
}

func TestExecuteCMPresentsCommittedPartialOutcomeAfterPushFailure(t *testing.T) {
	repository := newGitCMRepository(t)
	withGitCMWorkingDirectory(t, repository)
	runGitCM(t, repository, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "partial push\n")
	runGitCM(t, repository, "add", "README.md")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	server, provider := newGitCMMessageProvider(t, "feat(cm): retain partial commit")
	defer server.Close()
	configureGitCMProvider(t, server.URL)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("\n"),
		Output:       stdout,
		Diagnostics:  stderr,
	})

	remote := "origin"
	result, err := executeCMForTest(context.Background(), experience, Input{Staged: true, Push: &remote})
	if err == nil || !result.Committed || result.Pushed || result.PushRemote != "" || provider.calls != 1 {
		t.Fatalf("Run() = (%#v, %v), provider calls = %d", result, err, provider.calls)
	}
	if !strings.Contains(stdout.String(), "Commit created") || strings.Contains(stdout.String(), "feat(cm): retain partial commit") || strings.Contains(stdout.String(), "Commit created and pushed") {
		t.Fatalf("partial stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "feat(cm): retain partial commit") || !strings.Contains(stderr.String(), "Pushing commit") {
		t.Fatalf("partial stderr = %q", stderr.String())
	}
	if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead == beforeHead {
		t.Fatalf("partial result did not retain a commit: HEAD = %q", afterHead)
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
		t.Fatalf("Plain streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
	}
}

func TestGitCMPlainCommitDecisionCancellationAndDeclineDoNotMutate(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		input string
	}{
		{name: "decline", input: "n\n"},
		{name: "cancel", input: "cancel\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newGitCMRepository(t)
			withGitCMWorkingDirectory(t, repository)
			writeGitCMFile(t, filepath.Join(repository, "README.md"), "decision\n")
			runGitCM(t, repository, "add", "README.md")
			beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
			server, provider := newGitCMMessageProvider(t, "feat(cm): decision")
			defer server.Close()
			configureGitCMProvider(t, server.URL)
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
				Input:        strings.NewReader(testCase.input),
				Output:       stdout,
				Diagnostics:  stderr,
			})

			result, err := executeCMForTest(context.Background(), experience, Input{Staged: true})
			if err != nil || !result.Cancelled || result.Committed || result.Generated == nil || provider.calls != 1 {
				t.Fatalf("Run() = (%#v, %v), provider calls = %d", result, err, provider.calls)
			}
			if got, want := stdout.String(), "Cancelled\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if !strings.Contains(stderr.String(), "Create this commit? [Y/n]:") || strings.Contains(stdout.String(), "feat(cm): decision") || strings.Contains(stdout.String(), "Commit created") {
				t.Fatalf("decision streams = (%q, %q)", stdout.String(), stderr.String())
			}
			if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
				t.Fatalf("decision changed HEAD from %q to %q", beforeHead, afterHead)
			}
			if status := gitCMOutput(t, repository, "status", "--short"); status != "A  README.md\n" {
				t.Fatalf("decision changed index = %q", status)
			}
		})
	}
}

func TestGitCMPlainProviderFailureUsesSafeProjectionAndNoMutation(t *testing.T) {
	repository := newGitCMRepository(t)
	withGitCMWorkingDirectory(t, repository)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "provider failure\n")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
		_, _ = response.Write([]byte("provider-secret /private/provider/path"))
	}))
	defer server.Close()
	configureGitCMProvider(t, server.URL)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  stderr,
	})

	result, err := executeCMForTest(context.Background(), experience, Input{})
	if err == nil || result.Generated != nil || result.Profile == (ProfileDiagnostic{}) {
		t.Fatalf("Run() = (%#v, %v), want a provider failure with safe profile context", result, err)
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(stdout.String(), "Provider: env") || strings.Contains(combined, "Commit created") || strings.Contains(combined, "provider-secret") || strings.Contains(combined, "/private/provider/path") || strings.Contains(combined, "fixture-api-key") {
		t.Fatalf("provider failure streams leaked or looked successful: (%q, %q)", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Generating commit message") {
		t.Fatalf("provider failure omitted generation phase: %q", stderr.String())
	}
	if status := gitCMOutput(t, repository, "status", "--short"); status != "?? README.md\n" {
		t.Fatalf("provider failure changed repository = %q", status)
	}
}

func TestGitCMPlainStaleScopeAndHookFailureKeepCommitUncreated(t *testing.T) {
	t.Run("stale scope", func(t *testing.T) {
		repository := newGitCMRepository(t)
		withGitCMWorkingDirectory(t, repository)
		writeGitCMFile(t, filepath.Join(repository, "README.md"), "stale scope\n")
		runGitCM(t, repository, "add", "README.md")
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("changed after capture\n"), 0o600); err != nil {
				t.Errorf("mutate stale-scope file: %v", err)
			}
			command := exec.Command("git", "-C", repository, "add", "README.md")
			command.Env = environmentWith(map[string]string{"GIT_CONFIG_NOSYSTEM": "1"})
			if output, err := command.CombinedOutput(); err != nil {
				t.Errorf("refresh stale-scope index: %v\n%s", err, output)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"choices":[{"message":{"content":"feat(cm): stale"}}]}`))
		}))
		defer server.Close()
		configureGitCMProvider(t, server.URL)
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
			Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
			Input:        strings.NewReader("\n"),
			Output:       stdout,
			Diagnostics:  stderr,
		})

		result, err := executeCMForTest(context.Background(), experience, Input{Staged: true})
		var commandErr *CommandError
		if err == nil || !result.PromptedCommit || result.Committed || !errors.As(err, &commandErr) || commandErr.Code != ErrorStaleScope {
			t.Fatalf("stale scope Run() = (%#v, %v)", result, err)
		}
		if strings.Contains(stdout.String(), "Commit created") || strings.Contains(stderr.String(), "changed after capture") {
			t.Fatalf("stale scope streams = (%q, %q)", stdout.String(), stderr.String())
		}
		if status := gitCMOutput(t, repository, "status", "--short"); status != "A  README.md\n" {
			t.Fatalf("stale scope status = %q", status)
		}
	})

	t.Run("hook failure", func(t *testing.T) {
		repository := newGitCMRepository(t)
		withGitCMWorkingDirectory(t, repository)
		writeGitCMFile(t, filepath.Join(repository, "README.md"), "hook failure\n")
		runGitCM(t, repository, "add", "README.md")
		hook := filepath.Join(repository, ".git", "hooks", "pre-commit")
		writeGitCMFile(t, hook, "#!/bin/sh\necho hook-secret /private/hook/path >&2\nexit 1\n")
		if err := os.Chmod(hook, 0o700); err != nil {
			t.Fatalf("chmod pre-commit hook: %v", err)
		}
		server, provider := newGitCMMessageProvider(t, "feat(cm): hook failure")
		defer server.Close()
		configureGitCMProvider(t, server.URL)
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
			Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
			Input:        strings.NewReader("\n"),
			Output:       stdout,
			Diagnostics:  stderr,
		})

		result, err := executeCMForTest(context.Background(), experience, Input{Staged: true})
		if err == nil || !result.PromptedCommit || result.Committed || provider.calls != 1 {
			t.Fatalf("hook failure Run() = (%#v, %v), provider calls = %d", result, err, provider.calls)
		}
		combined := stdout.String() + stderr.String()
		if strings.Contains(combined, "Commit created") || strings.Contains(combined, "hook-secret") || strings.Contains(combined, "/private/hook/path") {
			t.Fatalf("hook failure streams = (%q, %q)", stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "Creating commit") {
			t.Fatalf("hook failure omitted commit phase: %q", stderr.String())
		}
	})
}

func TestGitCMDocumentsPreserveTheExistingPlainResults(t *testing.T) {
	for _, testCase := range []struct {
		result Result
		want   string
	}{
		{result: Result{NoChanges: true, NoChangeScope: ScopeAllUncommitted}, want: "No uncommitted changes.\n"},
		{result: Result{NoChanges: true, NoChangeScope: ScopeStaged}, want: "No staged changes.\n"},
		{result: Result{NothingSelected: true}, want: "Nothing selected.\n"},
		{result: Result{Cancelled: true}, want: "Cancelled\n"},
		{result: Result{Committed: true}, want: "Commit created\n"},
		{result: Result{Pushed: true}, want: "Commit created and pushed\n"},
	} {
		if got := terminalexperience.RenderPlain(gitCMOutcomeDocument(testCase.result)); got != testCase.want {
			t.Fatalf("Outcome(%#v) = %q, want %q", testCase.result, got, testCase.want)
		}
	}
	generated := gitCMGeneratedDocument(GeneratedMessage{Message: "feat(cm): compact", Evidence: EvidenceCoverage{EstimatedLocalPromptTokens: 4000, RepresentedClusters: 2, TotalClusters: 3, IncludedFacts: 18, OmittedFacts: 13, ContentCompacted: true}}, ProfileDiagnostic{Name: "work", Model: "model"})
	if got := terminalexperience.RenderPlain(generated); !strings.Contains(got, "Provider tokens: unavailable") || !strings.Contains(got, "4,000") || !strings.Contains(got, "3 clusters represented with compacted semantic evidence") {
		t.Fatalf("generated output = %q", got)
	}
}

func configureGitCMProvider(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", "")
	t.Setenv("YCY_CM_PROFILE", "")
	t.Setenv("YCY_CM_BASE_URL", baseURL)
	t.Setenv("YCY_CM_MODEL", "fixture-model")
	t.Setenv("YCY_CM_API_KEY", "fixture-api-key")
}

type panicGitCMReader struct{}

func (panicGitCMReader) Read([]byte) (int, error) {
	panic("git cm Automation must not read stdin")
}
