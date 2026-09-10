package zip

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

// Input is the typed command request after a later CLI binder applies defaults.
type Input struct {
	Directory string
	Open      bool
	WithDir   string
}

// ResultKind distinguishes legacy-visible normal outcomes from unexpected Go failures.
type ResultKind string

const (
	ResultCompleted         ResultKind = "completed"
	ResultCancelled         ResultKind = "cancelled"
	ResultDirectoryNotFound ResultKind = "directory-not-found"
	ResultPathNotDirectory  ResultKind = "path-not-directory"
	ResultCollectionFailed  ResultKind = "collection-failed"
	ResultNoFiles           ResultKind = "no-files"
	ResultNoValidFiles      ResultKind = "no-valid-files"
	ResultCompressionFailed ResultKind = "compression-failed"
	ResultWriteFailed       ResultKind = "write-failed"
)

// Result records the observable command result while preserving the legacy success-status branches.
type Result struct {
	Kind           ResultKind
	Plan           *ZipPlan
	OutputPath     string
	CollectedCount int
	IncludedCount  int
	Cause          error
	RevealFailed   bool
}

// Prompter owns the four command-owned interactions without knowing terminal implementation details.
type Prompter interface {
	SelectPackage(SelectPackageStep) (string, bool, error)
	SelectSource(SelectSourceStep) (string, bool, error)
	SelectGlob(SelectGlobStep) ([]string, bool, error)
	EditOutputFile(EditOutputFileStep) (string, bool, error)
}

// PathStater validates the selected source only after the interactive plan is complete.
type PathStater interface {
	Stat(string) (fs.FileInfo, error)
}

// ArchiveCollector owns glob expansion and regular-file selection.
type ArchiveCollector interface {
	CollectArchiveFiles(string, []string) ([]ArchiveEntry, error)
}

// ArchiveBuilder owns complete in-memory ZIP creation.
type ArchiveBuilder interface {
	BuildZipData([]ArchiveEntry, string, string) ([]byte, int, error)
}

// ArchiveWriter owns the final direct output publication.
type ArchiveWriter interface {
	WriteZipFile(string, []byte) error
}

// Revealer opens a completed archive in the host shell when enabled.
type Revealer interface {
	Reveal(string) error
}

// PhaseReporter receives semantic archive lifecycle updates. It is optional so
// the compatibility Module.Run path remains presentation-independent.
type PhaseReporter interface {
	Report(terminalexperience.OperationPhase) error
}

// Dependencies are the command-owned boundaries required by the complete flow.
type Dependencies struct {
	Prompter           Prompter
	Presenter          Presenter
	RemoteNameResolver RemoteNameResolver
	Stater             PathStater
	Collector          ArchiveCollector
	Builder            ArchiveBuilder
	Writer             ArchiveWriter
	Revealer           Revealer
	Phases             PhaseReporter
}

// Module composes planning, archiving, and result mapping while remaining terminal-independent.
type Module struct {
	prompter           Prompter
	presenter          Presenter
	remoteNameResolver RemoteNameResolver
	stater             PathStater
	collector          ArchiveCollector
	builder            ArchiveBuilder
	writer             ArchiveWriter
	revealer           Revealer
	phases             PhaseReporter
}

// New creates an unregistered ZIP command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.Prompter == nil {
		return nil, errors.New("zip prompter is required")
	}
	if dependencies.Stater == nil {
		dependencies.Stater = osPathStater{}
	}
	if dependencies.Collector == nil {
		dependencies.Collector = archiveCollectorFunc(CollectArchiveFiles)
	}
	if dependencies.Builder == nil {
		dependencies.Builder = archiveBuilderFunc(BuildZipData)
	}
	if dependencies.Writer == nil {
		dependencies.Writer = archiveWriterFunc(WriteZipFile)
	}
	if dependencies.Presenter == nil {
		dependencies.Presenter = discardPresenter{}
	}
	return &Module{
		prompter:           dependencies.Prompter,
		presenter:          dependencies.Presenter,
		remoteNameResolver: dependencies.RemoteNameResolver,
		stater:             dependencies.Stater,
		collector:          dependencies.Collector,
		builder:            dependencies.Builder,
		writer:             dependencies.Writer,
		revealer:           dependencies.Revealer,
		phases:             dependencies.Phases,
	}, nil
}

// Run executes the full legacy planning and archive flow without assigning a process exit code.
func (module *Module) Run(input Input) (Result, error) {
	return module.RunContext(context.Background(), input)
}

