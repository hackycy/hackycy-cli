package zip

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestZipPhaseCoordinatorUsesOneControlledWorkCatalogAcrossPlanning(t *testing.T) {
	run := &recordingZIPRun{}
	coordinator := newZipPhaseCoordinator(run, terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive})

	for _, update := range []terminalexperience.OperationPhase{
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseActive, Detail: "Inspecting workspace"},
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Workspace ready"},
		{ID: zipSelectSourcePhaseID, State: terminalexperience.PhaseActive, Detail: "Reviewing source"},
		{ID: zipSelectSourcePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Source selected"},
	} {
		if err := coordinator.Report(update); err != nil {
			t.Fatalf("Report(%#v) error = %v", update, err)
		}
	}

	for _, update := range []terminalexperience.OperationPhase{
		{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseActive, Detail: "Collecting files"},
		{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseCompleted, Detail: "Collected 2 files"},
	} {
		if err := coordinator.Report(update); err != nil {
			t.Fatalf("archive Report(%#v) error = %v", update, err)
		}
	}
	if err := coordinator.finish(); err != nil {
		t.Fatalf("finish() error = %v", err)
	}

	tracks, updates := run.trackSnapshot()
	if len(tracks) != 0 {
		t.Fatalf("legacy Track calls = %#v, want none", tracks)
	}
	want := []terminalexperience.OperationPhase{
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseActive, Detail: "Inspecting workspace"},
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Workspace ready"},
		{ID: zipSelectSourcePhaseID, State: terminalexperience.PhaseActive, Detail: "Reviewing source"},
		{ID: zipSelectSourcePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Source selected"},
		{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseActive, Detail: "Collecting files"},
		{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseCompleted, Detail: "Collected 2 files"},
	}
	catalogs, workUpdates, closeCount := run.workSnapshot()
	if len(catalogs) != 1 || catalogs[0].ID != zipWorkCatalogID || catalogs[0].Label != "Create archive" || !reflect.DeepEqual(catalogs[0].Phases, zipPhaseDefinitions) {
		t.Fatalf("Work Catalogs = %#v", catalogs)
	}
	if !reflect.DeepEqual(workUpdates, want) {
		t.Fatalf("Work updates = %#v, want %#v", workUpdates, want)
	}
	if len(updates) != 0 {
		t.Fatalf("legacy updates = %#v, want none", updates)
	}
	if closeCount != 1 {
		t.Fatalf("Work close count = %d, want 1", closeCount)
	}
	if err := coordinator.finish(); err != nil {
		t.Fatalf("second finish() error = %v", err)
	}
	if _, _, closeCount := run.workSnapshot(); closeCount != 1 {
		t.Fatalf("Work close count after second finish = %d, want 1", closeCount)
	}
}

func TestZipPhaseCoordinatorPlainDoesNotReplayPlanningPhases(t *testing.T) {
	run := &recordingZIPRun{}
	coordinator := newZipPhaseCoordinator(run, terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive})

	for _, update := range []terminalexperience.OperationPhase{
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseActive, Detail: "Inspecting workspace"},
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Workspace ready"},
		{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseActive, Detail: "Collecting files"},
		{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseCompleted, Detail: "Collected 2 files"},
	} {
		if err := coordinator.Report(update); err != nil {
			t.Fatalf("Report(%#v) error = %v", update, err)
		}
	}
	if err := coordinator.finish(); err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if got := run.trackCount(); got != 0 {
		t.Fatalf("Plain Track count = %d, want 0", got)
	}
	if got := run.noticeCount(); got != 4 {
		t.Fatalf("Plain notices = %d, want 4", got)
	}
}

func TestFinishTerminalZIPClassifiesContextCancellationAndRedactsFailures(t *testing.T) {
	run := &recordingZIPRun{}
	caps := terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive}
	coordinator := newZipPhaseCoordinator(run, caps)
	if err := coordinator.Report(terminalexperience.OperationPhase{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseActive, Detail: "Collecting files"}); err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	presenter := &terminalZipPresenter{run: run}
	if err := finishTerminalZIP(run, caps, coordinator, presenter, Result{}, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("finishTerminalZIP() error = %v, want context cancellation", err)
	}
	finishes := run.finishSnapshot()
	wantRequest := terminalZipFinishRequest(terminalexperience.Cancelled, Result{}, zipCollectFilesPhaseID)
	if len(finishes) != 1 || finishes[0].outcome != terminalexperience.Cancelled || !reflect.DeepEqual(finishes[0].request, wantRequest) || finishes[0].document != nil {
		t.Fatalf("finishes = %#v", finishes)
	}

	document := terminalZipResultDocument(Result{
		Kind:  ResultCollectionFailed,
		Plan:  &ZipPlan{Input: "/private/work\x1b[31m", PackageRoot: "/private"},
		Cause: errors.New("token=secret"),
	}, caps)
	text := terminalexperience.RenderPlain(document)
	for _, forbidden := range []string{"/private", "secret", "\x1b"} {
		if contains := containsZIPText(text, forbidden); contains {
			t.Fatalf("failure document leaked %q: %q", forbidden, text)
		}
	}
}

