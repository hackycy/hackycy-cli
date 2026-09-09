package cm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

var errGitCMRequiresInteractive = errors.New("git cm requires an interactive terminal")

const (
	gitCMStageFormID  = "select-files-to-stage"
	gitCMCommitFormID = "confirm-commit"
)

func runCM(options *Options) error {
	_, err := executeCM(options)
	return err
}

func executeCM(options *Options) (Result, error) {
	if options == nil || options.Context == nil || options.Config == nil || options.HTTP == nil || options.Terminal == nil || options.Git == nil {
		return Result{}, errors.New("git cm options are incomplete")
	}
	ctx, cancel := context.WithCancel(options.Context)
	defer cancel()
	run, err := options.Terminal.OpenConsole(ctx, gitCMConsoleDescriptor(options.Input))
	if err != nil {
		return Result{}, err
	}
	defer run.Close()
	adapter := newTerminalGitCMAdapter(run, cancel)
	adapter.enableDetailed()
	if options.Terminal.Capabilities().Interaction == terminalexperience.RichInteractive {
		adapter.enableControlledWork()
	}

	store, err := options.Config()
	if err != nil {
		return Result{}, errors.Join(err, adapter.finish(terminalexperience.Failed, Result{}, nil))
	}
	module, err := New(Dependencies{
		Git:       gitRunnerAdapter{runner: options.Git},
		Files:     osCMSnapshotFileSystem{},
		Prompter:  adapter,
		Committer: adapter,
		Resolver:  store,
		Transport: options.HTTP,
		Tracker:   adapter,
	})
	if err != nil {
		return Result{}, errors.Join(err, adapter.finish(terminalexperience.Failed, Result{}, nil))
	}
	if options.Terminal.Capabilities().Interaction == terminalexperience.Automation {
		requiresInteraction, err := RequiresInteraction(options.Input)
		if err != nil {
			return Result{}, errors.Join(err, adapter.finish(terminalexperience.Failed, Result{}, nil))
		}
		if requiresInteraction {
			return Result{}, errors.Join(errGitCMRequiresInteractive, adapter.finish(terminalexperience.Failed, Result{}, nil))
		}
	}

	result, err := module.Run(ctx, options.Input)
	if finishErr := adapter.finishDetailed(); finishErr != nil {
		err = errors.Join(err, finishErr)
	}
	if err != nil {
		if result.Generated == nil && result.Profile != (ProfileDiagnostic{}) {
			_ = adapter.PresentFailure(result)
		}
		if result.Committed && !result.Pushed {
			_ = adapter.PresentOutcome(result)
		}
		return result, errors.Join(err, adapter.finish(gitCMFinishOutcome(ctx, result, err), result, adapter.finalDocument(result, true)))
	}
	if result.Generated != nil && !result.PromptedCommit {
		_ = adapter.PresentGenerated(result)
	}
	_ = adapter.PresentOutcome(result)
	outcome := terminalexperience.Succeeded
	if result.Cancelled || result.NothingSelected {
		outcome = terminalexperience.Cancelled
	}
	return result, adapter.finish(outcome, result, adapter.finalDocument(result, false))
}

func gitCMFinishOutcome(ctx context.Context, result Result, err error) terminalexperience.FinishOutcome {
	if err != nil {
		if gitCMCancellationOnly(ctx, err) {
			return terminalexperience.Cancelled
		}
		return terminalexperience.Failed
	}
	if result.Cancelled || result.NothingSelected {
		return terminalexperience.Cancelled
	}
	return terminalexperience.Succeeded
}

func gitCMCancellationOnly(ctx context.Context, err error) bool {
	if ctx == nil || err == nil || ctx.Err() == nil || !errors.Is(err, ctx.Err()) {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range many.Unwrap() {
			if cause != nil && !gitCMCancellationOnly(ctx, cause) {
				return false
			}
		}
		return true
	}
	if cause := errors.Unwrap(err); cause != nil {
		return gitCMCancellationOnly(ctx, cause)
	}
	return true
}

