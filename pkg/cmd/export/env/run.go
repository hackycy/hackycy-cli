package env

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// Input is the typed export env command input.
type Input struct {
	Directory   string
	Environment string
	Merge       bool
	Output      string
}

// Result records a successful command outcome.
type Result struct {
	Cancelled bool
}

// Dependencies are the command-owned external adapters.
type Dependencies struct {
	WorkingDirectory func() (string, error)
	Selector         EnvironmentSelector
	Reader           FileReader
	Writer           FileWriter
	Presenter        Presenter
}

// Module owns export env behavior behind one typed command interface.
type Module struct {
	workingDirectory func() (string, error)
	selector         EnvironmentSelector
	reader           FileReader
	writer           FileWriter
	presenter        Presenter
}

// runObserver receives command-owned phase boundaries for the terminal path.
// The public Module.Run contract remains presentation-compatible for existing
// callers and tests.
type runObserver struct {
	phase     func(id, name string, state terminalPhaseState, detail string)
	selected  func(selection Selection, source string, merge bool)
	variables func(count int)
	output    string
}

type terminalPhaseState uint8

const (
	terminalPhaseActive terminalPhaseState = iota
	terminalPhaseSucceeded
	terminalPhaseCancelled
	terminalPhaseFailed
)

// New constructs an export env command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.WorkingDirectory == nil {
		return nil, errors.New("export env working directory is required")
	}
	if dependencies.Selector == nil {
		return nil, errors.New("export env selector is required")
	}
	if dependencies.Reader == nil {
		return nil, errors.New("export env reader is required")
	}
	if dependencies.Writer == nil {
		return nil, errors.New("export env writer is required")
	}
	if dependencies.Presenter == nil {
		return nil, errors.New("export env presenter is required")
	}
	return &Module{
		workingDirectory: dependencies.WorkingDirectory,
		selector:         dependencies.Selector,
		reader:           dependencies.Reader,
		writer:           dependencies.Writer,
		presenter:        dependencies.Presenter,
	}, nil
}

// Run executes the export env command without owning process exit behavior.
func (module *Module) Run(ctx context.Context, input Input) (Result, error) {
	return module.run(ctx, input, nil)
}

