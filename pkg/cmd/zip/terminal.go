package zip

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

const (
	zipPackageFormID            = "select-workspace-package"
	zipSourceFormID             = "select-source-directory"
	zipPatternsFormID           = "select-file-patterns"
	zipOutputFormID             = "edit-output-name"
	zipWorkCatalogID            = "zip-archive"
	zipDiscoverWorkspacePhaseID = "discover-workspace"
	zipSelectSourcePhaseID      = "select-source"
	zipSelectPatternsPhaseID    = "select-patterns"
	zipPrepareArchivePhaseID    = "prepare-archive"
	zipCollectFilesPhaseID      = "collect-files"
	zipCompressFilesPhaseID     = "compress-files"
	zipWriteArchivePhaseID      = "write-archive"
	zipRevealArchivePhaseID     = "reveal-archive"
)

var zipPhaseDefinitions = []terminalexperience.PhaseDefinition{
	{ID: zipDiscoverWorkspacePhaseID, Name: "Discover workspace"},
	{ID: zipSelectSourcePhaseID, Name: "Select source"},
	{ID: zipSelectPatternsPhaseID, Name: "Select patterns"},
	{ID: zipPrepareArchivePhaseID, Name: "Prepare archive"},
	{ID: zipCollectFilesPhaseID, Name: "Collect files"},
	{ID: zipCompressFilesPhaseID, Name: "Compress files"},
	{ID: zipWriteArchivePhaseID, Name: "Write archive"},
	{ID: zipRevealArchivePhaseID, Name: "Reveal archive"},
}

// runZIP is the terminal-owned adapter for the archive command. Module.Run
// remains available for compatibility tests and does not know about terminal
// capabilities or output streams.
func runZIP(options *Options) error {
	if options == nil || options.Terminal == nil {
		return errors.New("zip options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalZipConsoleDescriptor(options))
	if err != nil {
		return err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	if caps.Interaction == terminalexperience.Automation {
		return errors.Join(errZipRequiresInteractive, run.Finish(terminalZipFinishRequest(terminalexperience.Failed, Result{}, ""), nil))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, run.Finish(terminalZipFinishRequest(terminalexperience.Cancelled, Result{}, ""), nil))
	}

	presenter := &terminalZipPresenter{run: run}
	phases := newZipPhaseCoordinator(run, caps)
	adapter := newTerminalZipAdapter(run)
	module, err := New(Dependencies{
		Prompter:           adapter,
		Presenter:          presenter,
		RemoteNameResolver: newZipRemoteNameResolver(osZipRemoteOutputRunner{}),
		Revealer:           newHostZipRevealer(osZipHostCommandRunner{}),
		Phases:             phases,
	})
	if err != nil {
		return errors.Join(err, run.Finish(terminalZipFinishRequest(terminalexperience.Failed, Result{}, ""), nil))
	}
	result, workErr := module.RunContext(ctx, Input{
		Directory: options.Directory,
		Open:      options.Open,
		WithDir:   options.WithDir,
	})
	return finishTerminalZIP(run, caps, phases, presenter, result, workErr)
}

type terminalZipPresenter struct {
	run terminalexperience.ExperienceRun
	err error
}

func (presenter *terminalZipPresenter) Intro() {
	presenter.capture(presenter.run.Notice(terminalZipIntroDocument()))
}

func (presenter *terminalZipPresenter) Note(note PlanningNote) {
	presenter.capture(presenter.run.Notice(terminalZipPlanningNoteDocument(note)))
}

// Progress is represented by Work Phases. Keeping this method a no-op avoids
// replaying the legacy spinner/status lines in addition to the semantic phase.
func (*terminalZipPresenter) Progress(string) {}

// Module.Run reports the final command result to its caller. The terminal
// adapter submits it after the result is classified, so these compatibility
// callbacks intentionally do not write a second result.
func (*terminalZipPresenter) Cancel(string) {}
func (*terminalZipPresenter) Outro(string)  {}

func (presenter *terminalZipPresenter) capture(err error) {
	if err != nil && presenter.err == nil {
		presenter.err = err
	}
}

func terminalZipIntroDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / zip"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Zip Directory"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Plan and publish a bounded archive"},
	}}
}

