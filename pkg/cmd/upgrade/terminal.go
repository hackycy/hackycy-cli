package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/hackycy/hackycy-cli/internal/logging"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/updater"
)

type upgradePhaseSink struct {
	run          terminalexperience.ExperienceRun
	capabilities terminalexperience.Capabilities
	cancel       context.CancelFunc

	mu              sync.Mutex
	work            terminalexperience.WorkSession
	workStarted     bool
	workClosed      bool
	workErr         error
	previousResult  *terminalexperience.PresentationDocument
	presentationErr error
}

func newUpgradePhaseSink(run terminalexperience.ExperienceRun, capabilities terminalexperience.Capabilities, cancel context.CancelFunc) *upgradePhaseSink {
	return &upgradePhaseSink{run: run, capabilities: capabilities, cancel: cancel}
}

func (sink *upgradePhaseSink) observer() updater.UpgradeObserver {
	return updater.UpgradeObserver{
		Phase:         sink.phase,
		PreviousState: sink.previousState,
	}
}

func (sink *upgradePhaseSink) phase(event updater.UpgradePhaseEvent) {
	sink.mu.Lock()
	if sink.presentationErr != nil {
		sink.mu.Unlock()
		return
	}
	work, err := sink.ensureWorkLocked()
	sink.mu.Unlock()
	if err != nil {
		sink.record(err)
		return
	}
	err = work.Update(terminalexperience.OperationPhase{
		ID:     string(event.Phase),
		Detail: terminalUpgradePhaseDetail(event),
		State:  terminalUpgradePhaseState(event.State),
	})
	sink.record(err)
}

func (sink *upgradePhaseSink) ensureWorkLocked() (terminalexperience.WorkSession, error) {
	if sink.workStarted {
		return sink.work, sink.workErr
	}
	sink.workStarted = true
	work, err := terminalexperience.StartWork(sink.run, terminalUpgradeWorkCatalog(sink.cancel))
	sink.work = work
	sink.workErr = err
	return work, err
}

func (sink *upgradePhaseSink) previousState(state updater.UpdateTransaction) {
	sink.mu.Lock()
	if sink.presentationErr != nil {
		sink.mu.Unlock()
		return
	}
	document := terminalUpgradeDocument(StateMessage(state), terminalUpgradeStateRole(state))
	sink.previousResult = &document
	rich := sink.capabilities.Interaction == terminalexperience.RichInteractive
	sink.mu.Unlock()
	if rich {
		sink.record(sink.run.Milestone(document))
	}
}

func (sink *upgradePhaseSink) close() error {
	sink.mu.Lock()
	if sink.workClosed {
		err := sink.workErr
		sink.mu.Unlock()
		return err
	}
	sink.workClosed = true
	work := sink.work
	err := sink.workErr
	sink.mu.Unlock()
	if work != nil {
		err = errors.Join(err, work.Close())
	}
	sink.mu.Lock()
	sink.workErr = err
	sink.mu.Unlock()
	return err
}

func (sink *upgradePhaseSink) previousDocument() *terminalexperience.PresentationDocument {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.previousResult == nil {
		return nil
	}
	document := *sink.previousResult
	document.Blocks = append([]terminalexperience.PresentationBlock(nil), document.Blocks...)
	return &document
}

func (sink *upgradePhaseSink) err() error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.presentationErr
}

func (sink *upgradePhaseSink) record(err error) {
	if err == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.presentationErr = errors.Join(sink.presentationErr, err)
	if sink.cancel != nil {
		sink.cancel()
	}
}

const upgradeWorkCatalogID = "upgrade-release"