// RunContext executes the archive flow while observing cancellation at each
// boundary. The archive collector, builder, and writer remain the same
// command-owned operations; context cancellation never interrupts an
// operation already in progress.
func (module *Module) RunContext(ctx context.Context, input Input) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	presentIntroduction(module.presenter)
	plan, cancelled, err := module.resolvePlanContext(ctx, input.Directory, input.WithDir)
	if err != nil {
		return Result{}, err
	}
	if cancelled {
		presentCancellation(module.presenter)
		return Result{Kind: ResultCancelled}, nil
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := module.reportPhase(zipCollectFilesPhaseID, terminalexperience.PhaseActive, "Collecting files: walking selected source and matching patterns"); err != nil {
		return Result{}, err
	}
	info, err := module.stater.Stat(plan.Input)
	if err != nil {
		if reportErr := module.reportPhase(zipCollectFilesPhaseID, terminalexperience.PhaseFailed, "Source directory unavailable"); reportErr != nil {
			return Result{}, reportErr
		}
		presentDirectoryNotFound(module.presenter, plan.Input)
		return Result{Kind: ResultDirectoryNotFound, Plan: &plan, Cause: err}, nil
	}
	if !info.IsDir() {
		if reportErr := module.reportPhase(zipCollectFilesPhaseID, terminalexperience.PhaseFailed, "Selected path is not a directory"); reportErr != nil {
			return Result{}, reportErr
		}
		presentPathNotDirectory(module.presenter, plan.Input)
		return Result{Kind: ResultPathNotDirectory, Plan: &plan}, nil
	}

	outputPath := filepath.Join(plan.Input, plan.File+".zip")
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	presentCollectionStart(module.presenter)
	entries, err := module.collector.CollectArchiveFiles(plan.Input, plan.Glob)
	if err != nil {
		if reportErr := module.reportPhase(zipCollectFilesPhaseID, terminalexperience.PhaseFailed, "File collection failed"); reportErr != nil {
			return Result{}, reportErr
		}
		presentCollectionFailure(module.presenter, err)
		return Result{Kind: ResultCollectionFailed, Plan: &plan, OutputPath: outputPath, Cause: err}, nil
	}
	if len(entries) == 0 {
		if reportErr := module.reportPhase(zipCollectFilesPhaseID, terminalexperience.PhaseCompleted, "No files matched selected patterns"); reportErr != nil {
			return Result{}, reportErr
		}
		presentNoFiles(module.presenter)
		return Result{Kind: ResultNoFiles, Plan: &plan, OutputPath: outputPath}, nil
	}
	if err := module.reportPhase(zipCollectFilesPhaseID, terminalexperience.PhaseCompleted, fmt.Sprintf("Collected %d file%s", len(entries), pluralSuffix(len(entries)))); err != nil {
		return Result{}, err
	}
	presentCollectedFiles(module.presenter, len(entries))

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := module.reportPhase(zipCompressFilesPhaseID, terminalexperience.PhaseActive, "Building ZIP data in memory"); err != nil {
		return Result{}, err
	}
	presentCompressionStart(module.presenter)
	data, included, err := module.builder.BuildZipData(entries, outputPath, input.WithDir)
	if err != nil {
		if errors.Is(err, errNoValidArchiveFiles) {
			if reportErr := module.reportPhase(zipCompressFilesPhaseID, terminalexperience.PhaseCompleted, "No valid files remained after filtering"); reportErr != nil {
				return Result{}, reportErr
			}
			presentNoValidFiles(module.presenter)
			return Result{Kind: ResultNoValidFiles, Plan: &plan, OutputPath: outputPath, CollectedCount: len(entries), Cause: err}, nil
		}
		if reportErr := module.reportPhase(zipCompressFilesPhaseID, terminalexperience.PhaseFailed, "Compression failed"); reportErr != nil {
			return Result{}, reportErr
		}
		presentCompressionFailure(module.presenter, err)
		return Result{Kind: ResultCompressionFailed, Plan: &plan, OutputPath: outputPath, CollectedCount: len(entries), Cause: err}, nil
	}
	if err := module.reportPhase(zipCompressFilesPhaseID, terminalexperience.PhaseCompleted, fmt.Sprintf("Compressed %d file%s", included, pluralSuffix(included))); err != nil {
		return Result{}, err
	}
	presentCompressionComplete(module.presenter, included)

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := module.reportPhase(zipWriteArchivePhaseID, terminalexperience.PhaseActive, "Publishing completed archive"); err != nil {
		return Result{}, err
	}
	presentWritingStart(module.presenter)
	if err := module.writer.WriteZipFile(outputPath, data); err != nil {
		if reportErr := module.reportPhase(zipWriteArchivePhaseID, terminalexperience.PhaseFailed, "Archive publication failed"); reportErr != nil {
			return Result{}, reportErr
		}
		presentWriteFailure(module.presenter, err)
		return Result{Kind: ResultWriteFailed, Plan: &plan, OutputPath: outputPath, CollectedCount: len(entries), IncludedCount: included, Cause: err}, nil
	}
	if err := module.reportPhase(zipWriteArchivePhaseID, terminalexperience.PhaseCompleted, "Archive published"); err != nil {
		return Result{}, err
	}

	result := Result{
		Kind:           ResultCompleted,
		Plan:           &plan,
		OutputPath:     outputPath,
		CollectedCount: len(entries),
		IncludedCount:  included,
	}
	if input.Open && module.revealer != nil {
		if err := module.reportPhase(zipRevealArchivePhaseID, terminalexperience.PhaseActive, "Opening archive in the host shell"); err != nil {
			return Result{}, err
		}
		if revealErr := module.revealer.Reveal(outputPath); revealErr != nil {
			result.RevealFailed = true
			if err := module.reportPhase(zipRevealArchivePhaseID, terminalexperience.PhaseFailed, "Host reveal failed (warning)"); err != nil {
				return Result{}, err
			}
		} else if err := module.reportPhase(zipRevealArchivePhaseID, terminalexperience.PhaseCompleted, "Archive opened"); err != nil {
			return Result{}, err
		}
	}
	presentSavedArchive(module.presenter, outputPath)
	return result, nil
}