type terminalGitCMAdapter struct {
	run           terminalexperience.ExperienceRun
	requestCancel context.CancelFunc

	mu                    sync.Mutex
	detailed              bool
	controlled            bool
	activeUpdates         chan terminalexperience.OperationPhase
	activeDone            chan error
	active                bool
	segment               int
	pending               []terminalexperience.OperationPhase
	work                  terminalexperience.WorkSession
	workClosed            bool
	workErr               error
	lastPhaseID           string
	stagedChangesRetained bool
	milestones            []terminalexperience.PresentationDocument
	generated             *terminalexperience.PresentationDocument
	failure               *terminalexperience.PresentationDocument
	outcome               *terminalexperience.PresentationDocument
}

func newTerminalGitCMAdapter(run terminalexperience.ExperienceRun, requestCancel context.CancelFunc) *terminalGitCMAdapter {
	return &terminalGitCMAdapter{run: run, requestCancel: requestCancel}
}

func (adapter *terminalGitCMAdapter) enableDetailed() {
	adapter.mu.Lock()
	adapter.detailed = true
	adapter.mu.Unlock()
}

func (adapter *terminalGitCMAdapter) enableControlledWork() {
	adapter.mu.Lock()
	adapter.controlled = true
	adapter.mu.Unlock()
}

func (adapter *terminalGitCMAdapter) SelectFiles(prompt StagePrompt) ([]string, bool, error) {
	answer, cancelled, err := adapter.ask(gitCMStageRequest(prompt))
	if err != nil || cancelled {
		return nil, cancelled, err
	}
	return append([]string(nil), answer.Values...), false, nil
}

func (adapter *terminalGitCMAdapter) ConfirmCommit(prompt CommitPrompt) (bool, bool, error) {
	document := gitCMGeneratedDocument(prompt.Generated, prompt.Profile)
	adapter.mu.Lock()
	detailed := adapter.detailed
	adapter.mu.Unlock()
	if !detailed {
		if err := adapter.run.Notice(document); err != nil {
			return false, false, err
		}
	} else if err := adapter.run.Milestone(document); err != nil {
		return false, false, err
	}
	answer, cancelled, err := adapter.ask(gitCMCommitRequest(prompt))
	if err != nil || cancelled {
		return false, cancelled, err
	}
	return answer.Confirmed, false, nil
}

func (adapter *terminalGitCMAdapter) Start(_ context.Context) (PhaseReporter, error) {
	adapter.mu.Lock()
	if adapter.detailed {
		if adapter.controlled {
			adapter.mu.Unlock()
			return &terminalGitCMPhaseReporter{adapter: adapter, detailed: true, controlled: true}, nil
		}
		if adapter.active {
			adapter.mu.Unlock()
			return nil, errors.New("git cm tracker segment is already active")
		}
		definitions := cmPhaseDefinitionsForSegment(adapter.segment)
		if len(definitions) == 0 {
			adapter.mu.Unlock()
			return nil, errors.New("git cm tracker has no remaining phase segment")
		}
		adapter.segment++
		updates := make(chan terminalexperience.OperationPhase, 256)
		done := make(chan error, 1)
		adapter.activeUpdates = updates
		adapter.activeDone = done
		adapter.active = true
		pending := append([]terminalexperience.OperationPhase(nil), adapter.pending...)
		adapter.pending = nil
		adapter.mu.Unlock()
		go func() {
			err := adapter.run.Track(terminalexperience.TrackedOperation{
				ID:            "git-cm",
				OperationID:   "git-cm",
				Label:         "Git CM",
				Phases:        definitions,
				Updates:       updates,
				RequestCancel: adapter.requestCancel,
			})
			done <- err
		}()
		for _, update := range pending {
			updates <- update
		}
		return &terminalGitCMPhaseReporter{adapter: adapter, detailed: true, detailUpdates: updates, detailDone: done}, nil
	}
	adapter.mu.Unlock()
	reporter := &terminalGitCMPhaseReporter{
		updates:  make(chan terminalexperience.OperationPhase, 1),
		finished: make(chan struct{}),
	}
	go func() {
		err := adapter.run.Track(terminalexperience.TrackedOperation{
			Label:         "Git CM",
			Updates:       reporter.updates,
			RequestCancel: adapter.requestCancel,
		})
		reporter.complete(err)
	}()
	return reporter, nil
}

