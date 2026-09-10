package fork

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/hackycy/hackycy-cli/internal/logging"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

var errGitForkRequiresInteractive = errors.New("git fork requires an interactive terminal")

func runFork(options *Options) error {
	_, err := executeFork(options)
	return err
}

func executeFork(options *Options) (Result, error) {
	if options == nil || options.Context == nil || options.Config == nil || options.WorkingDirectory == nil || options.HTTP == nil || options.Terminal == nil || options.Git == nil {
		return Result{}, errors.New("git fork options are incomplete")
	}
	ctx, cancel := context.WithCancel(options.Context)
	defer cancel()
	run, err := options.Terminal.OpenConsole(ctx, gitForkConsoleDescriptor(options.Repository, options.Destination))
	if err != nil {
		return Result{}, err
	}
	defer run.Close()
	adapter := newTerminalGitForkAdapter(run, cancel)
	adapter.enableDetailed()
	if options.Terminal.Capabilities().Interaction == terminalexperience.RichInteractive {
		adapter.enableRichResult()
		adapter.enableControlledWork()
	}

	store, err := options.Config()
	if err != nil {
		return Result{}, errors.Join(err, adapter.finish(terminalexperience.Failed, Result{}, nil))
	}
	provider, err := NewProviderClient(options.HTTP)
	if err != nil {
		return Result{}, errors.Join(err, adapter.finish(terminalexperience.Failed, Result{}, nil))
	}
	module, err := New(Dependencies{
		Config:           store,
		WorkingDirectory: options.WorkingDirectory,
		Directories:      osForkDirectoryReader{},
		Prompter:         adapter,
		Provider:         provider,
		Extractor:        OSArchiveExtractor{},
		CloneRunner:      gitRunnerAdapter{runner: options.Git},
		Remover:          osForkDirectoryRemover{},
		Presenter:        adapter,
		Tracker:          adapter,
	})
	if err != nil {
		return Result{}, errors.Join(err, adapter.finish(terminalexperience.Failed, Result{}, nil))
	}
	result, err := module.Run(ctx, Input{Repository: options.Repository, Destination: options.Destination})
	if err != nil {
		adapter.recordFailure(result, err)
	}
	if flushErr := adapter.flushDetailed(); flushErr != nil {
		err = errors.Join(err, flushErr)
	}
	if err != nil {
		outcome := terminalexperience.Failed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = terminalexperience.Cancelled
		}
		return result, errors.Join(err, adapter.finish(outcome, result, nil))
	}
	outcome := terminalexperience.Succeeded
	if result.Cancelled {
		outcome = terminalexperience.Cancelled
	}
	document := adapter.resultDocument(result)
	return result, errors.Join(adapter.finish(outcome, result, &document))
}

type osForkDirectoryReader struct{}

func (osForkDirectoryReader) ReadDir(path string) ([]fs.DirEntry, error) {
	return os.ReadDir(path)
}

type osForkDirectoryRemover struct{}

func (osForkDirectoryRemover) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

type terminalGitForkAdapter struct {
	run           terminalexperience.ExperienceRun
	requestCancel context.CancelFunc

	mu          sync.Mutex
	detailed    bool
	rich        bool
	controlled  bool
	work        terminalexperience.WorkSession
	workClosed  bool
	workErr     error
	updates     chan terminalexperience.OperationPhase
	done        chan error
	started     bool
	closed      bool
	lastPhaseID string
	pending     []terminalexperience.OperationPhase
	milestones  []terminalexperience.PresentationDocument
	result      *terminalexperience.PresentationDocument
}

func newTerminalGitForkAdapter(run terminalexperience.ExperienceRun, requestCancel context.CancelFunc) *terminalGitForkAdapter {
	return &terminalGitForkAdapter{run: run, requestCancel: requestCancel}
}

func (adapter *terminalGitForkAdapter) enableDetailed() {
	adapter.mu.Lock()
	adapter.detailed = true
	adapter.mu.Unlock()
}

func (adapter *terminalGitForkAdapter) enableRichResult() {
	adapter.mu.Lock()
	adapter.rich = true
	adapter.mu.Unlock()
}

func (adapter *terminalGitForkAdapter) enableControlledWork() {
	adapter.mu.Lock()
	adapter.controlled = true
	adapter.mu.Unlock()
}

