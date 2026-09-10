package fork

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
)

// Input is the typed Git Fork command request.
type Input struct {
	Repository  string
	Destination string
}

// DirectoryReader checks whether the requested destination already has entries.
type DirectoryReader interface {
	ReadDir(string) ([]fs.DirEntry, error)
}

// OverwritePrompt describes the only mutation confirmation in Git Fork.
type OverwritePrompt struct {
	Destination string
	Message     string
}

// OverwritePrompter obtains the legacy default-yes replacement decision.
type OverwritePrompter interface {
	ConfirmOverwrite(OverwritePrompt) (confirmed bool, cancelled bool, err error)
}

// Provider resolves a default branch and downloads a provider archive.
type Provider interface {
	DefaultBranch(context.Context, Repository) (string, error)
	DownloadArchive(context.Context, Repository, string) ([]byte, error)
}

// Presenter owns the observable terminal milestones of a Git Fork operation.
type Presenter interface {
	Introduction()
	Cancelled()
	Outcome(Result)
}

// destinationWarningPresenter is an optional presentation hook. A directory
// read failure is intentionally permissive for compatibility, but interactive
// callers still deserve a safe explanation that inspection was unavailable.
type destinationWarningPresenter interface {
	DestinationUnreadable()
}

// Acquisition identifies the successful source of the working tree.
type Acquisition string

const (
	acquisitionArchive Acquisition = "archive"
	acquisitionClone   Acquisition = "clone"
)

// Result records Git Fork's normal completion or prompt-cancellation outcome.
type Result struct {
	Cancelled          bool
	Repository         Repository
	Destination        string
	DestinationPath    string
	Ref                string
	Acquisition        Acquisition
	DefaultBranchError error
	ArchiveError       error
	// DiskFact records a conservative post-failure filesystem fact. It is empty
	// for ordinary success and therefore does not alter the legacy result shape.
	DiskFact string
}

// Dependencies are the command-owned collaboration points for Git Fork orchestration.
type Dependencies struct {
	Config           ConfigReader
	WorkingDirectory func() (string, error)
	Directories      DirectoryReader
	Prompter         OverwritePrompter
	Provider         Provider
	Extractor        ArchiveExtractor
	CloneRunner      CloneRunner
	Remover          DirectoryRemover
	Presenter        Presenter
	Tracker          Tracker
}

// Module owns Git Fork's destination mutation and archive-or-clone behavior.
type Module struct {
	config           ConfigReader
	workingDirectory func() (string, error)
	directories      DirectoryReader
	prompter         OverwritePrompter
	provider         Provider
	extractor        ArchiveExtractor
	cloneRunner      CloneRunner
	remover          DirectoryRemover
	presenter        Presenter
	tracker          Tracker
}

// New constructs an unregistered Git Fork module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.Config == nil {
		return nil, errors.New("git fork config reader is required")
	}
	if dependencies.WorkingDirectory == nil {
		return nil, errors.New("git fork working directory is required")
	}
	if dependencies.Directories == nil {
		return nil, errors.New("git fork directory reader is required")
	}
	if dependencies.Prompter == nil {
		return nil, errors.New("git fork overwrite prompter is required")
	}
	if dependencies.Provider == nil {
		return nil, errors.New("git fork provider is required")
	}
	if dependencies.Extractor == nil {
		return nil, errors.New("git fork archive extractor is required")
	}
	if dependencies.CloneRunner == nil {
		return nil, errors.New("git fork clone runner is required")
	}
	if dependencies.Remover == nil {
		return nil, errors.New("git fork directory remover is required")
	}
	if dependencies.Presenter == nil {
		return nil, errors.New("git fork presenter is required")
	}
	if dependencies.Tracker == nil {
		return nil, errors.New("git fork tracker is required")
	}
	return &Module{
		config:           dependencies.Config,
		workingDirectory: dependencies.WorkingDirectory,
		directories:      dependencies.Directories,
		prompter:         dependencies.Prompter,
		provider:         dependencies.Provider,
		extractor:        dependencies.Extractor,
		cloneRunner:      dependencies.CloneRunner,
		remover:          dependencies.Remover,
		presenter:        dependencies.Presenter,
		tracker:          dependencies.Tracker,
	}, nil
}