func (adapter *terminalGitCMAdapter) PresentGenerated(result Result) error {
	if result.Generated == nil {
		return nil
	}
	document := gitCMGeneratedDocument(*result.Generated, result.Profile)
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.generated = &document
		adapter.mu.Unlock()
		return nil
	}
	adapter.mu.Unlock()
	return adapter.run.Result(document)
}

func (adapter *terminalGitCMAdapter) PresentFailure(result Result) error {
	document := gitCMFailureDocument(result.Profile)
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.failure = &document
		adapter.mu.Unlock()
		return nil
	}
	adapter.mu.Unlock()
	return adapter.run.Result(document)
}

func (adapter *terminalGitCMAdapter) PresentOutcome(result Result) error {
	document := gitCMOutcomeDocument(result)
	if len(document.Blocks) == 0 {
		return nil
	}
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.outcome = &document
		adapter.mu.Unlock()
		return nil
	}
	adapter.mu.Unlock()
	return adapter.run.Result(document)
}

func (adapter *terminalGitCMAdapter) finish(outcome terminalexperience.FinishOutcome, result Result, document *terminalexperience.PresentationDocument) error {
	adapter.mu.Lock()
	location := adapter.lastPhaseID
	staged := adapter.stagedChangesRetained
	adapter.mu.Unlock()
	return adapter.run.Finish(terminalGitCMFinishRequestWithStaging(outcome, result, location, staged), document)
}

func (adapter *terminalGitCMAdapter) reportCMPhase(id string, state PhaseState, detail string) {
	update := terminalexperience.OperationPhase{ID: id, State: terminalGitCMPhaseState(state), Detail: safeCMText(detail, "phase")}
	adapter.mu.Lock()
	if !adapter.detailed {
		adapter.mu.Unlock()
		return
	}
	if gitCMFinishLocation(id) != "" {
		adapter.lastPhaseID = id
	}
	if state == PhaseCompleted && (id == cmStageSelectedPhaseID || id == cmStageAllPhaseID) {
		adapter.stagedChangesRetained = true
	}
	controlled := adapter.controlled
	adapter.mu.Unlock()
	if controlled {
		adapter.reportControlledCMPhase(update)
		return
	}
	adapter.mu.Lock()
	if !adapter.active {
		adapter.pending = append(adapter.pending, update)
		if state == PhaseActive {
			adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: cmLegacyPhaseLabel(id)}}})
		}
		adapter.mu.Unlock()
		return
	}
	if state == PhaseActive {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: cmLegacyPhaseLabel(id)}}})
	}
	updates := adapter.activeUpdates
	adapter.mu.Unlock()
	updates <- update
}

func (adapter *terminalGitCMAdapter) reportControlledCMPhase(update terminalexperience.OperationPhase) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if !adapter.detailed || !adapter.controlled || adapter.workClosed {
		return
	}
	adapter.lastPhaseID = update.ID
	if adapter.work == nil {
		work, err := terminalexperience.StartWork(adapter.run, terminalexperience.WorkCatalog{
			ID:            cmWorkCatalogID,
			Label:         "Git CM",
			Phases:        append([]terminalexperience.PhaseDefinition(nil), cmPhaseDefinitions...),
			RequestCancel: adapter.requestCancel,
		})
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

func cmLegacyPhaseLabel(id string) string {
	switch id {
	case cmInspectChangesPhaseID:
		return "Inspecting changes"
	case cmStageSelectedPhaseID:
		return "Staging selected files"
	case cmStageAllPhaseID:
		return "Staging all changes"
	case cmCaptureEvidencePhaseID:
		return "Collecting changes"
	case cmResolveProfilePhaseID:
		return "Resolving provider profile"
	case cmGenerateMessagePhaseID:
		return "Generating commit message"
	case cmVerifyScopePhaseID:
		return "Verifying unchanged scope"
	case cmCreateCommitPhaseID:
		return "Creating commit"
	case cmPushCommitPhaseID:
		return "Pushing commit"
	default:
		return "Git CM"
	}
}

func (adapter *terminalGitCMAdapter) reportCMMilestone(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: safeCMText(text, "CM update")}}})
	}
	adapter.mu.Unlock()
}