var terminalUpgradePhaseDefinitions = []terminalexperience.PhaseDefinition{
	{ID: string(updater.UpgradePhaseConsumeStartupTransaction), Name: "Consume startup transaction"},
	{ID: string(updater.UpgradePhaseResolveRelease), Name: "Resolve release"},
	{ID: string(updater.UpgradePhaseResolveArtifact), Name: "Resolve artifact"},
	{ID: string(updater.UpgradePhaseDownloadCandidate), Name: "Download candidate"},
	{ID: string(updater.UpgradePhaseVerifyCandidate), Name: "Verify candidate"},
	{ID: string(updater.UpgradePhaseStageUpdater), Name: "Stage updater"},
	{ID: string(updater.UpgradePhasePublishPending), Name: "Publish pending update"},
	{ID: string(updater.UpgradePhaseScheduleUpdater), Name: "Schedule updater"},
	{ID: string(updater.UpgradePhaseComplete), Name: "Complete"},
}

func terminalUpgradeWorkCatalog(cancel context.CancelFunc) terminalexperience.WorkCatalog {
	return terminalexperience.WorkCatalog{
		ID:            upgradeWorkCatalogID,
		Label:         "Upgrade ycy",
		Phases:        append([]terminalexperience.PhaseDefinition(nil), terminalUpgradePhaseDefinitions...),
		RequestCancel: cancel,
	}
}

func finishUpgradeRun(run terminalexperience.ExperienceRun, diagnostics io.Writer, previous *terminalexperience.PresentationDocument, result updater.UpgradeResult, resultErr error) error {
	if resultErr != nil {
		// Cancellation is a process lifecycle outcome, even when the lower-level
		// updater preserves its historical ExitCodeError wrapper.
		if errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
			return errors.Join(resultErr, run.Finish(terminalUpgradeFinishRequest(terminalexperience.Cancelled, "Upgrade cancelled.", terminalexperience.VisualRoleWarning), previous))
		}
		var exit *updater.ExitCodeError
		if result.Aborted && errors.As(resultErr, &exit) {
			_, _ = fmt.Fprintln(diagnostics, "error: "+terminalUpgradeDiagnostic(resultErr))
			document := terminalUpgradeDocument("Update aborted.", terminalexperience.VisualRoleWarning)
			return errors.Join(resultErr, run.Finish(terminalUpgradeFinishRequest(terminalexperience.Failed, "Update aborted.", terminalexperience.VisualRoleWarning), terminalUpgradeCombinedDocument(previous, &document)))
		}
		outcome := terminalexperience.Failed
		if errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
			outcome = terminalexperience.Cancelled
		}
		summary := "Unable to complete upgrade."
		role := terminalexperience.VisualRoleError
		if outcome == terminalexperience.Cancelled {
			summary = "Upgrade cancelled."
			role = terminalexperience.VisualRoleWarning
		}
		return errors.Join(resultErr, run.Finish(terminalUpgradeFinishRequest(outcome, summary, role), previous))
	}
	if result.AlreadyCurrent {
		document := terminalUpgradeDocument(fmt.Sprintf("Current version v%s is the latest.\nNo update needed.", result.CurrentVersion), terminalexperience.VisualRoleSuccess)
		return run.Finish(terminalUpgradeFinishRequest(terminalexperience.Succeeded, fmt.Sprintf("Current version v%s is the latest. No update needed.", terminalUpgradeSafeDetail(result.CurrentVersion)), terminalexperience.VisualRoleSuccess), terminalUpgradeCombinedDocument(previous, &document))
	}
	if result.Scheduled {
		document := terminalUpgradeDocument(fmt.Sprintf("Update to v%s has been scheduled and will finish after ycy exits.", result.ScheduledVersion), terminalexperience.VisualRoleSuccess)
		return run.Finish(terminalUpgradeFinishRequest(terminalexperience.Succeeded, fmt.Sprintf("Update to v%s has been scheduled and will finish after ycy exits.", terminalUpgradeSafeDetail(result.ScheduledVersion)), terminalexperience.VisualRoleSuccess), terminalUpgradeCombinedDocument(previous, &document))
	}
	return run.Finish(terminalUpgradeFinishRequest(terminalexperience.Succeeded, "Upgrade complete.", terminalexperience.VisualRoleSuccess), previous)
}