func terminalZipConsoleDescriptor(options *Options) terminalexperience.ConsoleDescriptor {
	directory := "workspace"
	withDir := "disabled"
	reveal := "disabled"
	if options != nil {
		directory = zipDescriptorDirectory(options.Directory)
		if strings.TrimSpace(options.WithDir) != "" {
			withDir = "enabled"
		}
		if options.Open {
			reveal = "enabled"
		}
	}
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / zip",
		Target:  "Plan and publish a bounded archive",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "summary", Value: "Zip Directory"},
			{Label: "directory", Value: directory},
			{Label: "with-dir", Value: withDir},
			{Label: "reveal", Value: reveal},
		},
		FormCatalog: terminalZipFormCatalog(),
	}
}

func terminalZipFormCatalog() []terminalexperience.ConsoleFormStep {
	return []terminalexperience.ConsoleFormStep{
		{ID: zipPackageFormID, Name: "Workspace package", Detail: "select when multiple packages are found"},
		{ID: zipSourceFormID, Name: "Source directory", Detail: "choose archive source"},
		{ID: zipPatternsFormID, Name: "File patterns", Detail: "select files to include"},
		{ID: zipOutputFormID, Name: "Output name", Detail: "name the archive"},
	}
}

func zipDescriptorDirectory(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "workspace"
	}
	if filepath.IsAbs(value) {
		base := filepath.Base(filepath.Clean(value))
		if base == "" || base == "." || base == string(filepath.Separator) {
			return "workspace"
		}
		return safeZipText(base, "workspace")
	}
	return safeZipText(filepath.ToSlash(filepath.Clean(value)), "workspace")
}

func terminalZipPlanningNoteDocument(note PlanningNote) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: safeZipText(note.Title, "Planning")}}
	if len(note.Lines) > 0 {
		lines := make([]string, 0, len(note.Lines))
		for _, line := range note.Lines {
			lines = append(lines, safeZipText(line, "Planning detail"))
		}
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: strings.Join(lines, "\n")})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

type zipPhaseCoordinator struct {
	run  terminalexperience.ExperienceRun
	caps terminalexperience.Capabilities

	mu          sync.Mutex
	err         error
	active      map[string]terminalexperience.OperationPhase
	work        terminalexperience.WorkSession
	workStarted bool
	closed      bool
	lastPhase   string
}

func newZipPhaseCoordinator(run terminalexperience.ExperienceRun, caps terminalexperience.Capabilities) *zipPhaseCoordinator {
	return &zipPhaseCoordinator{
		run:    run,
		caps:   caps,
		active: make(map[string]terminalexperience.OperationPhase),
	}
}

func (coordinator *zipPhaseCoordinator) Report(update terminalexperience.OperationPhase) error {
	if update.ID == "" {
		update.ID = update.PhaseID
	}
	if update.ID == "" {
		return errors.New("zip phase ID is required")
	}
	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		return terminalexperience.ErrExperienceRunFinished
	}
	coordinator.mu.Unlock()

	if coordinator.caps.Interaction == terminalexperience.RichInteractive {
		if err := coordinator.ensureWork(); err != nil {
			return err
		}
		coordinator.mu.Lock()
		work := coordinator.work
		coordinator.mu.Unlock()
		if err := work.Update(update); err != nil {
			return err
		}
		coordinator.record(update)
		return nil
	}
	if coordinator.caps.Interaction == terminalexperience.Automation {
		return nil
	}
	if err := coordinator.run.Notice(zipPhaseDocument(update)); err != nil {
		return err
	}
	coordinator.record(update)
	return nil
}

func (coordinator *zipPhaseCoordinator) ensureWork() error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.workStarted {
		return coordinator.err
	}
	coordinator.workStarted = true
	work, err := terminalexperience.StartWork(coordinator.run, terminalexperience.WorkCatalog{
		ID:     zipWorkCatalogID,
		Label:  "Create archive",
		Phases: append([]terminalexperience.PhaseDefinition(nil), zipPhaseDefinitions...),
	})
	coordinator.work = work
	coordinator.err = errors.Join(coordinator.err, err)
	return err
}

func (coordinator *zipPhaseCoordinator) record(update terminalexperience.OperationPhase) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if phase := zipFinishLocation(update.ID); phase != "" {
		coordinator.lastPhase = phase
	}
	if update.State == terminalexperience.PhaseActive {
		coordinator.active[update.ID] = update
		return
	}
	if terminalPhaseIsFinal(update.State) {
		delete(coordinator.active, update.ID)
	}
}

func (coordinator *zipPhaseCoordinator) lastPublishedPhase() string {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.lastPhase
}

func (coordinator *zipPhaseCoordinator) markOpen(state terminalexperience.PhaseState, detail string) {
	coordinator.mu.Lock()
	updates := make([]terminalexperience.OperationPhase, 0, len(coordinator.active))
	for _, definition := range zipPhaseDefinitions {
		if _, ok := coordinator.active[definition.ID]; ok {
			updates = append(updates, terminalexperience.OperationPhase{ID: definition.ID, State: state, Detail: detail})
		}
	}
	coordinator.mu.Unlock()
	for _, update := range updates {
		_ = coordinator.Report(update)
	}
}