func (adapter *terminalGitCMAdapter) sendDetailed(update terminalexperience.OperationPhase) {
	adapter.mu.Lock()
	if !adapter.detailed || !adapter.active || adapter.activeUpdates == nil {
		adapter.mu.Unlock()
		return
	}
	updates := adapter.activeUpdates
	adapter.mu.Unlock()
	updates <- update
}

func (adapter *terminalGitCMAdapter) finishDetailed() error {
	adapter.mu.Lock()
	if !adapter.detailed {
		adapter.mu.Unlock()
		return nil
	}
	if adapter.active {
		adapter.mu.Unlock()
		return errors.New("git cm tracker segment was not closed")
	}
	if adapter.controlled {
		work := adapter.work
		if work != nil {
			adapter.workClosed = true
		}
		workErr := adapter.workErr
		milestones := append([]terminalexperience.PresentationDocument(nil), adapter.milestones...)
		adapter.milestones = nil
		adapter.mu.Unlock()
		if work != nil {
			workErr = errors.Join(workErr, work.Close())
		}
		var presentationErr error
		for _, milestone := range milestones {
			presentationErr = errors.Join(presentationErr, adapter.run.Milestone(milestone))
		}
		return errors.Join(workErr, presentationErr)
	}
	pending := append([]terminalexperience.OperationPhase(nil), adapter.pending...)
	adapter.pending = nil
	milestones := append([]terminalexperience.PresentationDocument(nil), adapter.milestones...)
	adapter.milestones = nil
	adapter.mu.Unlock()

	// Selection or an early failure can finish before the first compatibility
	// tracker segment starts. Replay those reached catalog entries so the
	// semantic Transcript still records the work that actually ran.
	var trackErr error
	if len(pending) > 0 {
		updates := make(chan terminalexperience.OperationPhase, len(pending))
		done := make(chan error, 1)
		go func() {
			done <- adapter.run.Track(terminalexperience.TrackedOperation{
				ID:          "git-cm-final",
				OperationID: "git-cm-final",
				Label:       "Git CM",
				Phases:      cmPhaseDefinitionsForSegment(0),
				Updates:     updates,
			})
		}()
		for _, update := range pending {
			updates <- update
		}
		close(updates)
		trackErr = <-done
	}
	var presentationErr error
	for _, milestone := range milestones {
		presentationErr = errors.Join(presentationErr, adapter.run.Milestone(milestone))
	}
	return errors.Join(trackErr, presentationErr)
}

func (adapter *terminalGitCMAdapter) finalDocument(result Result, failed bool) *terminalexperience.PresentationDocument {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if failed {
		if result.Committed && !result.Pushed && adapter.outcome != nil {
			document := *adapter.outcome
			return &document
		}
		if adapter.failure != nil {
			document := *adapter.failure
			return &document
		}
		return nil
	}
	if result.Generated != nil && !result.PromptedCommit && adapter.generated != nil {
		document := *adapter.generated
		return &document
	}
	if adapter.outcome != nil {
		document := *adapter.outcome
		return &document
	}
	document := gitCMOutcomeDocument(result)
	if len(document.Blocks) == 0 {
		return nil
	}
	return &document
}

func terminalGitCMFinishRequest(outcome terminalexperience.FinishOutcome, result Result, location string) terminalexperience.FinishRequest {
	return terminalGitCMFinishRequestWithStaging(outcome, result, location, false)
}

func terminalGitCMFinishRequestWithStaging(outcome terminalexperience.FinishOutcome, result Result, location string, staged bool) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome}
	if outcome != terminalexperience.Succeeded {
		request.Location = gitCMFinishLocation(location)
	}
	switch outcome {
	case terminalexperience.Succeeded:
		request.Summary = terminalGitCMSuccessSummary(result)
	case terminalexperience.Cancelled:
		request.Summary = terminalGitCMCancellationSummaryWithStaging(result, request.Location, staged)
	case terminalexperience.Failed:
		request.Summary = terminalGitCMFailureSummaryWithStaging(result, request.Location, staged)
	}
	return request
}