func (adapter *terminalGitForkAdapter) ConfirmOverwrite(prompt OverwritePrompt) (bool, bool, error) {
	answer, err := adapter.run.Ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionConfirm,
		Message:         prompt.Message,
		ConsoleStepID:   gitForkOverwriteFormID,
		TranscriptLabel: "Destination replacement",
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string {
			if answer.Confirmed {
				return "confirmed"
			}
			return "declined"
		},
		HasDefault:   true,
		Default:      terminalexperience.InteractionAnswer{Confirmed: true},
		CancelValues: []string{"q", "quit", "cancel"},
		PlainPrompt:  prompt.Message + " [Y/n]: ",
		ParsePlain: func(value string) (terminalexperience.InteractionAnswer, error) {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "y", "yes":
				return terminalexperience.InteractionAnswer{Confirmed: true}, nil
			case "n", "no":
				return terminalexperience.InteractionAnswer{Confirmed: false}, nil
			default:
				return terminalexperience.InteractionAnswer{}, errors.New("Invalid confirmation")
			}
		},
	})
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) || errors.Is(err, context.Canceled) {
		return false, true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return false, false, errGitForkRequiresInteractive
	}
	if err != nil {
		return false, false, err
	}
	return answer.Confirmed, false, nil
}

func (adapter *terminalGitForkAdapter) Introduction() {
	_ = adapter.run.Notice(gitForkIntroductionDocument())
}

func (adapter *terminalGitForkAdapter) Cancelled() {
	adapter.mu.Lock()
	if adapter.detailed {
		document := gitForkCancelledDocument()
		adapter.result = &document
		adapter.mu.Unlock()
		return
	}
	adapter.mu.Unlock()
	_ = adapter.run.Result(gitForkCancelledDocument())
}

func (adapter *terminalGitForkAdapter) Outcome(result Result) {
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleSuccess, Text: "Project ready"}}})
		document := gitForkOutcomeDocument(result)
		if adapter.rich {
			document = gitForkOutcomeDocumentDetailed(result)
		}
		adapter.result = &document
		adapter.mu.Unlock()
		return
	}
	adapter.mu.Unlock()
	_ = adapter.run.Result(gitForkOutcomeDocument(result))
}

func (adapter *terminalGitForkAdapter) Start(_ context.Context) (PhaseReporter, error) {
	adapter.mu.Lock()
	if adapter.detailed {
		if adapter.controlled {
			adapter.mu.Unlock()
			return &terminalGitForkPhaseReporter{adapter: adapter, detailed: true, controlled: true}, nil
		}
		if adapter.started {
			adapter.mu.Unlock()
			return nil, errors.New("git fork tracker already started")
		}
		adapter.updates = make(chan terminalexperience.OperationPhase, 128)
		adapter.done = make(chan error, 1)
		adapter.started = true
		pending := append([]terminalexperience.OperationPhase(nil), adapter.pending...)
		adapter.pending = nil
		updates := adapter.updates
		done := adapter.done
		adapter.mu.Unlock()
		go func() {
			err := adapter.run.Track(terminalexperience.TrackedOperation{
				ID:            "git-fork",
				OperationID:   "git-fork",
				Label:         "Git Fork",
				Phases:        append([]terminalexperience.PhaseDefinition(nil), forkPhaseDefinitions...),
				Updates:       updates,
				RequestCancel: adapter.requestCancel,
			})
			done <- err
		}()
		for _, update := range pending {
			updates <- update
		}
		return &terminalGitForkPhaseReporter{adapter: adapter, detailed: true}, nil
	}
	adapter.mu.Unlock()
	reporter := &terminalGitForkPhaseReporter{
		updates:  make(chan terminalexperience.OperationPhase, 1),
		finished: make(chan struct{}),
	}
	go func() {
		err := adapter.run.Track(terminalexperience.TrackedOperation{
			Label:         "Git Fork",
			Updates:       reporter.updates,
			RequestCancel: adapter.requestCancel,
		})
		reporter.complete(err)
	}()
	return reporter, nil
}

// reportForkPhase is the detailed phase bridge used by Module.Run. Updates
// that occur before Track starts are retained and replayed into the catalog.
func (adapter *terminalGitForkAdapter) reportForkPhase(id string, state PhaseState, detail string) {
	update := terminalexperience.OperationPhase{ID: id, State: terminalGitForkPhaseState(state), Detail: safeForkText(detail, "phase")}
	adapter.mu.Lock()
	controlled := adapter.detailed && adapter.controlled
	adapter.mu.Unlock()
	if controlled {
		adapter.reportControlledForkPhase(update)
		return
	}
	adapter.mu.Lock()
	if !adapter.detailed || adapter.closed {
		adapter.mu.Unlock()
		return
	}
	adapter.lastPhaseID = id
	if !adapter.started {
		adapter.pending = append(adapter.pending, update)
		if state == PhaseActive {
			adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: forkLegacyPhaseLabel(id)}}})
		}
		adapter.mu.Unlock()
		return
	}
	if state == PhaseActive {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: forkLegacyPhaseLabel(id)}}})
	}
	adapter.updates <- update
	adapter.mu.Unlock()
}

