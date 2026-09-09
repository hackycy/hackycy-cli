package cm

import (
	"context"
	"errors"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

func phaseStateForCMError(ctx context.Context, err error) PhaseState {
	if err != nil && ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return PhaseCancelled
	}
	return PhaseFailed
}

const (
	cmInspectChangesPhaseID  = "inspect-changes"
	cmStageSelectedPhaseID   = "stage-selected-files"
	cmStageAllPhaseID        = "stage-all-changes"
	cmCaptureEvidencePhaseID = "capture-commit-evidence"
	cmResolveProfilePhaseID  = "resolve-provider-profile"
	cmGenerateMessagePhaseID = "generate-commit-message"
	cmVerifyScopePhaseID     = "verify-unchanged-scope"
	cmCreateCommitPhaseID    = "create-commit"
	cmPushCommitPhaseID      = "push-commit"
	cmWorkCatalogID          = "git-cm-workflow"

	cmInspectChangesPhaseName  = "Inspect changes"
	cmStageSelectedPhaseName   = "Stage selected files"
	cmStageAllPhaseName        = "Stage all changes"
	cmCaptureEvidencePhaseName = "Capture commit evidence"
	cmResolveProfilePhaseName  = "Resolve provider profile"
	cmGenerateMessagePhaseName = "Generate commit message"
	cmVerifyScopePhaseName     = "Verify unchanged scope"
	cmCreateCommitPhaseName    = "Create commit"
	cmPushCommitPhaseName      = "Push commit"
)

var cmPhaseDefinitions = []terminalexperience.PhaseDefinition{
	{ID: cmInspectChangesPhaseID, Name: cmInspectChangesPhaseName},
	{ID: cmStageSelectedPhaseID, Name: cmStageSelectedPhaseName},
	{ID: cmStageAllPhaseID, Name: cmStageAllPhaseName},
	{ID: cmCaptureEvidencePhaseID, Name: cmCaptureEvidencePhaseName},
	{ID: cmResolveProfilePhaseID, Name: cmResolveProfilePhaseName},
	{ID: cmGenerateMessagePhaseID, Name: cmGenerateMessagePhaseName},
	{ID: cmVerifyScopePhaseID, Name: cmVerifyScopePhaseName},
	{ID: cmCreateCommitPhaseID, Name: cmCreateCommitPhaseName},
	{ID: cmPushCommitPhaseID, Name: cmPushCommitPhaseName},
}

// Git CM pauses tracking while it presents its selection and confirmation
// forms. Each contiguous tracker segment therefore receives only its own
// ordered slice of the catalog; passing all phases to every segment would
// duplicate rows in the persistent Console table.
func cmPhaseDefinitionsForSegment(segment int) []terminalexperience.PhaseDefinition {
	var definitions []terminalexperience.PhaseDefinition
	switch segment {
	case 0:
		definitions = cmPhaseDefinitions[:6]
	case 1:
		definitions = cmPhaseDefinitions[6:]
	default:
		return nil
	}
	return append([]terminalexperience.PhaseDefinition(nil), definitions...)
}

// cmDetailedObserver is an optional terminal projection. The module keeps
// its typed Tracker contract for legacy callers while the command adapter can
// expose the finer Work Phase boundaries required by the B Console.
type cmDetailedObserver interface {
	reportCMPhase(string, PhaseState, string)
	reportCMMilestone(string)
}

func (module *Module) detailedObserver() cmDetailedObserver {
	observer, _ := module.tracker.(cmDetailedObserver)
	return observer
}

func reportCMPhase(observer cmDetailedObserver, id string, state PhaseState, detail string) {
	if observer != nil {
		observer.reportCMPhase(id, state, detail)
	}
}

func reportCMMilestone(observer cmDetailedObserver, text string) {
	if observer != nil && text != "" {
		observer.reportCMMilestone(text)
	}
}

// PhaseKind identifies one externally meaningful Git CM work phase.
type PhaseKind uint8

const (
	PhaseStage PhaseKind = iota
	PhaseCollect
	PhaseGenerate
	PhaseCommit
	PhasePush
)

// PhaseState records the command-owned state of one Git CM phase.
type PhaseState uint8

const (
	PhaseActive PhaseState = iota
	PhaseCompleted
	PhaseCancelled
	PhaseFailed
)

// Phase is one typed Git CM work update without terminal implementation details.
type Phase struct {
	Kind      PhaseKind
	State     PhaseState
	FileCount int
	Remote    string
	// StageAll distinguishes the bulk staging phase for detailed terminal
	// projections without changing the legacy PhaseKind contract.
	StageAll bool
}

// PhaseReporter consumes the updates for one contiguous Git CM work segment.
type PhaseReporter interface {
	Report(Phase)
	Close() error
}

// Tracker opens a command-owned Git CM work segment.
type Tracker interface {
	Start(context.Context) (PhaseReporter, error)
}

func (module *Module) track(ctx context.Context, work func(func(Phase)) (Result, error)) (result Result, err error) {
	reporter, err := module.tracker.Start(ctx)
	if err != nil {
		return Result{}, err
	}
	last := Phase{}
	reported := false
	report := func(phase Phase) {
		last = phase
		reported = true
		reporter.Report(phase)
	}
	defer func() {
		if reported && last.State == PhaseActive {
			if ctx.Err() != nil {
				last.State = PhaseCancelled
			} else if err != nil {
				last.State = PhaseFailed
			} else {
				last.State = PhaseCompleted
			}
			reporter.Report(last)
		}
		if closeErr := reporter.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return work(report)
}