func (coordinator *zipPhaseCoordinator) finish() error {
	coordinator.mu.Lock()
	if coordinator.closed {
		err := coordinator.err
		coordinator.mu.Unlock()
		return err
	}
	coordinator.closed = true
	work := coordinator.work
	coordinator.mu.Unlock()

	if work != nil {
		workErr := work.Close()
		coordinator.mu.Lock()
		coordinator.err = errors.Join(coordinator.err, workErr)
		err := coordinator.err
		coordinator.mu.Unlock()
		return err
	}
	coordinator.mu.Lock()
	err := coordinator.err
	coordinator.mu.Unlock()
	return err
}

func terminalPhaseIsFinal(state terminalexperience.PhaseState) bool {
	return state == terminalexperience.PhaseCompleted || state == terminalexperience.PhaseCancelled || state == terminalexperience.PhaseFailed
}

func zipPhaseDocument(update terminalexperience.OperationPhase) terminalexperience.PresentationDocument {
	name := zipPhaseName(update.ID)
	text := name
	if update.Detail != "" {
		text += ": " + safeZipText(update.Detail, "Phase detail")
	}
	role := terminalexperience.VisualRoleActive
	switch update.State {
	case terminalexperience.PhaseCompleted:
		role = terminalexperience.VisualRoleSuccess
	case terminalexperience.PhaseCancelled:
		role = terminalexperience.VisualRoleWarning
	case terminalexperience.PhaseFailed:
		role = terminalexperience.VisualRoleError
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func zipPhaseName(id string) string {
	for _, definition := range zipPhaseDefinitions {
		if definition.ID == id {
			return definition.Name
		}
	}
	return "Archive phase"
}

func finishTerminalZIP(
	run terminalexperience.ExperienceRun,
	caps terminalexperience.Capabilities,
	phases *zipPhaseCoordinator,
	presenter *terminalZipPresenter,
	result Result,
	workErr error,
) error {
	if workErr != nil {
		outcome := terminalexperience.Failed
		detail := "Archive operation failed"
		state := terminalexperience.PhaseFailed
		if errors.Is(workErr, context.Canceled) || errors.Is(workErr, context.DeadlineExceeded) {
			outcome = terminalexperience.Cancelled
			detail = "Archive operation cancelled"
			state = terminalexperience.PhaseCancelled
		}
		phases.markOpen(state, detail)
		return errors.Join(workErr, presenter.err, phases.finish(), run.Finish(terminalZipFinishRequest(outcome, result, phases.lastPublishedPhase()), nil))
	}

	outcome := terminalexperience.Succeeded
	if result.Kind == ResultCancelled {
		outcome = terminalexperience.Cancelled
		phases.markOpen(terminalexperience.PhaseCancelled, "Archive planning cancelled")
	} else if result.Kind != ResultCompleted {
		outcome = terminalexperience.Failed
		phases.markOpen(terminalexperience.PhaseFailed, zipResultFailureDetail(result.Kind))
	}

	document := terminalZipResultDocument(result, caps)
	return errors.Join(presenter.err, phases.finish(), run.Finish(terminalZipFinishRequest(outcome, result, phases.lastPublishedPhase()), &document))
}

func terminalZipFinishRequest(outcome terminalexperience.FinishOutcome, result Result, location string) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminalexperience.Succeeded:
		request.Summary = terminalZipOutcomeDocument(result)
	case terminalexperience.Cancelled:
		request.Location = zipFinishLocation(location)
		summary := "Archive cancelled"
		if result.Kind == ResultCancelled {
			summary = "Archive planning cancelled"
		}
		request.Summary = terminalZipOutcomeSummary(summary, terminalexperience.VisualRoleWarning)
	case terminalexperience.Failed:
		request.Location = zipFinishLocation(location)
		request.Summary = terminalZipOutcomeSummary("Archive failed ("+zipFinishFailureCategory(result.Kind)+")", terminalexperience.VisualRoleError)
	}
	return request
}

func terminalZipOutcomeDocument(result Result) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / zip"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Archive complete"},
		{Role: terminalexperience.VisualRoleSuccess, Text: "Archive created"},
		{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Collected %d; included %d; output %s", zipFinishCount(result.CollectedCount), zipFinishCount(result.IncludedCount), zipFinishOutputName(result.Plan))},
	}
	if result.RevealFailed {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Archive created; host reveal unavailable"})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalZipOutcomeSummary(text string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / zip"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Archive outcome"},
		{Role: role, Text: text},
	}}
}