func terminalUpgradeFinishRequest(outcome terminalexperience.FinishOutcome, summary string, role terminalexperience.VisualRole) terminalexperience.FinishRequest {
	return terminalexperience.FinishRequest{
		Outcome: outcome,
		Summary: terminalUpgradeDocument(summary, role),
	}
}

func terminalUpgradeCombinedDocument(first, second *terminalexperience.PresentationDocument) *terminalexperience.PresentationDocument {
	if first == nil && second == nil {
		return nil
	}
	document := terminalexperience.PresentationDocument{}
	if first != nil {
		document.Blocks = append(document.Blocks, first.Blocks...)
	}
	if second != nil {
		document.Blocks = append(document.Blocks, second.Blocks...)
	}
	return &document
}

func terminalUpgradeIntroDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / upgrade"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Upgrade ycy"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Resolve and verify the latest release before scheduling the updater"},
	}}
}

func terminalUpgradePhaseName(phase updater.UpgradePhase) (string, bool) {
	switch phase {
	case updater.UpgradePhaseConsumeStartupTransaction:
		return "Consume startup transaction", true
	case updater.UpgradePhaseResolveRelease:
		return "Resolve release", true
	case updater.UpgradePhaseResolveArtifact:
		return "Resolve artifact", true
	case updater.UpgradePhaseDownloadCandidate:
		return "Download candidate", true
	case updater.UpgradePhaseVerifyCandidate:
		return "Verify candidate", true
	case updater.UpgradePhaseStageUpdater:
		return "Stage updater", true
	case updater.UpgradePhasePublishPending:
		return "Publish pending update", true
	case updater.UpgradePhaseScheduleUpdater:
		return "Schedule updater", true
	case updater.UpgradePhaseComplete:
		return "Complete", true
	default:
		return "", false
	}
}

func terminalUpgradePhaseState(state updater.UpgradePhaseState) terminalexperience.PhaseState {
	switch state {
	case updater.UpgradePhaseCompleted:
		return terminalexperience.PhaseCompleted
	case updater.UpgradePhaseCancelled:
		return terminalexperience.PhaseCancelled
	case updater.UpgradePhaseFailed:
		return terminalexperience.PhaseFailed
	default:
		return terminalexperience.PhaseActive
	}
}

func terminalUpgradePhaseDetail(event updater.UpgradePhaseEvent) string {
	parts := []string{event.Detail}
	switch event.Phase {
	case updater.UpgradePhaseResolveRelease:
		if event.CurrentVersion != "" && event.CandidateVersion != "" {
			parts = append(parts, "Current v"+event.CurrentVersion+"; latest v"+event.CandidateVersion)
		}
		if event.TargetOS != "" && event.TargetArchitecture != "" {
			parts = append(parts, event.TargetOS+"/"+event.TargetArchitecture)
		}
	case updater.UpgradePhaseResolveArtifact:
		if event.ArtifactName != "" {
			parts = append(parts, event.ArtifactName)
		}
		if event.ChecksumSource != "" {
			parts = append(parts, "checksum: "+event.ChecksumSource)
		}
	case updater.UpgradePhaseComplete:
		if event.CandidateVersion != "" {
			parts = append(parts, "Target v"+event.CandidateVersion)
		}
	}
	return terminalUpgradeSafeDetail(strings.Join(nonEmptyUpgradeFields(parts), " | "))
}

func nonEmptyUpgradeFields(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func terminalUpgradeSafeDetail(value string) string {
	value = terminalexperience.RenderPlain(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Text: value}}})
	value = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(value)
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= 240 {
		return value
	}
	return string([]rune(value)[:237]) + "..."
}

func terminalUpgradeDiagnostic(err error) string {
	return terminalUpgradeSafeDetail(logging.Redact(err.Error()))
}
