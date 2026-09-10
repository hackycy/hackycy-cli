package updater

import (
	"context"
	"errors"
)

// UpgradePhase identifies one user-meaningful parent upgrade boundary.
type UpgradePhase string

const (
	UpgradePhaseConsumeStartupTransaction UpgradePhase = "consume-startup-transaction"
	UpgradePhaseResolveRelease            UpgradePhase = "resolve-release"
	UpgradePhaseResolveArtifact           UpgradePhase = "resolve-artifact"
	UpgradePhaseDownloadCandidate         UpgradePhase = "download-candidate"
	UpgradePhaseVerifyCandidate           UpgradePhase = "verify-candidate"
	UpgradePhaseStageUpdater              UpgradePhase = "stage-updater"
	UpgradePhasePublishPending            UpgradePhase = "publish-pending"
	UpgradePhaseScheduleUpdater           UpgradePhase = "schedule-updater"
	UpgradePhaseComplete                  UpgradePhase = "complete"
)

// UpgradePhaseState is the terminal-facing state of a parent upgrade phase.
type UpgradePhaseState uint8

const (
	UpgradePhaseActive UpgradePhaseState = iota + 1
	UpgradePhaseCompleted
	UpgradePhaseCancelled
	UpgradePhaseFailed
)

// UpgradePhaseEvent contains only presentation-safe facts about parent work.
// It deliberately excludes URLs, hashes, paths, headers, and raw errors.
type UpgradePhaseEvent struct {
	Phase              UpgradePhase
	State              UpgradePhaseState
	Detail             string
	CurrentVersion     string
	CandidateVersion   string
	TargetOS           string
	TargetArchitecture string
	ArtifactName       string
	ChecksumSource     string
}

// UpgradeObserver is optional parent-command presentation instrumentation.
// Callbacks never influence release, filesystem, process, or cleanup behavior.
type UpgradeObserver struct {
	Phase         func(UpgradePhaseEvent)
	PreviousState func(UpdateTransaction)
}

func (observer UpgradeObserver) begin(phase UpgradePhase) {
	observer.report(UpgradePhaseEvent{Phase: phase, State: UpgradePhaseActive})
}

func (observer UpgradeObserver) complete(phase UpgradePhase, event UpgradePhaseEvent) {
	event.Phase = phase
	event.State = UpgradePhaseCompleted
	observer.report(event)
}

func (observer UpgradeObserver) end(ctx context.Context, phase UpgradePhase, err error, event UpgradePhaseEvent) {
	event.Phase = phase
	if isUpgradeCancellation(ctx, err) {
		event.State = UpgradePhaseCancelled
		if event.Detail == "" {
			event.Detail = "Cancelled"
		}
	} else {
		event.State = UpgradePhaseFailed
		if event.Detail == "" {
			event.Detail = upgradePhaseFailureDetail(phase)
		}
	}
	observer.report(event)
}

func (observer UpgradeObserver) report(event UpgradePhaseEvent) {
	if observer.Phase != nil {
		observer.Phase(event)
	}
}

func (observer UpgradeObserver) previous(state UpdateTransaction) {
	if observer.PreviousState != nil {
		observer.PreviousState(state)
	}
}

func isUpgradeCancellation(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (ctx != nil && ctx.Err() != nil)
}

func upgradePhaseFailureDetail(phase UpgradePhase) string {
	switch phase {
	case UpgradePhaseConsumeStartupTransaction:
		return "Startup transaction failed"
	case UpgradePhaseResolveRelease:
		return "Release resolution failed"
	case UpgradePhaseResolveArtifact:
		return "Artifact resolution failed"
	case UpgradePhaseDownloadCandidate:
		return "Candidate download failed"
	case UpgradePhaseVerifyCandidate:
		return "Candidate verification failed"
	case UpgradePhaseStageUpdater:
		return "Updater staging failed"
	case UpgradePhasePublishPending:
		return "Pending update publication failed"
	case UpgradePhaseScheduleUpdater:
		return "Updater scheduling failed"
	default:
		return "Update failed"
	}
}