// run executes the established workflow. When observer is non-nil, legacy
// Presenter calls are suppressed and phase boundaries are reported instead.
func (module *Module) run(ctx context.Context, input Input, observer *runObserver) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	phase := func(id, name string, state terminalPhaseState, detail string) {
		if observer != nil && observer.phase != nil {
			observer.phase(id, name, state, detail)
		}
	}
	phase("resolve-directory", "Resolve directory", terminalPhaseActive, "")
	if err := ctx.Err(); err != nil {
		phase("resolve-directory", "Resolve directory", terminalPhaseCancelled, "Cancelled")
		return Result{}, err
	}
	workingDirectory, err := module.workingDirectory()
	if err != nil {
		phase("resolve-directory", "Resolve directory", terminalPhaseFailed, "Unable to resolve directory")
		return Result{}, err
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		phase("resolve-directory", "Resolve directory", terminalPhaseFailed, "Unable to resolve directory")
		return Result{}, err
	}
	phase("resolve-directory", "Resolve directory", terminalPhaseSucceeded, "Directory ready")

	phase("discover-environment-files", "Discover environment files", terminalPhaseActive, "")
	discovery, err := Discover(resolveInputDirectory(workingDirectory, input.Directory))
	if err != nil {
		phase("discover-environment-files", "Discover environment files", terminalPhaseFailed, "Unable to discover environment files")
		return Result{}, err
	}
	phase("discover-environment-files", "Discover environment files", terminalPhaseSucceeded, fmt.Sprintf("Found %d environment file%s", discoveryFileCount(discovery), pluralSuffix(discoveryFileCount(discovery))))

	needsSelection := selectionNeedsPrompt(discovery, SelectionOptions{Environment: input.Environment, Merge: input.Merge})
	if needsSelection {
		phase("select-environment", "Select environment", terminalPhaseActive, "Choose an environment file")
	}
	selection, err := Select(discovery, SelectionOptions{
		Environment: input.Environment,
		Merge:       input.Merge,
	}, module.selector)
	if err != nil {
		if needsSelection {
			phase("select-environment", "Select environment", terminalPhaseFailed, "Unable to select environment")
		}
		return Result{}, err
	}
	if selection.Cancelled {
		if needsSelection {
			phase("select-environment", "Select environment", terminalPhaseCancelled, "Selection cancelled")
		}
		if observer == nil {
			PresentCancellation(module.presenter)
		}
		return Result{Cancelled: true}, nil
	}
	if observer != nil {
		source := "unique candidate"
		if input.Environment != "" {
			source = "--env"
		} else if needsSelection {
			source = "user selection"
		}
		if observer.selected != nil {
			observer.selected(selection, source, input.Merge)
		}
	}
	if needsSelection {
		phase("select-environment", "Select environment", terminalPhaseSucceeded, "Environment selected")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	phase("read-selected-files", "Read selected files", terminalPhaseActive, "")
	contents, err := Read(discovery, selection, module.reader)
	if err != nil {
		phase("read-selected-files", "Read selected files", terminalPhaseFailed, "Unable to read selected files")
		return Result{}, err
	}
	phase("read-selected-files", "Read selected files", terminalPhaseSucceeded, fmt.Sprintf("Read %d file%s", len(contents), pluralSuffix(len(contents))))
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	phase("parse-and-merge-values", "Parse and merge values", terminalPhaseActive, "")
	values := Parse(contents...)
	if observer != nil {
		if observer.variables != nil {
			observer.variables(len(values))
		}
	}
	phase("parse-and-merge-values", "Parse and merge values", terminalPhaseSucceeded, fmt.Sprintf("Parsed %d variable%s", len(values), pluralSuffix(len(values))))

	phase("encode-json", "Encode JSON", terminalPhaseActive, "")
	output, err := Encode(values)
	if err != nil {
		phase("encode-json", "Encode JSON", terminalPhaseFailed, "Unable to encode JSON")
		return Result{}, err
	}
	if observer != nil {
		observer.output = output
	}
	phase("encode-json", "Encode JSON", terminalPhaseSucceeded, "JSON ready")
	if input.Output != "" {
		phase("write-output-file", "Write output file", terminalPhaseActive, "")
		if observer == nil {
			// Keep historical direct-Module ordering; the terminal adapter writes
			// first and only then emits its success result.
			Present(module.presenter, output, input.Output)
		}
		if err := WriteOutput(workingDirectory, input.Output, output, module.writer); err != nil {
			phase("write-output-file", "Write output file", terminalPhaseFailed, "Unable to write output file")
			return Result{}, err
		}
		phase("write-output-file", "Write output file", terminalPhaseSucceeded, "Output written")
		return Result{}, nil
	}

	if observer == nil {
		Present(module.presenter, output, "")
	}
	return Result{}, nil
}

func discoveryFileCount(discovery Discovery) int {
	count := len(discovery.EnvironmentFiles)
	if discovery.BaseFile != "" {
		count++
	}
	return count
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func selectionNeedsPrompt(discovery Discovery, options SelectionOptions) bool {
	if options.Environment != "" {
		return false
	}
	selectable := len(discovery.EnvironmentFiles)
	if !options.Merge && discovery.BaseFile != "" {
		selectable++
	}
	if selectable <= 1 {
		return false
	}
	return true
}

func resolveInputDirectory(workingDirectory, directory string) string {
	if directory == "" {
		return workingDirectory
	}
	if filepath.IsAbs(directory) {
		return filepath.Clean(directory)
	}
	return filepath.Join(workingDirectory, directory)
}