func (module *Module) resolvePlan(directory string) (ZipPlan, bool, error) {
	return module.resolvePlanContext(context.Background(), directory)
}

func (module *Module) resolvePlanContext(ctx context.Context, directory string, withDirValues ...string) (ZipPlan, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	withDir := ""
	if len(withDirValues) > 0 {
		withDir = withDirValues[0]
	}
	if err := module.reportPhase(zipDiscoverWorkspacePhaseID, terminalexperience.PhaseActive, "Inspecting workspace and package candidates"); err != nil {
		return ZipPlan{}, false, err
	}
	session, err := CreatePlanningSession(directory)
	if err != nil {
		_ = module.reportPhase(zipDiscoverWorkspacePhaseID, terminalexperience.PhaseFailed, "Workspace discovery failed")
		return ZipPlan{}, false, err
	}
	discoverClosed := false
	closeDiscover := func(state terminalexperience.PhaseState, detail string) error {
		if discoverClosed {
			return nil
		}
		discoverClosed = true
		return module.reportPhase(zipDiscoverWorkspacePhaseID, state, detail)
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = closeDiscover(terminalexperience.PhaseCancelled, "Workspace planning cancelled")
			return ZipPlan{}, false, err
		}
		resolution, err := ResolvePlanningStep(session, module.remoteNameResolver)
		if err != nil {
			if !discoverClosed {
				_ = closeDiscover(terminalexperience.PhaseFailed, "Planning discovery failed")
			}
			return ZipPlan{}, false, err
		}
		session = resolution.Session
		var answer PlanningAnswer
		switch step := resolution.Step.(type) {
		case SelectPackageStep:
			presentPlanningNote(module.presenter, step.Note)
			value, cancelled, err := module.prompter.SelectPackage(step)
			if err != nil {
				_ = closeDiscover(terminalexperience.PhaseFailed, "Package selection failed")
				return ZipPlan{}, false, err
			}
			if cancelled {
				if closeErr := closeDiscover(terminalexperience.PhaseCancelled, "Package selection cancelled"); closeErr != nil {
					return ZipPlan{}, false, closeErr
				}
				return ZipPlan{}, true, nil
			}
			answer = PlanningAnswer{Type: PlanningAnswerPackage, Value: value}
			if err := closeDiscover(terminalexperience.PhaseCompleted, fmt.Sprintf("Found %d workspace package%s", len(session.Workspace.Packages), pluralSuffix(len(session.Workspace.Packages)))); err != nil {
				return ZipPlan{}, false, err
			}
		case SelectSourceStep:
			if err := closeDiscover(terminalexperience.PhaseCompleted, "Workspace ready for source selection"); err != nil {
				return ZipPlan{}, false, err
			}
			if err := module.reportPhase(zipSelectSourcePhaseID, terminalexperience.PhaseActive, "Reviewing source-directory candidates"); err != nil {
				return ZipPlan{}, false, err
			}
			presentPlanningNote(module.presenter, step.Note)
			value, cancelled, err := module.prompter.SelectSource(step)
			if err != nil {
				_ = module.reportPhase(zipSelectSourcePhaseID, terminalexperience.PhaseFailed, "Source selection failed")
				return ZipPlan{}, false, err
			}
			if cancelled {
				if reportErr := module.reportPhase(zipSelectSourcePhaseID, terminalexperience.PhaseCancelled, "Source selection cancelled"); reportErr != nil {
					return ZipPlan{}, false, reportErr
				}
				return ZipPlan{}, true, nil
			}
			answer = PlanningAnswer{Type: PlanningAnswerSource, Value: value}
			if err := module.reportPhase(zipSelectSourcePhaseID, terminalexperience.PhaseCompleted, "Source directory selected"); err != nil {
				return ZipPlan{}, false, err
			}
		case SelectGlobStep:
			if err := module.reportPhase(zipSelectPatternsPhaseID, terminalexperience.PhaseActive, "Choose archive file patterns"); err != nil {
				return ZipPlan{}, false, err
			}
			values, cancelled, err := module.prompter.SelectGlob(step)
			if err != nil {
				_ = module.reportPhase(zipSelectPatternsPhaseID, terminalexperience.PhaseFailed, "Pattern selection failed")
				return ZipPlan{}, false, err
			}
			if cancelled {
				if reportErr := module.reportPhase(zipSelectPatternsPhaseID, terminalexperience.PhaseCancelled, "Pattern selection cancelled"); reportErr != nil {
					return ZipPlan{}, false, reportErr
				}
				return ZipPlan{}, true, nil
			}
			answer = PlanningAnswer{Type: PlanningAnswerGlob, Values: values}
			if err := module.reportPhase(zipSelectPatternsPhaseID, terminalexperience.PhaseCompleted, fmt.Sprintf("Selected %d pattern%s", len(normalizeSelectedPatterns(values)), pluralSuffix(len(normalizeSelectedPatterns(values))))); err != nil {
				return ZipPlan{}, false, err
			}
		case EditOutputFileStep:
			if err := module.reportPhase(zipPrepareArchivePhaseID, terminalexperience.PhaseActive, "Preparing output name and archive plan"); err != nil {
				return ZipPlan{}, false, err
			}
			value, cancelled, err := module.prompter.EditOutputFile(step)
			if err != nil {
				_ = module.reportPhase(zipPrepareArchivePhaseID, terminalexperience.PhaseFailed, "Output-name planning failed")
				return ZipPlan{}, false, err
			}
			if cancelled {
				if reportErr := module.reportPhase(zipPrepareArchivePhaseID, terminalexperience.PhaseCancelled, "Output-name planning cancelled"); reportErr != nil {
					return ZipPlan{}, false, reportErr
				}
				return ZipPlan{}, true, nil
			}
			answer = PlanningAnswer{Type: PlanningAnswerOutput, Value: value}
		case CompleteStep:
			presentPlanningNote(module.presenter, step.Note)
			return step.Plan, false, nil
		default:
			_ = closeDiscover(terminalexperience.PhaseFailed, "Unknown planning step")
			return ZipPlan{}, false, fmt.Errorf("unknown zip planning step %T", resolution.Step)
		}
		session, err = ApplyPlanningAnswer(session, answer)
		if err != nil {
			return ZipPlan{}, false, err
		}
		if answer.Type == PlanningAnswerOutput {
			if err := module.reportPhase(zipPrepareArchivePhaseID, terminalexperience.PhaseCompleted, zipPrepareArchiveDetail(ZipPlan{
				Input:       session.SelectedSource,
				File:        session.OutputFileName,
				PackageRoot: session.PackageRoot,
			}, withDir)); err != nil {
				return ZipPlan{}, false, err
			}
		}
		if err := ctx.Err(); err != nil {
			return ZipPlan{}, false, err
		}
	}
}