func TestTerminalZipFinishRequestUsesSafeOutcomeSummaries(t *testing.T) {
	success := Result{
		Kind:           ResultCompleted,
		Plan:           &ZipPlan{File: "release"},
		CollectedCount: 5,
		IncludedCount:  3,
		RevealFailed:   true,
	}
	successSummary := terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / zip"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Archive complete"},
		{Role: terminalexperience.VisualRoleSuccess, Text: "Archive created"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Collected 5; included 3; output release.zip"},
		{Role: terminalexperience.VisualRoleWarning, Text: "Archive created; host reveal unavailable"},
	}}

	tests := []struct {
		name     string
		outcome  terminalexperience.FinishOutcome
		result   Result
		location string
		want     terminalexperience.FinishRequest
	}{
		{
			name:    "success keeps counts basename and reveal warning",
			outcome: terminalexperience.Succeeded,
			result:  success,
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Succeeded,
				Summary: successSummary,
			},
		},
		{
			name:     "planning cancellation retains a safe phase",
			outcome:  terminalexperience.Cancelled,
			result:   Result{Kind: ResultCancelled},
			location: zipSelectPatternsPhaseID,
			want: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Cancelled,
				Location: "Select patterns",
				Summary: terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
					{Role: terminalexperience.VisualRoleMuted, Text: "YCY / zip"},
					{Role: terminalexperience.VisualRoleTitle, Text: "Archive outcome"},
					{Role: terminalexperience.VisualRoleWarning, Text: "Archive planning cancelled"},
				}},
			},
		},
		{
			name:     "collection failure excludes raw archive data",
			outcome:  terminalexperience.Failed,
			result:   Result{Kind: ResultCollectionFailed, Plan: &ZipPlan{Input: "/private/token", File: "https://secret.example/archive"}, OutputPath: "/private/token/archive.zip", Cause: errors.New("token=secret")},
			location: zipCollectFilesPhaseID,
			want: terminalexperience.FinishRequest{
				Outcome:  terminalexperience.Failed,
				Location: "Collect files",
				Summary: terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
					{Role: terminalexperience.VisualRoleMuted, Text: "YCY / zip"},
					{Role: terminalexperience.VisualRoleTitle, Text: "Archive outcome"},
					{Role: terminalexperience.VisualRoleError, Text: "Archive failed (collection)"},
				}},
			},
		},
		{
			name:     "unknown failure uses no unsafe location",
			outcome:  terminalexperience.Failed,
			location: "unsafe\nlocation",
			want: terminalexperience.FinishRequest{
				Outcome: terminalexperience.Failed,
				Summary: terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
					{Role: terminalexperience.VisualRoleMuted, Text: "YCY / zip"},
					{Role: terminalexperience.VisualRoleTitle, Text: "Archive outcome"},
					{Role: terminalexperience.VisualRoleError, Text: "Archive failed (archive)"},
				}},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := terminalZipFinishRequest(testCase.outcome, testCase.result, testCase.location)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("Finish request = %#v, want %#v", got, testCase.want)
			}
			text := terminalexperience.RenderPlain(got.Summary)
			if terminaltest.ContainsTerminalControl([]byte(text)) {
				t.Fatalf("summary contains terminal controls: %q", text)
			}
			for _, forbidden := range []string{"/private", "secret", "token="} {
				if containsZIPText(text, forbidden) {
					t.Fatalf("summary leaked %q: %q", forbidden, text)
				}
			}
		})
	}

	for _, failure := range []struct {
		kind     ResultKind
		category string
	}{
		{ResultDirectoryNotFound, "directory"},
		{ResultPathNotDirectory, "path"},
		{ResultNoFiles, "no-files"},
		{ResultNoValidFiles, "no-valid-files"},
		{ResultCollectionFailed, "collection"},
		{ResultCompressionFailed, "compression"},
		{ResultWriteFailed, "write"},
	} {
		request := terminalZipFinishRequest(terminalexperience.Failed, Result{Kind: failure.kind}, zipWriteArchivePhaseID)
		if request.Location != "Write archive" || !containsZIPText(terminalexperience.RenderPlain(request.Summary), "Archive failed ("+failure.category+")") {
			t.Fatalf("failure %q request = %#v", failure.kind, request)
		}
	}
}