func (adapter *terminalGitForkAdapter) reportControlledForkPhase(update terminalexperience.OperationPhase) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.lastPhaseID = update.ID
	if adapter.workClosed {
		return
	}
	if adapter.work == nil {
		work, err := terminalexperience.StartWork(adapter.run, gitForkWorkCatalog(adapter.requestCancel))
		if err != nil {
			adapter.workErr = errors.Join(adapter.workErr, err)
			return
		}
		adapter.work = work
	}
	if err := adapter.work.Update(update); err != nil {
		adapter.workErr = errors.Join(adapter.workErr, err)
	}
}

func (adapter *terminalGitForkAdapter) reportForkMilestone(text string) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if !adapter.detailed || adapter.closed {
		return
	}
	adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: safeForkText(text, "Fork update")}}})
}

func (adapter *terminalGitForkAdapter) flushDetailed() error {
	adapter.mu.Lock()
	if !adapter.detailed {
		adapter.mu.Unlock()
		return nil
	}
	if adapter.controlled {
		work := adapter.work
		workClosed := adapter.workClosed
		if work != nil {
			adapter.workClosed = true
		}
		workErr := adapter.workErr
		milestones := append([]terminalexperience.PresentationDocument(nil), adapter.milestones...)
		adapter.milestones = nil
		adapter.mu.Unlock()
		if work != nil && !workClosed {
			workErr = errors.Join(workErr, work.Close())
		}
		var result error
		for _, milestone := range milestones {
			result = errors.Join(result, adapter.run.Milestone(milestone))
		}
		return errors.Join(workErr, result)
	}
	// A prompt cancellation or an early failure can finish before the normal
	// acquisition Track starts. Replay the reached catalog entries in a
	// one-shot Track so the Transcript still describes what actually ran.
	pending := append([]terminalexperience.OperationPhase(nil), adapter.pending...)
	adapter.pending = nil
	started := adapter.started
	milestones := append([]terminalexperience.PresentationDocument(nil), adapter.milestones...)
	adapter.milestones = nil
	adapter.mu.Unlock()
	var trackErr error
	if !started && len(pending) > 0 {
		updates := make(chan terminalexperience.OperationPhase, len(pending))
		done := make(chan error, 1)
		go func() {
			done <- adapter.run.Track(terminalexperience.TrackedOperation{
				ID:          "git-fork-final",
				OperationID: "git-fork-final",
				Label:       "Git Fork",
				Phases:      append([]terminalexperience.PhaseDefinition(nil), forkPhaseDefinitions...),
				Updates:     updates,
			})
		}()
		for _, update := range pending {
			updates <- update
		}
		close(updates)
		trackErr = <-done
	}
	var result error
	for _, milestone := range milestones {
		result = errors.Join(result, adapter.run.Milestone(milestone))
	}
	return errors.Join(trackErr, result)
}

func (adapter *terminalGitForkAdapter) DestinationUnreadable() {
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "Destination inspection unavailable; continuing"}}})
	}
	adapter.mu.Unlock()
}

func (adapter *terminalGitForkAdapter) recordFailure(result Result, err error) {
	adapter.mu.Lock()
	if !adapter.detailed {
		adapter.mu.Unlock()
		return
	}
	if result.DiskFact != "" {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: safeForkText(result.DiskFact, "Filesystem state recorded")}}})
	}
	if err != nil {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleError, Text: "Git Fork operation failed"}}})
	}
	adapter.mu.Unlock()
}

func (adapter *terminalGitForkAdapter) resultDocument(result Result) terminalexperience.PresentationDocument {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.result != nil {
		return *adapter.result
	}
	if !adapter.rich {
		return gitForkOutcomeDocument(result)
	}
	return gitForkOutcomeDocumentDetailed(result)
}

func (adapter *terminalGitForkAdapter) finish(outcome terminalexperience.FinishOutcome, result Result, document *terminalexperience.PresentationDocument) error {
	adapter.mu.Lock()
	location := adapter.lastPhaseID
	adapter.mu.Unlock()
	return adapter.run.Finish(terminalGitForkFinishRequest(outcome, result, location), document)
}