func zipFinishLocation(location string) string {
	for _, definition := range zipPhaseDefinitions {
		if location == definition.ID || location == definition.Name {
			return definition.Name
		}
	}
	return ""
}

func zipFinishFailureCategory(kind ResultKind) string {
	switch kind {
	case ResultDirectoryNotFound:
		return "directory"
	case ResultPathNotDirectory:
		return "path"
	case ResultNoFiles:
		return "no-files"
	case ResultNoValidFiles:
		return "no-valid-files"
	case ResultCollectionFailed:
		return "collection"
	case ResultCompressionFailed:
		return "compression"
	case ResultWriteFailed:
		return "write"
	default:
		return "archive"
	}
}

func zipFinishCount(count int) int {
	if count < 0 {
		return 0
	}
	return count
}

func zipFinishOutputName(plan *ZipPlan) string {
	if plan == nil {
		return "archive.zip"
	}
	name := strings.ReplaceAll(strings.TrimSpace(plan.File), "\\", "/")
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	if name == "." || name == ".." || strings.Contains(name, "://") {
		name = ""
	}
	return safeZipName(name) + ".zip"
}

func terminalZipResultDocument(result Result, caps terminalexperience.Capabilities) terminalexperience.PresentationDocument {
	if result.Kind == ResultCompleted {
		if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
			blocks := []terminalexperience.PresentationBlock{
				{Role: terminalexperience.VisualRoleMuted, Text: "YCY / zip"},
				{Role: terminalexperience.VisualRoleTitle, Text: "Archive ready"},
				{Role: terminalexperience.VisualRoleSuccess, Text: "Done!"},
			}
			if result.Plan != nil {
				blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("%d files included; output %s.zip", result.IncludedCount, safeZipName(result.Plan.File))})
			}
			if result.RevealFailed {
				blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Archive created; host reveal unavailable"})
			}
			return terminalexperience.PresentationDocument{Blocks: blocks}
		}
		return terminalZipDocument("Done!", terminalexperience.VisualRoleSuccess)
	}

	message := "Operation cancelled."
	role := terminalexperience.VisualRoleWarning
	switch result.Kind {
	case ResultDirectoryNotFound:
		message = "Directory not found: " + safeZipPath(result.Plan)
		role = terminalexperience.VisualRoleError
	case ResultPathNotDirectory:
		message = "Path is not a directory: " + safeZipPath(result.Plan)
		role = terminalexperience.VisualRoleError
	case ResultNoFiles:
		message = "No files matched the selected patterns."
		role = terminalexperience.VisualRoleWarning
	case ResultNoValidFiles:
		message = "No valid files matched after filtering."
		role = terminalexperience.VisualRoleWarning
	case ResultCollectionFailed:
		message = "File collection failed (collection)."
		role = terminalexperience.VisualRoleError
	case ResultCompressionFailed:
		message = "Compression failed (compression)."
		role = terminalexperience.VisualRoleError
	case ResultWriteFailed:
		message = "Failed to write zip (write)."
		role = terminalexperience.VisualRoleError
	}
	return terminalZipDocument(message, role)
}

func zipResultFailureDetail(kind ResultKind) string {
	switch kind {
	case ResultDirectoryNotFound:
		return "Directory unavailable (directory)"
	case ResultPathNotDirectory:
		return "Selected path is not a directory (path)"
	case ResultNoFiles:
		return "No files matched (no-files)"
	case ResultNoValidFiles:
		return "No valid files remained (no-valid-files)"
	case ResultCollectionFailed:
		return "Collection failed (collection)"
	case ResultCompressionFailed:
		return "Compression failed (compression)"
	case ResultWriteFailed:
		return "Publication failed (write)"
	default:
		return "Archive operation failed"
	}
}

func safeZipPath(plan *ZipPlan) string {
	if plan == nil {
		return "path"
	}
	return safeZipText(NormalizeRelativePath(plan.PackageRoot, plan.Input), "path")
}

func safeZipRelativePath(root, value string) string {
	if root == "" {
		return safeZipText(value, "source")
	}
	return safeZipText(NormalizeRelativePath(root, value), "source")
}

func safeZipName(value string) string {
	return safeZipText(value, "archive")
}

func safeZipText(value, fallback string) string {
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
	if len(runes) > 256 {
		return string(runes[:256]) + "..."
	}
	return value
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