func TestFinishTerminalZIPSubmitsFinishRequestAndSeparateResult(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	caps := terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive}
	coordinator := newZipPhaseCoordinator(run, caps)
	for _, update := range []terminalexperience.OperationPhase{
		{ID: zipWriteArchivePhaseID, State: terminalexperience.PhaseActive, Detail: "Publishing completed archive"},
		{ID: zipWriteArchivePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Archive published"},
	} {
		if err := coordinator.Report(update); err != nil {
			t.Fatalf("Report(%#v) error = %v", update, err)
		}
	}
	result := Result{Kind: ResultCompleted, Plan: &ZipPlan{File: "release"}, CollectedCount: 4, IncludedCount: 3}
	if err := finishTerminalZIP(run, caps, coordinator, &terminalZipPresenter{run: run}, result, nil); err != nil {
		t.Fatalf("finishTerminalZIP() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) == 0 || operations[len(operations)-1].Kind != terminaltest.FinishOperation {
		t.Fatalf("operations = %#v", operations)
	}
	finish := operations[len(operations)-1].Value.(terminaltest.Finish)
	wantRequest := terminalZipFinishRequest(terminalexperience.Succeeded, result, "Write archive")
	if !reflect.DeepEqual(finish.Request, wantRequest) || !reflect.DeepEqual(finish.Value.(terminalexperience.FinishRequest), wantRequest) {
		t.Fatalf("Finish = %#v, want request %#v", finish, wantRequest)
	}
	wantResult := terminalZipResultDocument(result, caps)
	if len(finish.Documents) != 1 || finish.Documents[0] == nil || !reflect.DeepEqual(*finish.Documents[0], wantResult) {
		t.Fatalf("durable Result = %#v, want %#v", finish.Documents, wantResult)
	}
	if reflect.DeepEqual(finish.Request.Summary, wantResult) {
		t.Fatalf("Finish summary must not be derived from the durable Result: %#v", finish)
	}
}

func TestModuleRunContextReportsArchivePhaseOrderAndKeepsArchiveBytes(t *testing.T) {
	root := t.TempDir()
	writeZipFile(t, root+"/package.json", `{"name":"project"}`)
	writeZipFile(t, root+"/index.html", "<main />")
	phases := &recordingZIPPhases{}
	module := newZipModule(t, Dependencies{
		Prompter: selectFirstZipPrompter{output: "release"},
		Phases:   phases,
	})

	result, err := module.RunContext(context.Background(), Input{Directory: root, Open: false, WithDir: "bundle"})
	if err != nil || result.Kind != ResultCompleted || result.IncludedCount != 2 {
		t.Fatalf("RunContext() = (%#v, %v)", result, err)
	}
	wantIDs := []string{
		zipDiscoverWorkspacePhaseID,
		zipDiscoverWorkspacePhaseID,
		zipSelectSourcePhaseID,
		zipSelectSourcePhaseID,
		zipSelectPatternsPhaseID,
		zipSelectPatternsPhaseID,
		zipPrepareArchivePhaseID,
		zipPrepareArchivePhaseID,
		zipCollectFilesPhaseID,
		zipCollectFilesPhaseID,
		zipCompressFilesPhaseID,
		zipCompressFilesPhaseID,
		zipWriteArchivePhaseID,
		zipWriteArchivePhaseID,
	}
	if got := phases.ids(); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("phase IDs = %#v, want %#v", got, wantIDs)
	}
	if detail := phases.finalDetail(zipPrepareArchivePhaseID); detail != "Source: .; Output: release.zip; with-dir: on" {
		t.Fatalf("prepare detail = %q", detail)
	}
}

type recordingZIPPhases struct {
	updates []terminalexperience.OperationPhase
}

func (reporter *recordingZIPPhases) Report(update terminalexperience.OperationPhase) error {
	reporter.updates = append(reporter.updates, update)
	return nil
}

func (reporter *recordingZIPPhases) ids() []string {
	ids := make([]string, 0, len(reporter.updates))
	for _, update := range reporter.updates {
		ids = append(ids, update.ID)
	}
	return ids
}

func (reporter *recordingZIPPhases) finalDetail(id string) string {
	for index := len(reporter.updates) - 1; index >= 0; index-- {
		if reporter.updates[index].ID == id {
			return reporter.updates[index].Detail
		}
	}
	return ""
}

type recordedZIPFinish struct {
	outcome  terminalexperience.FinishOutcome
	request  terminalexperience.FinishRequest
	document *terminalexperience.PresentationDocument
}

