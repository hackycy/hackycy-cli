package cm

import (
	"context"
	"errors"
	"fmt"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

// ProfileResolver is the shared encrypted-configuration boundary used by Git CM.
type ProfileResolver interface {
	ResolveCMProfile(appconfig.CMResolveOptions) (appconfig.ResolvedCMProfile, error)
}

// CommitPrompt contains the generated message and safe diagnostics shown before committing.
type CommitPrompt struct {
	Message   string
	Generated GeneratedMessage
	Profile   ProfileDiagnostic
}

// CommitPrompter owns the default-yes commit confirmation boundary.
type CommitPrompter interface {
	ConfirmCommit(CommitPrompt) (confirmed bool, cancelled bool, err error)
}

// Dependencies are Git CM's command-owned collaboration points.
type Dependencies struct {
	Git       GitInputRunner
	Files     SnapshotFileSystem
	Prompter  StagePrompter
	Committer CommitPrompter
	Resolver  ProfileResolver
	Transport ProviderTransport
	Tracker   Tracker
}

// Module owns Git CM generation before confirmation, commit, and push handling.
type Module struct {
	git       GitInputRunner
	files     SnapshotFileSystem
	prompter  StagePrompter
	committer CommitPrompter
	resolver  ProfileResolver
	transport ProviderTransport
	tracker   Tracker
}

// New constructs an unregistered Git CM module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.Git == nil {
		return nil, errors.New("Git CM runner is required")
	}
	if dependencies.Files == nil {
		return nil, errors.New("Git CM filesystem is required")
	}
	if dependencies.Prompter == nil {
		return nil, errors.New("Git CM staging prompt is required")
	}
	if dependencies.Committer == nil {
		return nil, errors.New("Git CM commit prompt is required")
	}
	if dependencies.Resolver == nil {
		return nil, errors.New("Git CM profile resolver is required")
	}
	if dependencies.Transport == nil {
		return nil, errors.New("Git CM provider transport is required")
	}
	if dependencies.Tracker == nil {
		return nil, errors.New("Git CM tracker is required")
	}
	return &Module{
		git:       dependencies.Git,
		files:     dependencies.Files,
		prompter:  dependencies.Prompter,
		committer: dependencies.Committer,
		resolver:  dependencies.Resolver,
		transport: dependencies.Transport,
		tracker:   dependencies.Tracker,
	}, nil
}