func terminalGitCMSuccessSummary(result Result) terminalexperience.PresentationDocument {
	text := "Git CM complete"
	role := terminalexperience.VisualRoleSuccess
	switch {
	case result.NoChanges && result.NoChangeScope == ScopeStaged:
		text = "No staged changes"
		role = terminalexperience.VisualRoleWarning
	case result.NoChanges:
		text = "No uncommitted changes"
		role = terminalexperience.VisualRoleWarning
	case result.NothingSelected:
		text = "Nothing selected"
		role = terminalexperience.VisualRoleWarning
	case result.Pushed:
		text = "Commit created and pushed"
	case result.Committed:
		text = "Commit created"
	case result.Generated != nil && !result.PromptedCommit:
		text = "Commit message generated"
	}
	return terminalGitCMSummaryDocument(text, role)
}

func terminalGitCMCancellationSummary(result Result, location string) terminalexperience.PresentationDocument {
	return terminalGitCMCancellationSummaryWithStaging(result, location, false)
}

func terminalGitCMCancellationSummaryWithStaging(result Result, location string, staged bool) terminalexperience.PresentationDocument {
	text := "Git CM operation cancelled"
	switch {
	case result.Committed && !result.Pushed:
		text = "Commit created locally; push not completed"
	case result.NothingSelected:
		text = "Nothing selected"
	case result.PromptedCommit:
		text = "Commit creation cancelled"
	case location == cmInspectChangesPhaseName:
		text = "File selection cancelled"
	case location == cmStageSelectedPhaseName || location == cmStageAllPhaseName:
		text = "Staging cancelled"
	case location == cmCaptureEvidencePhaseName || location == cmResolveProfilePhaseName || location == cmGenerateMessagePhaseName:
		text = "Commit message generation cancelled"
	}
	document := terminalGitCMSummaryDocument(text, terminalexperience.VisualRoleWarning)
	if staged && !result.Committed {
		document.Blocks = append(document.Blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Staged changes retained"})
	}
	return document
}

func terminalGitCMFailureSummary(result Result, location string) terminalexperience.PresentationDocument {
	return terminalGitCMFailureSummaryWithStaging(result, location, false)
}

func terminalGitCMFailureSummaryWithStaging(result Result, location string, staged bool) terminalexperience.PresentationDocument {
	text := "Git CM operation failed"
	switch location {
	case cmInspectChangesPhaseName:
		text = "Unable to inspect Git changes"
	case cmStageSelectedPhaseName, cmStageAllPhaseName:
		text = "Unable to stage changes"
	case cmCaptureEvidencePhaseName:
		text = "Unable to capture commit evidence"
	case cmResolveProfilePhaseName:
		text = "Unable to resolve provider profile"
	case cmGenerateMessagePhaseName:
		text = "Unable to confirm commit"
		if !result.PromptedCommit {
			text = "Unable to generate commit message"
		}
	case cmVerifyScopePhaseName:
		text = "Git scope changed; commit not created"
	case cmCreateCommitPhaseName:
		text = "Unable to create commit"
	case cmPushCommitPhaseName:
		text = "Unable to push commit"
		if result.Committed && !result.Pushed {
			text = "Commit created locally; push not completed"
		}
	}
	blocks := []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleError, Text: text}}
	if location == cmStageSelectedPhaseName || location == cmStageAllPhaseName {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Index may be partially updated"})
	}
	if !strings.Contains(text, "commit not created") && !strings.Contains(text, "Commit created locally") && (location == cmCaptureEvidencePhaseName || location == cmResolveProfilePhaseName || location == cmGenerateMessagePhaseName || location == cmCreateCommitPhaseName) {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Commit not created"})
	}
	if staged && !result.Committed {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Staged changes retained"})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalGitCMSummaryDocument(text string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func gitCMFinishLocation(location string) string {
	for _, definition := range cmPhaseDefinitions {
		if location == definition.ID || location == definition.Name {
			return definition.Name
		}
	}
	return ""
}