func terminalGitForkFinishRequest(outcome terminalexperience.FinishOutcome, result Result, location string) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{
		Outcome:  outcome,
		Location: gitForkFinishLocation(location),
	}
	switch outcome {
	case terminalexperience.Succeeded:
		request.Summary = terminalGitForkSuccessSummary(result)
	case terminalexperience.Cancelled:
		request.Summary = terminalGitForkCancellationSummary(result)
	case terminalexperience.Failed:
		request.Summary = terminalGitForkFailureSummary(result, request.Location)
	}
	return request
}

func terminalGitForkSuccessSummary(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / git fork"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Project acquired"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Repository: " + safeForkRepositoryDetail(result.Repository)},
	}
	if result.Ref != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Ref: " + safeForkRefDetail(result.Ref)})
	}
	switch result.Acquisition {
	case acquisitionArchive:
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: "Acquired via archive"},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "History: not included"},
		)
	case acquisitionClone:
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: "Acquired via git clone"},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Git metadata: removed"},
		)
	}
	if result.DefaultBranchError != nil {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Default branch unavailable; used remote default"})
	}
	if result.ArchiveError != nil {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Archive unavailable; used git clone fallback"})
	}
	if fact := safeForkDiskFact(result.DiskFact); fact != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: fact})
	}
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Destination: " + safeForkDestinationDetail(result.Destination)})
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalGitForkCancellationSummary(result Result) terminalexperience.PresentationDocument {
	message := "Git Fork operation cancelled"
	if result.Cancelled {
		message = "Destination replacement cancelled"
	}
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / git fork"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Project acquisition cancelled"},
		{Role: terminalexperience.VisualRoleWarning, Text: message},
	}
	if fact := safeForkDiskFact(result.DiskFact); fact != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: fact})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalGitForkFailureSummary(result Result, location string) terminalexperience.PresentationDocument {
	message := "Git Fork operation failed"
	switch location {
	case "Resolve repository":
		message = "Unable to resolve repository"
	case "Inspect destination":
		message = "Unable to inspect destination"
	case "Replace destination":
		message = "Unable to replace destination"
	case "Resolve default branch":
		message = "Unable to resolve default branch"
	case "Download archive":
		message = "Unable to download archive"
	case "Extract archive":
		message = "Unable to extract archive"
	case "Clone fallback":
		message = "Unable to clone project"
	case "Remove Git metadata":
		message = "Unable to remove Git metadata"
	}
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / git fork"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Project acquisition failed"},
		{Role: terminalexperience.VisualRoleError, Text: message},
	}
	if fact := safeForkDiskFact(result.DiskFact); fact != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: fact})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func gitForkFinishLocation(location string) string {
	for _, definition := range forkPhaseDefinitions {
		if location == definition.ID || location == definition.Name {
			return definition.Name
		}
	}
	return ""
}

func safeForkDiskFact(value string) string {
	switch value {
	case "Destination may contain partially replaced files",
		"Destination may contain partially extracted files",
		"Destination may contain a partial clone",
		"Project files created; Git metadata remains":
		return value
	default:
		return ""
	}
}

func forkLegacyPhaseLabel(id string) string {
	switch id {
	case forkResolveRepositoryPhaseID:
		return "Resolving repository"
	case forkInspectDestinationPhaseID:
		return "Inspecting destination"
	case forkReplaceDestinationPhaseID:
		return "Replacing destination"
	case forkResolveDefaultBranchPhaseID:
		return "Fetching default branch"
	case forkDownloadArchivePhaseID:
		return "Downloading archive"
	case forkExtractArchivePhaseID:
		return "Extracting archive"
	case forkCloneFallbackPhaseID:
		return "Falling back to git clone"
	case forkRemoveGitMetadataPhaseID:
		return "Removing Git metadata"
	default:
		return "Git Fork"
	}
}

type terminalGitForkPhaseReporter struct {
	adapter    *terminalGitForkAdapter
	detailed   bool
	controlled bool
	updates    chan terminalexperience.OperationPhase
	finished   chan struct{}

	mu     sync.Mutex
	closed bool
	err    error
}

func (reporter *terminalGitForkPhaseReporter) Report(phase Phase) {
	if reporter.detailed {
		// Detailed catalog updates are emitted by reportForkPhase. The typed
		// tracker remains part of the module contract but must not duplicate
		// catalog rows in the B Console.
		return
	}
	update := terminalGitForkPhase(phase)
	select {
	case reporter.updates <- update:
	case <-reporter.finished:
	}
}