// Run applies optional staging and generates one validated commit message.
func (module *Module) Run(ctx context.Context, input Input) (Result, error) {
	mode, err := resolveExecutionMode(input)
	if err != nil {
		return Result{}, err
	}
	observer := module.detailedObserver()
	result := Result{Scope: mode.Scope}
	if mode.PromptStage {
		reportCMPhase(observer, cmInspectChangesPhaseID, PhaseActive, "Reading repository status")
		plan, err := prepareStage(ctx, module.git)
		if err != nil {
			reportCMPhase(observer, cmInspectChangesPhaseID, phaseStateForCMError(ctx, err), "Repository inspection failed")
			return Result{}, err
		}
		reportCMPhase(observer, cmInspectChangesPhaseID, PhaseCompleted, fmt.Sprintf("%d changed files available", len(plan.state.Files)))
		result.RepositoryRoot = plan.state.Root
		if len(plan.state.Files) == 0 {
			result.NoChanges = true
			result.NoChangeScope = ScopeAllUncommitted
			return result, nil
		}
		selected, cancelled, err := module.prompter.SelectFiles(plan.prompt)
		if err != nil {
			return result, err
		}
		if cancelled {
			result.Cancelled = true
			reportCMMilestone(observer, "File selection cancelled")
			return result, nil
		}
		if len(selected) == 0 {
			result.NothingSelected = true
			return result, nil
		}
		result, err = module.track(ctx, func(report func(Phase)) (Result, error) {
			report(Phase{Kind: PhaseStage, State: PhaseActive, FileCount: len(selected)})
			reportCMPhase(observer, cmStageSelectedPhaseID, PhaseActive, fmt.Sprintf("%d files selected", len(selected)))
			if _, err := applyStagePlan(ctx, module.git, module.files, plan, selected); err != nil {
				reportCMPhase(observer, cmStageSelectedPhaseID, phaseStateForCMError(ctx, err), "Index update failed")
				return Result{}, err
			}
			report(Phase{Kind: PhaseStage, State: PhaseCompleted, FileCount: len(selected)})
			reportCMPhase(observer, cmStageSelectedPhaseID, PhaseCompleted, fmt.Sprintf("%d files staged", len(selected)))
			return module.generate(ctx, input, mode, result, report)
		})
		if err != nil {
			return result, err
		}
	} else {
		result, err = module.track(ctx, func(report func(Phase)) (Result, error) {
			if mode.StageAll {
				report(Phase{Kind: PhaseStage, State: PhaseActive})
				reportCMPhase(observer, cmInspectChangesPhaseID, PhaseActive, "Reading repository status")
				// The catalog permits only one active Work Phase. Finish the
				// inspection boundary before entering the stage-all mutation.
				reportCMPhase(observer, cmInspectChangesPhaseID, PhaseCompleted, "Repository status read")
				reportCMPhase(observer, cmStageAllPhaseID, PhaseActive, "Running git add -A")
				root, err := StageAllChanges(ctx, module.git)
				if err != nil {
					reportCMPhase(observer, cmStageAllPhaseID, phaseStateForCMError(ctx, err), "Index update failed")
					return Result{}, err
				}
				result.RepositoryRoot = root
				report(Phase{Kind: PhaseStage, State: PhaseCompleted})
				reportCMPhase(observer, cmStageAllPhaseID, PhaseCompleted, "All changes staged")
			}
			return module.generate(ctx, input, mode, result, report)
		})
		if err != nil {
			return result, err
		}
	}
	if result.Generated == nil || !mode.CreateCommit {
		return result, nil
	}
	generated := *result.Generated
	result.PromptedCommit = true
	confirmed, cancelled, err := module.committer.ConfirmCommit(CommitPrompt{
		Message:   "Create this commit?",
		Generated: generated,
		Profile:   result.Profile,
	})
	if err != nil {
		return result, err
	}
	if cancelled || !confirmed {
		result.Cancelled = true
		if cancelled {
			reportCMMilestone(observer, "Commit creation cancelled")
		} else {
			reportCMMilestone(observer, "Commit creation declined")
		}
		return result, nil
	}
	return module.track(ctx, func(report func(Phase)) (Result, error) {
		reportCMPhase(observer, cmVerifyScopePhaseID, PhaseActive, "Checking unchanged commit scope")
		request := CommitRequest{
			RepositoryRoot: result.RepositoryRoot,
			Scope:          result.Scope,
			SnapshotID:     generated.SnapshotID,
			Message:        generated.Message,
		}
		if err := AssertSnapshotCurrent(ctx, module.git, module.files, request.Scope, request.SnapshotID); err != nil {
			reportCMPhase(observer, cmVerifyScopePhaseID, phaseStateForCMError(ctx, err), "Commit scope changed or could not be checked")
			return result, err
		}
		reportCMPhase(observer, cmVerifyScopePhaseID, PhaseCompleted, "Commit scope unchanged")
		report(Phase{Kind: PhaseCommit, State: PhaseActive})
		reportCMPhase(observer, cmCreateCommitPhaseID, PhaseActive, "Creating commit")
		if err := commitSnapshotMutation(ctx, module.git, request); err != nil {
			reportCMPhase(observer, cmCreateCommitPhaseID, phaseStateForCMError(ctx, err), "Commit creation failed")
			return result, err
		}
		result.Committed = true
		report(Phase{Kind: PhaseCommit, State: PhaseCompleted})
		reportCMPhase(observer, cmCreateCommitPhaseID, PhaseCompleted, "Commit created")
		if !mode.Push {
			return result, nil
		}
		report(Phase{Kind: PhasePush, State: PhaseActive, Remote: mode.PushRemote})
		reportCMPhase(observer, cmPushCommitPhaseID, PhaseActive, "Remote: "+safeCMRemote(mode.PushRemote))
		if err := PushCommit(ctx, module.git, result.RepositoryRoot, mode.PushRemote); err != nil {
			reportCMPhase(observer, cmPushCommitPhaseID, phaseStateForCMError(ctx, err), "Push failed")
			return result, err
		}
		result.Pushed = true
		result.PushRemote = mode.PushRemote
		report(Phase{Kind: PhasePush, State: PhaseCompleted, Remote: mode.PushRemote})
		reportCMPhase(observer, cmPushCommitPhaseID, PhaseCompleted, "Commit pushed")
		return result, nil
	})
}