type recordingZIPRun struct {
	mu           sync.Mutex
	notices      []terminalexperience.PresentationDocument
	tracks       []terminalexperience.TrackedOperation
	updates      []terminalexperience.OperationPhase
	workCatalogs []terminalexperience.WorkCatalog
	workUpdates  []terminalexperience.OperationPhase
	workCloses   int
	finishes     []recordedZIPFinish
}

func (run *recordingZIPRun) Ask(terminalexperience.InteractionRequest) (terminalexperience.InteractionAnswer, error) {
	return terminalexperience.InteractionAnswer{}, errors.New("unexpected interaction")
}

func (run *recordingZIPRun) Track(operation terminalexperience.TrackedOperation) error {
	run.mu.Lock()
	run.tracks = append(run.tracks, operation)
	run.mu.Unlock()
	for update := range operation.Updates {
		run.mu.Lock()
		run.updates = append(run.updates, update)
		run.mu.Unlock()
	}
	return nil
}

func (run *recordingZIPRun) StartWork(catalog terminalexperience.WorkCatalog) (terminalexperience.WorkSession, error) {
	run.mu.Lock()
	run.workCatalogs = append(run.workCatalogs, catalog)
	run.mu.Unlock()
	return &recordingZIPWorkSession{run: run}, nil
}

func (run *recordingZIPRun) Notice(document terminalexperience.PresentationDocument) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.notices = append(run.notices, document)
	return nil
}

func (*recordingZIPRun) Milestone(terminalexperience.PresentationDocument) error { return nil }

func (run *recordingZIPRun) Finish(value any, documents ...*terminalexperience.PresentationDocument) error {
	run.mu.Lock()
	defer run.mu.Unlock()
	var outcome terminalexperience.FinishOutcome
	var document *terminalexperience.PresentationDocument
	switch request := value.(type) {
	case terminalexperience.FinishRequest:
		outcome = request.Outcome
		run.finishes = append(run.finishes, recordedZIPFinish{outcome: outcome, request: request, document: firstZIPDocument(documents)})
		return nil
	case terminalexperience.FinishOutcome:
		outcome = request
	}
	if len(documents) == 1 {
		document = documents[0]
	}
	run.finishes = append(run.finishes, recordedZIPFinish{outcome: outcome, document: document})
	return nil
}

func firstZIPDocument(documents []*terminalexperience.PresentationDocument) *terminalexperience.PresentationDocument {
	if len(documents) == 1 {
		return documents[0]
	}
	return nil
}

func (*recordingZIPRun) ResultCheckpoint(string, terminalexperience.PresentationDocument) error {
	return nil
}
func (*recordingZIPRun) Result(terminalexperience.PresentationDocument) error { return nil }
func (*recordingZIPRun) Close() error                                         { return nil }

type recordingZIPWorkSession struct {
	run *recordingZIPRun
}

func (session *recordingZIPWorkSession) Update(update terminalexperience.OperationPhase) error {
	session.run.mu.Lock()
	session.run.workUpdates = append(session.run.workUpdates, update)
	session.run.mu.Unlock()
	return nil
}

func (session *recordingZIPWorkSession) Close() error {
	session.run.mu.Lock()
	session.run.workCloses++
	session.run.mu.Unlock()
	return nil
}

func (run *recordingZIPRun) trackCount() int {
	run.mu.Lock()
	defer run.mu.Unlock()
	return len(run.tracks)
}

func (run *recordingZIPRun) noticeCount() int {
	run.mu.Lock()
	defer run.mu.Unlock()
	return len(run.notices)
}

func (run *recordingZIPRun) trackSnapshot() ([]terminalexperience.TrackedOperation, []terminalexperience.OperationPhase) {
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]terminalexperience.TrackedOperation(nil), run.tracks...), append([]terminalexperience.OperationPhase(nil), run.updates...)
}

func (run *recordingZIPRun) workSnapshot() ([]terminalexperience.WorkCatalog, []terminalexperience.OperationPhase, int) {
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]terminalexperience.WorkCatalog(nil), run.workCatalogs...), append([]terminalexperience.OperationPhase(nil), run.workUpdates...), run.workCloses
}

func (run *recordingZIPRun) finishSnapshot() []recordedZIPFinish {
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]recordedZIPFinish(nil), run.finishes...)
}

func containsZIPText(value, target string) bool {
	return len(target) > 0 && len(value) >= len(target) && containsZIPSubstring(value, target)
}

func containsZIPSubstring(value, target string) bool {
	for index := 0; index+len(target) <= len(value); index++ {
		if value[index:index+len(target)] == target {
			return true
		}
	}
	return false
}