func (reporter *terminalGitForkPhaseReporter) Close() error {
	if reporter.detailed {
		if reporter.controlled {
			return nil
		}
		reporter.adapter.mu.Lock()
		if !reporter.adapter.closed {
			reporter.adapter.closed = true
			close(reporter.adapter.updates)
		}
		done := reporter.adapter.done
		reporter.adapter.mu.Unlock()
		err := <-done
		reporter.adapter.mu.Lock()
		reporter.adapter.mu.Unlock()
		return err
	}
	reporter.mu.Lock()
	if !reporter.closed {
		close(reporter.updates)
		reporter.closed = true
	}
	reporter.mu.Unlock()
	<-reporter.finished
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	return reporter.err
}

func (reporter *terminalGitForkPhaseReporter) complete(err error) {
	reporter.mu.Lock()
	reporter.err = err
	reporter.mu.Unlock()
	close(reporter.finished)
}

func terminalGitForkPhase(phase Phase) terminalexperience.OperationPhase {
	name := "Resolving repository"
	detail := phase.Repository
	switch phase.Kind {
	case PhaseDefaultBranch:
		name = "Fetching default branch"
		detail = phase.Ref
	case PhaseArchive:
		name = "Downloading archive"
		detail = phase.Ref
	case PhaseClone:
		name = "Falling back to git clone"
		detail = phase.Destination
	case PhaseReady:
		name = "Project ready"
		detail = phase.Destination
	}
	return terminalexperience.OperationPhase{Name: name, Detail: detail, State: terminalGitForkPhaseState(phase.State)}
}

func terminalGitForkPhaseState(state PhaseState) terminalexperience.PhaseState {
	switch state {
	case PhaseCompleted:
		return terminalexperience.PhaseCompleted
	case PhaseCancelled:
		return terminalexperience.PhaseCancelled
	case PhaseFailed:
		return terminalexperience.PhaseFailed
	default:
		return terminalexperience.PhaseActive
	}
}

func phaseStateForForkError(ctx context.Context, err error) PhaseState {
	if err != nil && ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return PhaseCancelled
	}
	return PhaseFailed
}

func gitForkIntroductionDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleTitle, Text: "HACKYCY CLI"},
		{Role: terminalexperience.VisualRoleActive, Text: "Git Fork"},
	}}
}

func gitForkCancelledDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "Cancelled"}}}
}

func gitForkOutcomeDocument(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{
		gitForkBlock(terminalexperience.VisualRoleMuted, fmt.Sprintf("Resolved: %s/%s/%s (%s)", result.Repository.Host, result.Repository.Owner, result.Repository.Name, result.Repository.ProviderType)),
	}
	if result.Repository.Ref == "" && result.Ref != "" {
		blocks = append(blocks, gitForkBlock(terminalexperience.VisualRoleActive, "Branch: "+result.Ref))
	}
	if result.DefaultBranchError != nil {
		blocks = append(blocks,
			gitForkBlock(terminalexperience.VisualRoleError, "Failed to get default branch: "+logging.Redact(result.DefaultBranchError.Error())),
			gitForkBlock(terminalexperience.VisualRoleWarning, "Falling back to git clone with remote default branch."),
		)
	}
	if result.ArchiveError != nil {
		blocks = append(blocks,
			gitForkBlock(terminalexperience.VisualRoleError, "Archive download failed: "+logging.Redact(result.ArchiveError.Error())),
			gitForkBlock(terminalexperience.VisualRoleWarning, "Falling back to git clone..."),
		)
	}
	switch result.Acquisition {
	case "archive":
		blocks = append(blocks, gitForkBlock(terminalexperience.VisualRoleSuccess, "Archive downloaded and extracted"))
	case "clone":
		if result.ArchiveError == nil {
			blocks = append(blocks, gitForkBlock(terminalexperience.VisualRoleWarning, "Falling back to git clone..."))
		}
		blocks = append(blocks, gitForkBlock(terminalexperience.VisualRoleSuccess, "Cloned and cleaned up"))
	}
	blocks = append(blocks, gitForkBlock(terminalexperience.VisualRoleSuccess, "Done! Project created at "+result.Destination))
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

// gitForkOutcomeDocumentDetailed is the B durable result used by the command
// adapter. The legacy document above remains available to direct adapter
// callers and preserves its established wording and ordering.
func gitForkOutcomeDocumentDetailed(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / git fork"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Project acquired"},
		// Keep the established durable summary phrases in the new B document;
		// redirected and Plain callers rely on these exact semantic markers.
		{Role: terminalexperience.VisualRoleMuted, Text: "Resolved: " + safeForkRepositoryDetail(result.Repository)},
		{Role: terminalexperience.VisualRoleMuted, Text: "Repository: " + safeForkRepositoryDetail(result.Repository)},
	}
	if result.Ref != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Ref: " + safeForkRefDetail(result.Ref)})
		if result.Repository.Ref == "" {
			blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleActive, Text: "Branch: " + safeForkRefDetail(result.Ref)})
		}
	}
	if result.Acquisition != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: "Acquired via " + safeForkText(string(result.Acquisition), "provider")})
		switch result.Acquisition {
		case acquisitionArchive:
			blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: "Archive downloaded and extracted"})
		case acquisitionClone:
			blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: "Cloned and cleaned up"})
		}
	}
	if result.DefaultBranchError != nil {
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleError, Text: "Failed to get default branch"},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Default branch unavailable; used remote default"},
		)
	}
	if result.ArchiveError != nil {
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleError, Text: "Archive download failed"},
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Archive unavailable; used git clone fallback"},
		)
	}
	if result.DiskFact != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: safeForkText(result.DiskFact, "Filesystem state recorded")})
	}
	blocks = append(blocks,
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Destination: " + safeForkDestinationDetail(result.Destination)},
		terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: "Done! Project created at " + safeForkDestinationDetail(result.Destination)},
	)
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func gitForkConsoleDescriptor(repository, destination string) terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / git fork",
		Target:  "Acquire project files",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "repository", Value: safeForkRepositoryInput(repository)},
			{Label: "destination", Value: safeForkDestinationDetail(destination)},
			{Label: "route", Value: "archive first; git clone fallback"},
		},
		FormCatalog: []terminalexperience.ConsoleFormStep{
			{ID: gitForkOverwriteFormID, Name: "Replace destination", Detail: "confirm recursive replacement"},
		},
	}
}