func (module *Module) generate(ctx context.Context, input Input, mode executionMode, result Result, report func(Phase)) (Result, error) {
	report(Phase{Kind: PhaseCollect, State: PhaseActive})
	observer := module.detailedObserver()
	reportCMPhase(observer, cmCaptureEvidencePhaseID, PhaseActive, "Capturing Git scope and evidence")
	snapshot, err := CaptureSnapshot(ctx, module.git, module.files, mode.Scope)
	if err != nil {
		reportCMPhase(observer, cmCaptureEvidencePhaseID, phaseStateForCMError(ctx, err), "Git evidence capture failed")
		return Result{}, err
	}
	reportCMPhase(observer, cmCaptureEvidencePhaseID, PhaseCompleted, fmt.Sprintf("%d changed files captured", len(snapshot.Files)))
	result.RepositoryRoot = snapshot.RepositoryRoot
	if len(snapshot.Files) == 0 {
		report(Phase{Kind: PhaseCollect, State: PhaseCompleted})
		result.NoChanges = true
		result.NoChangeScope = mode.Scope
		return result, nil
	}
	report(Phase{Kind: PhaseCollect, State: PhaseCompleted, FileCount: len(snapshot.Files)})
	reportCMPhase(observer, cmResolveProfilePhaseID, PhaseActive, "Resolving provider profile")
	profile, err := module.resolver.ResolveCMProfile(appconfig.CMResolveOptions{
		ProfileName:       input.Profile,
		TimeoutOverrideMS: input.TimeoutMS,
	})
	if err != nil {
		reportCMPhase(observer, cmResolveProfilePhaseID, phaseStateForCMError(ctx, err), "Provider profile unavailable")
		return Result{}, err
	}
	reportCMPhase(observer, cmResolveProfilePhaseID, PhaseCompleted, "Provider profile resolved")
	language, err := normalizeLanguage(input.Language)
	if err != nil {
		return Result{}, err
	}
	result.Profile = profileDiagnostic(profile)
	model, err := NewOpenAICompatibleModel(profile, module.transport)
	if err != nil {
		reportCMPhase(observer, cmResolveProfilePhaseID, phaseStateForCMError(ctx, err), "Provider profile unavailable")
		return Result{}, err
	}
	report(Phase{Kind: PhaseGenerate, State: PhaseActive, FileCount: len(snapshot.Files)})
	reportCMPhase(observer, cmGenerateMessagePhaseID, PhaseActive, fmt.Sprintf("Generating from %d files", len(snapshot.Files)))
	generated, err := GenerateCommitMessage(ctx, model, GenerationInput{
		Snapshot:    snapshot,
		Language:    language,
		IncludeBody: input.Body,
	})
	if err != nil {
		reportCMPhase(observer, cmGenerateMessagePhaseID, phaseStateForCMError(ctx, err), "Commit message generation failed")
		return result, err
	}
	result.Generated = &generated
	report(Phase{Kind: PhaseGenerate, State: PhaseCompleted, FileCount: len(snapshot.Files)})
	reportCMPhase(observer, cmGenerateMessagePhaseID, PhaseCompleted, "Commit message generated")
	return result, nil
}

func normalizeLanguage(value string) (string, error) {
	if value == "" {
		return "en", nil
	}
	if value != "en" && value != "zh" {
		return "", errors.New("Unsupported language. Use \"en\" or \"zh\".")
	}
	return value, nil
}