// Run executes Git Fork without owning terminal presentation or process exit status.
func (module *Module) Run(ctx context.Context, input Input) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	module.presenter.Introduction()
	observer := module.detailedObserver()
	reportForkPhase(observer, forkResolveRepositoryPhaseID, PhaseActive, safeForkRepositoryInput(input.Repository))
	repository, err := ResolveRepository(input.Repository, module.config)
	if err != nil {
		reportForkPhase(observer, forkResolveRepositoryPhaseID, phaseStateForForkError(ctx, err), "repository could not be resolved")
		return Result{}, err
	}
	reportForkPhase(observer, forkResolveRepositoryPhaseID, PhaseCompleted, safeForkRepositoryDetail(repository))
	workingDirectory, err := module.workingDirectory()
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	destination := input.Destination
	if destination == "" {
		destination = repository.Name
	}
	destinationPath := resolveDestination(workingDirectory, destination)
	result := Result{Repository: repository, Destination: destination, DestinationPath: destinationPath, Ref: repository.Ref}

	reportForkPhase(observer, forkInspectDestinationPhaseID, PhaseActive, safeForkDestinationDetail(destination))
	entries, readErr := module.directories.ReadDir(destinationPath)
	if readErr != nil {
		// Missing and unreadable destinations have historically been treated as
		// empty. Keep that side effect boundary, but make non-missing failures
		// visible as a safe warning in interactive modes.
		if !errors.Is(readErr, fs.ErrNotExist) {
			if warning, ok := module.presenter.(destinationWarningPresenter); ok {
				warning.DestinationUnreadable()
			}
			reportForkPhase(observer, forkInspectDestinationPhaseID, PhaseCompleted, "destination unavailable; continuing")
		} else {
			reportForkPhase(observer, forkInspectDestinationPhaseID, PhaseCompleted, "destination does not exist")
		}
	} else if len(entries) > 0 {
		reportForkPhase(observer, forkInspectDestinationPhaseID, PhaseCompleted, "destination contains existing entries")
		confirmed, cancelled, err := module.prompter.ConfirmOverwrite(OverwritePrompt{
			Destination: destination,
			Message:     "Directory \"" + destinationPath + "\" is not empty. Overwrite all existing contents recursively?",
		})
		if err != nil {
			return Result{}, err
		}
		if cancelled || !confirmed {
			result.Cancelled = true
			if cancelled {
				reportForkMilestone(observer, "Destination replacement cancelled")
			} else {
				reportForkMilestone(observer, "Destination replacement declined")
			}
			reportForkMilestone(observer, "Destination unchanged")
			module.presenter.Cancelled()
			return result, nil
		}
		reportForkPhase(observer, forkReplaceDestinationPhaseID, PhaseActive, safeForkDestinationDetail(destination))
		if err := module.remover.RemoveAll(destinationPath); err != nil {
			reportForkPhase(observer, forkReplaceDestinationPhaseID, phaseStateForForkError(ctx, err), "destination replacement failed")
			result.DiskFact = "Destination may contain partially replaced files"
			return result, err
		}
		reportForkPhase(observer, forkReplaceDestinationPhaseID, PhaseCompleted, "existing destination removed")
		reportForkMilestone(observer, "Existing destination removed")
	} else {
		reportForkPhase(observer, forkInspectDestinationPhaseID, PhaseCompleted, "destination is empty")
	}

	result, err = module.track(ctx, result, func(report func(Phase)) (Result, error) {
		phase := func(kind PhaseKind, state PhaseState) Phase {
			return Phase{Kind: kind, State: state, Repository: repository.Owner + "/" + repository.Name, Ref: result.Ref, Destination: destination}
		}
		report(phase(PhaseResolve, PhaseActive))
		report(phase(PhaseResolve, PhaseCompleted))
		if result.Ref == "" {
			reportForkPhase(observer, forkResolveDefaultBranchPhaseID, PhaseActive, safeForkRepositoryDetail(repository))
			report(phase(PhaseDefaultBranch, PhaseActive))
			result.Ref, result.DefaultBranchError = module.provider.DefaultBranch(ctx, repository)
			if result.DefaultBranchError != nil {
				reportForkPhase(observer, forkResolveDefaultBranchPhaseID, phaseStateForForkError(ctx, result.DefaultBranchError), "default branch unavailable; using remote default")
				report(phase(PhaseDefaultBranch, PhaseFailed))
			} else {
				reportForkPhase(observer, forkResolveDefaultBranchPhaseID, PhaseCompleted, safeForkRefDetail(result.Ref))
				report(phase(PhaseDefaultBranch, PhaseCompleted))
			}
		}
		if result.Ref != "" {
			reportForkPhase(observer, forkDownloadArchivePhaseID, PhaseActive, safeForkRefDetail(result.Ref))
			report(phase(PhaseArchive, PhaseActive))
			archive, archiveErr := module.provider.DownloadArchive(ctx, repository, result.Ref)
			if archiveErr != nil {
				reportForkPhase(observer, forkDownloadArchivePhaseID, phaseStateForForkError(ctx, archiveErr), "archive download failed")
			} else {
				reportForkPhase(observer, forkDownloadArchivePhaseID, PhaseCompleted, "archive downloaded")
				reportForkPhase(observer, forkExtractArchivePhaseID, PhaseActive, safeForkDestinationDetail(destination))
				archiveErr = module.extractor.Extract(destinationPath, archive)
				if archiveErr == nil {
					reportForkPhase(observer, forkExtractArchivePhaseID, PhaseCompleted, "archive extracted")
				} else {
					reportForkPhase(observer, forkExtractArchivePhaseID, phaseStateForForkError(ctx, archiveErr), "archive extraction failed")
					result.DiskFact = "Destination may contain partially extracted files"
				}
			}
			if archiveErr == nil {
				result.Acquisition = acquisitionArchive
				report(phase(PhaseArchive, PhaseCompleted))
				report(phase(PhaseReady, PhaseCompleted))
				return result, nil
			}
			result.ArchiveError = archiveErr
			report(phase(PhaseArchive, PhaseFailed))
		}
		if result.ArchiveError != nil {
			reportForkMilestone(observer, "Falling back to git clone")
		}
		report(phase(PhaseClone, PhaseActive))
		cloneProgress := func(stage cloneFallbackStage, progressErr error) {
			switch stage {
			case cloneFallbackStarted:
				reportForkPhase(observer, forkCloneFallbackPhaseID, PhaseActive, safeForkDestinationDetail(destination))
			case cloneFallbackCompleted:
				result.Acquisition = acquisitionClone
				reportForkPhase(observer, forkCloneFallbackPhaseID, PhaseCompleted, "clone completed")
			case cloneFallbackFailed:
				result.DiskFact = "Destination may contain a partial clone"
				reportForkPhase(observer, forkCloneFallbackPhaseID, phaseStateForForkError(ctx, progressErr), "clone failed")
			case cloneMetadataRemovalStarted:
				reportForkPhase(observer, forkRemoveGitMetadataPhaseID, PhaseActive, safeForkDestinationDetail(destination))
			case cloneMetadataRemovalCompleted:
				reportForkPhase(observer, forkRemoveGitMetadataPhaseID, PhaseCompleted, "Git metadata removed")
			case cloneMetadataRemovalFailed:
				result.DiskFact = "Project files created; Git metadata remains"
				reportForkPhase(observer, forkRemoveGitMetadataPhaseID, phaseStateForForkError(ctx, progressErr), "Git metadata remains")
			}
		}
		if err := cloneFallbackWithProgress(ctx, module.cloneRunner, module.remover, repositoryWithRef(repository, result.Ref), destinationPath, cloneProgress); err != nil {
			return result, err
		}
		result.Acquisition = acquisitionClone
		report(phase(PhaseClone, PhaseCompleted))
		report(phase(PhaseReady, PhaseCompleted))
		return result, nil
	})
	if err != nil {
		return result, err
	}
	module.presenter.Outcome(result)
	return result, nil
}

func resolveDestination(workingDirectory, destination string) string {
	if filepath.IsAbs(destination) {
		return filepath.Clean(destination)
	}
	return filepath.Clean(filepath.Join(workingDirectory, destination))
}

func repositoryWithRef(repository Repository, ref string) Repository {
	repository.Ref = ref
	return repository
}