const (
	gitForkOverwriteFormID = "confirm-replace-destination"
	gitForkWorkCatalogID   = "git-fork-acquisition"
)

func gitForkWorkCatalog(requestCancel func()) terminalexperience.WorkCatalog {
	return terminalexperience.WorkCatalog{
		ID:            gitForkWorkCatalogID,
		Label:         "Acquire project files",
		Phases:        append([]terminalexperience.PhaseDefinition(nil), forkPhaseDefinitions...),
		RequestCancel: requestCancel,
	}
}

func safeForkRepositoryInput(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "repository"
	}
	_, rest := splitRef(value)
	if parsed, err := url.Parse(rest); err == nil && parsed.Host != "" {
		path := strings.Trim(strings.TrimSuffix(parsed.EscapedPath(), ".git"), "/")
		if path == "" {
			return safeForkText(parsed.Host, "repository")
		}
		return safeForkText(parsed.Host+"/"+path, "repository")
	}
	if index := strings.IndexByte(rest, ':'); index > 0 && !strings.Contains(rest[:index], "/") {
		return safeForkText(rest[:index]+":...", "repository")
	}
	return safeForkText(strings.TrimSuffix(rest, ".git"), "repository")
}

func safeForkRepositoryDetail(repository Repository) string {
	host := safeForkText(repository.Host, "provider")
	owner := safeForkText(repository.Owner, "owner")
	name := safeForkText(repository.Name, "repository")
	provider := safeForkText(repository.ProviderType, "provider")
	return host + "/" + owner + "/" + name + " (" + provider + ")"
}

func safeForkDestinationDetail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "project"
	}
	if filepath.IsAbs(value) {
		value = filepath.Base(filepath.Clean(value))
	}
	return safeForkText(filepath.ToSlash(value), "project")
}

func safeForkRefDetail(value string) string {
	return safeForkText(value, "default")
}

func safeForkText(value, fallback string) string {
	if !utf8.ValidString(value) {
		return fallback
	}
	var builder strings.Builder
	for _, character := range value {
		switch character {
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if unicode.IsControl(character) {
				return fallback
			}
			builder.WriteRune(character)
		}
	}
	value = strings.TrimSpace(builder.String())
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > 160 {
		return string(runes[:160]) + "..."
	}
	return value
}

func gitForkBlock(role terminalexperience.VisualRole, text string) terminalexperience.PresentationBlock {
	return terminalexperience.PresentationBlock{Role: role, Text: text}
}
