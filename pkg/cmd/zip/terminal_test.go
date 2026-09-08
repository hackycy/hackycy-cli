package zip

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

func TestZipPhaseCoordinatorDefersRichTrackingUntilArchiveWork(t *testing.T) {
	run := &recordingZIPRun{}
	coordinator := newZipPhaseCoordinator(run, terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive})

	for _, update := range []terminalexperience.OperationPhase{
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseActive, Detail: "Inspecting workspace"},
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Workspace ready"},
	} {
		if err := coordinator.Report(update); err != nil {
			t.Fatalf("planning Report(%#v) error = %v", update, err)
		}
	}
	if got := run.trackCount(); got != 0 {
		t.Fatalf("planning started Track %d time(s), want 0", got)
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
	if len(tracks) != 1 || tracks[0].ID != "zip-archive" || !reflect.DeepEqual(tracks[0].Phases, zipPhaseDefinitions) {
		t.Fatalf("tracks = %#v", tracks)
	}
	want := []terminalexperience.OperationPhase{
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseActive, Detail: "Workspace ready"},
		{ID: zipDiscoverWorkspacePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Workspace ready"},
		{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseActive, Detail: "Collecting files"},
		{ID: zipCollectFilesPhaseID, State: terminalexperience.PhaseCompleted, Detail: "Collected 2 files"},
	}
	if !reflect.DeepEqual(updates, want) {
		t.Fatalf("updates = %#v, want %#v", updates, want)
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
	if len(finishes) != 1 || finishes[0].outcome != terminalexperience.Cancelled || finishes[0].document != nil {
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
	document *terminalexperience.PresentationDocument
}

type recordingZIPRun struct {
	mu       sync.Mutex
	notices  []terminalexperience.PresentationDocument
	tracks   []terminalexperience.TrackedOperation
	updates  []terminalexperience.OperationPhase
	finishes []recordedZIPFinish
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
	if legacy, ok := value.(terminalexperience.FinishOutcome); ok {
		outcome = legacy
		if len(documents) == 1 {
			document = documents[0]
		}
	}
	run.finishes = append(run.finishes, recordedZIPFinish{outcome: outcome, document: document})
	return nil
}

func (*recordingZIPRun) ResultCheckpoint(string, terminalexperience.PresentationDocument) error {
	return nil
}
func (*recordingZIPRun) Result(terminalexperience.PresentationDocument) error { return nil }
func (*recordingZIPRun) Close() error                                         { return nil }

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