func (adapter *terminalGitCMAdapter) ask(request terminalexperience.InteractionRequest) (terminalexperience.InteractionAnswer, bool, error) {
	answer, err := adapter.run.Ask(request)
	if gitCMInteractionCancellationOnly(err) {
		return terminalexperience.InteractionAnswer{}, true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return terminalexperience.InteractionAnswer{}, false, errGitCMRequiresInteractive
	}
	if err != nil {
		return terminalexperience.InteractionAnswer{}, false, err
	}
	return answer, false, nil
}

func gitCMInteractionCancellationOnly(err error) bool {
	if err == nil || (!errors.Is(err, terminalexperience.ErrInteractionCancelled) && !errors.Is(err, context.Canceled)) {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range many.Unwrap() {
			if cause != nil && !gitCMInteractionCancellationOnly(cause) {
				return false
			}
		}
		return true
	}
	if cause := errors.Unwrap(err); cause != nil {
		return gitCMInteractionCancellationOnly(cause)
	}
	return true
}

func gitCMStageRequest(prompt StagePrompt) terminalexperience.InteractionRequest {
	options := make([]terminalexperience.InteractionOption, 0, len(prompt.Options))
	for _, option := range prompt.Options {
		options = append(options, terminalexperience.InteractionOption{Label: option.Label, Value: option.Value})
	}
	return terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionMultiSelect,
		Message:         prompt.Message,
		PlainLead:       prompt.Message,
		PlainPrompt:     "> ",
		ConsoleStepID:   gitCMStageFormID,
		TranscriptLabel: "Selected files",
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string {
			return gitCMSelectedFilesTranscript(answer.Values)
		},
		Options:      options,
		HasDefault:   true,
		Default:      terminalexperience.InteractionAnswer{Values: append([]string(nil), prompt.InitialValues...)},
		CancelValues: []string{"q", "quit", "cancel"},
		ParsePlain: func(value string) (terminalexperience.InteractionAnswer, error) {
			value = strings.TrimSpace(value)
			if strings.EqualFold(value, "all") {
				return terminalexperience.InteractionAnswer{Values: append([]string(nil), prompt.InitialValues...)}, nil
			}
			if strings.EqualFold(value, "none") {
				return terminalexperience.InteractionAnswer{Values: []string{}}, nil
			}
			selected, valid := selectGitCMOptions(value, prompt.Options)
			if !valid {
				return terminalexperience.InteractionAnswer{}, errors.New("Invalid selection")
			}
			return terminalexperience.InteractionAnswer{Values: selected}, nil
		},
	}
}

func gitCMCommitRequest(prompt CommitPrompt) terminalexperience.InteractionRequest {
	return terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionConfirm,
		Message:         prompt.Message,
		ConsoleStepID:   gitCMCommitFormID,
		TranscriptLabel: "Commit confirmation",
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
	}
}

func selectGitCMOptions(value string, options []StageOption) ([]string, bool) {
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character == ',' || character == ' ' || character == '\t'
	})
	if len(parts) == 0 {
		return nil, false
	}
	selected := make([]string, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, part := range parts {
		index, err := strconv.Atoi(part)
		if err != nil || index < 1 || index > len(options) || seen[index] {
			return nil, false
		}
		seen[index] = true
		selected = append(selected, options[index-1].Value)
	}
	return selected, true
}

type terminalGitCMPhaseReporter struct {
	adapter       *terminalGitCMAdapter
	detailed      bool
	controlled    bool
	detailUpdates chan terminalexperience.OperationPhase
	detailDone    chan error
	detailClosed  bool
	updates       chan terminalexperience.OperationPhase
	finished      chan struct{}

	mu     sync.Mutex
	closed bool
	err    error
}

func (reporter *terminalGitCMPhaseReporter) Report(phase Phase) {
	if reporter.detailed {
		if reporter.controlled {
			return
		}
		// Detailed Work Phase boundaries are emitted by the optional observer
		// hooks in the module. Suppressing the compatibility Phase stream here
		// avoids duplicate/overlapping transitions in the immutable catalog.
		return
	}
	update := terminalGitCMPhase(phase)
	select {
	case reporter.updates <- update:
	case <-reporter.finished:
	}
}