func (module *Module) reportPhase(id string, state terminalexperience.PhaseState, detail string) error {
	if module.phases == nil {
		return nil
	}
	return module.phases.Report(terminalexperience.OperationPhase{ID: id, State: state, Detail: detail})
}

func zipPrepareArchiveDetail(plan ZipPlan, withDir string) string {
	prefix := "off"
	if withDir != "" {
		prefix = "on"
	}
	return fmt.Sprintf("Source: %s; Output: %s.zip; with-dir: %s", safeZipRelativePath(plan.PackageRoot, plan.Input), safeZipName(plan.File), prefix)
}

type osPathStater struct{}

func (osPathStater) Stat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}

type archiveCollectorFunc func(string, []string) ([]ArchiveEntry, error)

func (function archiveCollectorFunc) CollectArchiveFiles(directory string, patterns []string) ([]ArchiveEntry, error) {
	return function(directory, patterns)
}

type archiveBuilderFunc func([]ArchiveEntry, string, string) ([]byte, int, error)

func (function archiveBuilderFunc) BuildZipData(entries []ArchiveEntry, outputPath, withDir string) ([]byte, int, error) {
	return function(entries, outputPath, withDir)
}

type archiveWriterFunc func(string, []byte) error

func (function archiveWriterFunc) WriteZipFile(outputPath string, data []byte) error {
	return function(outputPath, data)
}