func (reporter *terminalGitCMPhaseReporter) Close() error {
	if reporter.detailed {
		if reporter.controlled {
			return nil
		}
		if reporter.detailClosed {
			return nil
		}
		reporter.detailClosed = true
		close(reporter.detailUpdates)
		err := <-reporter.detailDone
		reporter.adapter.mu.Lock()
		if reporter.adapter.activeUpdates == reporter.detailUpdates {
			reporter.adapter.active = false
			reporter.adapter.activeUpdates = nil
			reporter.adapter.activeDone = nil
		}
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

func (reporter *terminalGitCMPhaseReporter) complete(err error) {
	reporter.mu.Lock()
	reporter.err = err
	reporter.mu.Unlock()
	close(reporter.finished)
}

func terminalGitCMPhase(phase Phase) terminalexperience.OperationPhase {
	name := "Staging selected files"
	detail := gitCMPhaseFileDetail(phase.FileCount)
	switch phase.Kind {
	case PhaseCollect:
		name = "Collecting changes"
	case PhaseGenerate:
		name = "Generating commit message"
	case PhaseCommit:
		name = "Creating commit"
		detail = ""
	case PhasePush:
		name = "Pushing commit"
		detail = phase.Remote
	}
	return terminalexperience.OperationPhase{Name: name, Detail: detail, State: terminalGitCMPhaseState(phase.State)}
}

func gitCMPhaseFileDetail(count int) string {
	if count == 0 {
		return ""
	}
	if count == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", count)
}

func terminalGitCMPhaseState(state PhaseState) terminalexperience.PhaseState {
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

func gitCMGeneratedDocument(generated GeneratedMessage, profile ProfileDiagnostic) terminalexperience.PresentationDocument {
	coverage := generated.Evidence
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleSuccess, Text: generated.Message + "\n\n"},
		{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Profile: %s (%s)", profile.Name, profile.Model)},
		{Role: terminalexperience.VisualRoleMuted, Text: formatGitCMTokenUsage(generated.Usage)},
		{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Local evidence estimate: ~%s serialized prompt tokens / %d of %d clusters / %d of %d facts", formatGitCMCount(float64(coverage.EstimatedLocalPromptTokens)), coverage.RepresentedClusters, coverage.TotalClusters, coverage.IncludedFacts, coverage.IncludedFacts+coverage.OmittedFacts)},
	}
	if coverage.ContentCompacted {
		suffix := "s"
		if coverage.TotalClusters == 1 {
			suffix = ""
		}
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Commit scope: %d cluster%s represented with compacted semantic evidence. This does not affect which files are committed.", coverage.TotalClusters, suffix)})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func gitCMGeneratedText(generated GeneratedMessage, profile ProfileDiagnostic) string {
	return terminalexperience.RenderPlain(gitCMGeneratedDocument(generated, profile))
}

func gitCMFailureDocument(profile ProfileDiagnostic) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleMuted,
		Text: fmt.Sprintf("Provider: %s\nBase URL: %s\nModel: %s", safeCMText(profile.Name, "provider"), safeCMProfileURL(profile.BaseURL), safeCMText(profile.Model, "model")),
	}}}
}

func safeCMProfileURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<unavailable>"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "<configured>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return safeCMText(strings.TrimSuffix(parsed.String(), "/"), "<configured>")
}

func gitCMOutcomeDocument(result Result) terminalexperience.PresentationDocument {
	text := ""
	role := terminalexperience.VisualRolePlain
	switch {
	case result.NoChanges && result.NoChangeScope == ScopeStaged:
		text = "No staged changes."
		role = terminalexperience.VisualRoleWarning
	case result.NoChanges:
		text = "No uncommitted changes."
		role = terminalexperience.VisualRoleWarning
	case result.NothingSelected:
		text = "Nothing selected."
		role = terminalexperience.VisualRoleWarning
	case result.Cancelled:
		text = "Cancelled"
		role = terminalexperience.VisualRoleWarning
	case result.Pushed:
		text = "Commit created and pushed"
		role = terminalexperience.VisualRoleSuccess
	case result.Committed:
		text = "Commit created"
		role = terminalexperience.VisualRoleSuccess
	}
	if text == "" {
		return terminalexperience.PresentationDocument{}
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func gitCMConsoleDescriptor(input Input) terminalexperience.ConsoleDescriptor {
	mode := "generate"
	if input.Stage || input.StagePush != nil {
		mode = "stage and commit"
	} else if input.StageAll {
		mode = "stage all and commit"
	} else if input.Staged {
		mode = "staged and commit"
	} else if input.Push != nil {
		mode = "commit and push"
	}
	if input.DryRun {
		mode = "generate only"
	}
	language := input.Language
	if language == "" {
		language = "en"
	}
	remote := ""
	if value, ok := firstTruthyOptional(input.StagePush, input.Push); ok {
		remote = safeCMRemote(value)
	}
	metadata := []terminalexperience.ConsoleMetadata{
		{Label: "mode", Value: safeCMText(mode, "generate")},
		{Label: "language", Value: safeCMText(language, "en")},
	}
	if remote != "" {
		metadata = append(metadata, terminalexperience.ConsoleMetadata{Label: "remote", Value: remote})
	}
	if strings.TrimSpace(input.Profile) != "" {
		metadata = append(metadata, terminalexperience.ConsoleMetadata{Label: "profile", Value: safeCMText(input.Profile, "configured")})
	}
	return terminalexperience.ConsoleDescriptor{
		Command:     "YCY / git cm",
		Target:      "Generate and optionally create a commit",
		Status:      "READY",
		Metadata:    metadata,
		FormCatalog: gitCMFormCatalog(input),
	}
}

func gitCMFormCatalog(input Input) []terminalexperience.ConsoleFormStep {
	mode, err := resolveExecutionMode(input)
	if err != nil {
		return nil
	}
	catalog := make([]terminalexperience.ConsoleFormStep, 0, 2)
	if mode.PromptStage {
		catalog = append(catalog, terminalexperience.ConsoleFormStep{
			ID:     gitCMStageFormID,
			Name:   "Select files to stage",
			Detail: "all changes selected by default",
		})
	}
	if mode.CreateCommit {
		catalog = append(catalog, terminalexperience.ConsoleFormStep{
			ID:     gitCMCommitFormID,
			Name:   "Commit confirmation",
			Detail: "default Yes",
		})
	}
	if len(catalog) == 0 {
		return nil
	}
	return catalog
}

func gitCMSelectedFilesTranscript(values []string) string {
	count := len(values)
	if count == 0 {
		return "no files selected"
	}
	if count == 1 {
		return "1 file selected"
	}
	return fmt.Sprintf("%d files selected", count)
}

func safeCMRemote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "origin"
	}
	return safeCMText(value, "remote")
}

func safeCMText(value, fallback string) string {
	if !utf8.ValidString(value) {
		return fallback
	}
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if unicode.IsControl(r) {
				return fallback
			}
			builder.WriteRune(r)
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

func formatGitCMTokenUsage(usage *TokenUsage) string {
	if usage == nil {
		return "Provider tokens: unavailable"
	}
	return "Provider tokens: " + formatGitCMTokenValue(usage.PromptTokens) + " prompt / " + formatGitCMTokenValue(usage.CompletionTokens) + " completion / " + formatGitCMTokenValue(usage.TotalTokens) + " total"
}

func formatGitCMTokenValue(value *float64) string {
	if value == nil {
		return "unknown"
	}
	return formatGitCMCount(*value)
}

func formatGitCMCount(value float64) string {
	formatted := strconv.FormatFloat(value, 'f', -1, 64)
	decimal := strings.IndexByte(formatted, '.')
	integer := formatted
	fraction := ""
	if decimal >= 0 {
		integer = formatted[:decimal]
		fraction = formatted[decimal:]
	}
	start := 0
	if strings.HasPrefix(integer, "-") {
		start = 1
	}
	for index := len(integer) - 3; index > start; index -= 3 {
		integer = integer[:index] + "," + integer[index:]
	}
	return integer + fraction
}
