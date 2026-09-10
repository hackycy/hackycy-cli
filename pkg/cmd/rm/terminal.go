package rm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

// runRM owns only the terminal projection. The Module/plan/delete functions
// remain the command's compatibility boundary and are intentionally reused.
func runRM(options *Options) error {
	if options == nil || options.Terminal == nil || options.WorkingDirectory == nil || options.Remover == nil {
		return errors.New("rm options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalRMConsoleDescriptor(options))
	if err != nil {
		return err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	sink := newRMPhaseSink(run, caps, len(options.Paths) > 0)
	if err := ctx.Err(); err != nil {
		return finishRM(run, sink, terminalexperience.Cancelled, nil, err)
	}
	if caps.Interaction == terminalexperience.RichInteractive {
		if err := run.Notice(terminalRMIntroDocument()); err != nil {
			return finishRM(run, sink, terminalexperience.Failed, nil, err)
		}
	}
	workingDirectory, err := options.WorkingDirectory()
	if err != nil {
		return finishRM(run, sink, terminalexperience.Failed, nil, err)
	}
	if err := ctx.Err(); err != nil {
		return finishRM(run, sink, terminalexperience.Cancelled, nil, err)
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		return finishRM(run, sink, terminalexperience.Failed, nil, err)
	}
	if err := ctx.Err(); err != nil {
		return finishRM(run, sink, terminalexperience.Cancelled, nil, err)
	}
	adapter := newTerminalRMAdapter(run)
	input := Input{Paths: append([]string(nil), options.Paths...), Force: options.Force, Depth: options.Depth}
	if len(input.Paths) > 0 {
		return runRMExplicitTerminal(ctx, caps, sink, adapter, options.Remover, workingDirectory, input, run)
	}
	return runRMSmartTerminal(ctx, caps, sink, adapter, options.Remover, workingDirectory, input, run)
}

func runRMExplicitTerminal(
	ctx context.Context,
	caps terminalexperience.Capabilities,
	sink *rmPhaseSink,
	adapter *terminalRMAdapter,
	remover PathRemover,
	workingDirectory string,
	input Input,
	run terminalexperience.ExperienceRun,
) error {
	if err := sink.begin(rmResolvePhaseID, rmResolvePhaseName, "Resolving explicit targets"); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmResolvePhaseName, nil, err)
	}
	plan, err := planExplicit(workingDirectory, input.Paths)
	if err != nil {
		_ = sink.end(terminalexperience.PhaseFailed, "Unable to resolve explicit targets")
		return finishRMAt(run, sink, terminalexperience.Failed, rmResolvePhaseName, nil, err)
	}
	if err := ctx.Err(); err != nil {
		_ = sink.end(terminalexperience.PhaseCancelled, "Resolving explicit targets cancelled")
		return finishRMAt(run, sink, terminalexperience.Cancelled, rmResolvePhaseName, nil, err)
	}
	resolveDetail := fmt.Sprintf("Resolved %d target%s; %d missing", len(plan.existing), rmPlural(len(plan.existing)), len(plan.missing))
	if err := sink.end(terminalexperience.PhaseCompleted, resolveDetail); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmResolvePhaseName, nil, err)
	}
	if err := presentRMMissing(caps, run, workingDirectory, plan.missing); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmResolvePhaseName, nil, err)
	}
	if len(plan.existing) == 0 {
		document := terminalRMNoValidPathsDocument(caps, workingDirectory, plan.missing)
		if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
			document = terminalRMRichResult("Paths removed", "No valid paths to delete.", terminalexperience.VisualRoleWarning)
		}
		return finishRM(run, sink, terminalexperience.Succeeded, &document, nil)
	}
	if !input.Force {
		if err := presentRMExplicitTargets(caps, run, workingDirectory, plan.existing); err != nil {
			return finishRMAt(run, sink, terminalexperience.Failed, rmResolvePhaseName, nil, err)
		}
		description := "Recursive deletion removes all contents. Targets: " + rmPathSummary(workingDirectory, plan.existing)
		confirmed, cancelled, promptErr := adapter.ConfirmExplicit(ExplicitConfirmationPrompt{
			Message:     fmt.Sprintf("Delete %d item%s?", len(plan.existing), rmPlural(len(plan.existing))),
			Initial:     false,
			Description: description,
		})
		if promptErr != nil {
			return finishRMInteractionError(run, sink, rmResolvePhaseName, promptErr)
		}
		if cancelled || !confirmed {
			document := terminalRMDocument("Cancelled.", terminalexperience.VisualRoleWarning)
			return finishRM(run, sink, terminalexperience.Cancelled, &document, nil)
		}
	}
	if err := ctx.Err(); err != nil {
		return finishRMAt(run, sink, terminalexperience.Cancelled, rmResolvePhaseName, nil, err)
	}
	if err := sink.begin(rmDeletePhaseID, rmDeletePhaseName, fmt.Sprintf("Deleting %d target%s", len(plan.existing), rmPlural(len(plan.existing)))); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmDeletePhaseName, nil, err)
	}
	result := deletePaths(plan.existing, remover)
	phaseDetail := rmDeletionDetail(len(plan.existing), result)
	if err := sink.end(terminalexperience.PhaseCompleted, phaseDetail); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmDeletePhaseName, nil, err)
	}
	if err := presentRMDeletion(caps, run, workingDirectory, result); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmDeletePhaseName, nil, err)
	}
	document := terminalRMDocument("Done!", terminalexperience.VisualRoleSuccess)
	if caps.Interaction == terminalexperience.Automation {
		document = terminalRMAutomationDeletionDocument(workingDirectory, plan.missing, result)
	} else if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
		document = terminalRMRichResult("Paths removed", "Done!", terminalexperience.VisualRoleSuccess)
	}
	return finishRM(run, sink, terminalexperience.Succeeded, &document, nil)
}

func runRMSmartTerminal(
	ctx context.Context,
	caps terminalexperience.Capabilities,
	sink *rmPhaseSink,
	adapter *terminalRMAdapter,
	remover PathRemover,
	workingDirectory string,
	input Input,
	run terminalexperience.ExperienceRun,
) error {
	action, cancelled, err := adapter.SelectSmartAction(SmartActionPrompt{Message: "Select a clean action", Options: append([]SmartAction(nil), smartActions...)})
	if err != nil {
		return finishRMInteractionError(run, sink, "", err)
	}
	if cancelled {
		document := terminalRMDocument("Cancelled.", terminalexperience.VisualRoleWarning)
		return finishRM(run, sink, terminalexperience.Cancelled, &document, nil)
	}
	if err := ctx.Err(); err != nil {
		return finishRM(run, sink, terminalexperience.Cancelled, nil, err)
	}
	if err := sink.begin(rmScanPhaseID, rmScanPhaseName, "Scanning cleanup targets"); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmScanPhaseName, nil, err)
	}
	targets := discoverSmart(workingDirectory, action, resolvedSmartDepth(input.Depth))
	if err := ctx.Err(); err != nil {
		_ = sink.end(terminalexperience.PhaseCancelled, "Scanning cleanup targets cancelled")
		return finishRMAt(run, sink, terminalexperience.Cancelled, rmScanPhaseName, nil, err)
	}
	detail := fmt.Sprintf("Found %d target%s", len(targets), rmPlural(len(targets)))
	if err := sink.end(terminalexperience.PhaseCompleted, detail); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmScanPhaseName, nil, err)
	}
	if len(targets) == 0 {
		if err := presentRMSmartEmpty(caps, run); err != nil {
			return finishRMAt(run, sink, terminalexperience.Failed, rmScanPhaseName, nil, err)
		}
		document := terminalRMDocument("Nothing to clean.", terminalexperience.VisualRoleSuccess)
		if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
			document = terminalRMRichResult("Cleanup complete", "Nothing to clean.", terminalexperience.VisualRoleSuccess)
		}
		return finishRM(run, sink, terminalexperience.Succeeded, &document, nil)
	}
	selected := append([]string(nil), targets...)
	if !input.Force {
		options := make([]SmartTargetChoice, 0, len(targets))
		for _, target := range targets {
			relative, relErr := filepath.Rel(workingDirectory, target)
			if relErr != nil {
				return finishRMAt(run, sink, terminalexperience.Failed, rmScanPhaseName, nil, relErr)
			}
			options = append(options, SmartTargetChoice{Value: target, Label: safeRMPathLabel(relative)})
		}
		selected, cancelled, err = adapter.SelectSmartTargets(SmartTargetPrompt{Message: "Select items to delete", Options: options, InitialValues: append([]string(nil), targets...)})
		if err != nil {
			return finishRMInteractionError(run, sink, rmScanPhaseName, err)
		}
		if cancelled {
			document := terminalRMDocument("Cancelled.", terminalexperience.VisualRoleWarning)
			return finishRM(run, sink, terminalexperience.Cancelled, &document, nil)
		}
	}
	if len(selected) == 0 {
		document := terminalRMDocument("Nothing selected.", terminalexperience.VisualRoleWarning)
		return finishRM(run, sink, terminalexperience.Cancelled, &document, nil)
	}
	if err := ctx.Err(); err != nil {
		return finishRMAt(run, sink, terminalexperience.Cancelled, rmScanPhaseName, nil, err)
	}
	if err := sink.begin(rmDeletePhaseID, rmDeletePhaseName, fmt.Sprintf("Deleting %d target%s", len(selected), rmPlural(len(selected)))); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmDeletePhaseName, nil, err)
	}
	result := deletePaths(selected, remover)
	if err := sink.end(terminalexperience.PhaseCompleted, rmDeletionDetail(len(selected), result)); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmDeletePhaseName, nil, err)
	}
	if err := presentRMDeletion(caps, run, workingDirectory, result); err != nil {
		return finishRMAt(run, sink, terminalexperience.Failed, rmDeletePhaseName, nil, err)
	}
	document := terminalRMDocument("Done!", terminalexperience.VisualRoleSuccess)
	if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
		document = terminalRMRichResult("Cleanup complete", "Done!", terminalexperience.VisualRoleSuccess)
	}
	return finishRM(run, sink, terminalexperience.Succeeded, &document, nil)
}

type rmPhaseSink struct {
	run         terminalexperience.ExperienceRun
	caps        terminalexperience.Capabilities
	explicit    bool
	work        terminalexperience.WorkSession
	workStarted bool
	workClosed  bool
	active      bool
	currentID   string
}

func newRMPhaseSink(run terminalexperience.ExperienceRun, caps terminalexperience.Capabilities, explicit ...bool) *rmPhaseSink {
	routeIsExplicit := len(explicit) > 0 && explicit[0]
	return &rmPhaseSink{run: run, caps: caps, explicit: routeIsExplicit}
}

func (sink *rmPhaseSink) begin(id, name, detail string) error {
	if sink.caps.Interaction == terminalexperience.RichInteractive && sink.active {
		return errors.New("rm phase already active")
	}
	if sink.caps.Interaction == terminalexperience.PlainInteractive {
		err := sink.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: name + "..."}}})
		return err
	}
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	if err := sink.work.Update(terminalexperience.OperationPhase{ID: id, State: terminalexperience.PhaseActive, Detail: detail}); err != nil {
		return err
	}
	sink.active = true
	sink.currentID = id
	return nil
}

func (sink *rmPhaseSink) end(state terminalexperience.PhaseState, detail string) error {
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if !sink.active {
		return errors.New("rm phase is not active")
	}
	err := sink.work.Update(terminalexperience.OperationPhase{ID: sink.currentID, State: state, Detail: detail})
	if err == nil {
		sink.active = false
		sink.currentID = ""
	}
	return err
}

func (sink *rmPhaseSink) ensureWork() error {
	if sink.workStarted {
		return nil
	}
	work, err := terminalexperience.StartWork(sink.run, terminalRMWorkCatalog(sink.explicit))
	if err != nil {
		return err
	}
	sink.work = work
	sink.workStarted = true
	return nil
}

func (sink *rmPhaseSink) closeActive() error {
	if sink.caps.Interaction != terminalexperience.RichInteractive || sink.workClosed || sink.work == nil {
		return nil
	}
	sink.workClosed = true
	sink.active = false
	sink.currentID = ""
	return sink.work.Close()
}

func finishRM(run terminalexperience.ExperienceRun, sink *rmPhaseSink, outcome terminalexperience.FinishOutcome, document *terminalexperience.PresentationDocument, workErr error) error {
	return finishRMAt(run, sink, outcome, "", document, workErr)
}

func finishRMAt(run terminalexperience.ExperienceRun, sink *rmPhaseSink, outcome terminalexperience.FinishOutcome, location string, document *terminalexperience.PresentationDocument, workErr error) error {
	return errors.Join(workErr, sink.closeActive(), run.Finish(terminalRMFinishRequest(outcome, sink.explicit, location), document))
}

func finishRMInteractionError(run terminalexperience.ExperienceRun, sink *rmPhaseSink, location string, workErr error) error {
	outcome := terminalexperience.Failed
	if rmContextCancelled(workErr) {
		outcome = terminalexperience.Cancelled
	}
	return finishRMAt(run, sink, outcome, location, nil, workErr)
}

func rmContextCancelled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func terminalRMIntroDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / rm"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Remove"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Remove selected files or clean project artifacts"},
	}}
}

func terminalRMConsoleDescriptor(options *Options) terminalexperience.ConsoleDescriptor {
	route := "smart cleanup"
	mode := "default-negative confirmation"
	if options != nil && len(options.Paths) > 0 {
		route = "explicit path removal"
	}
	if options != nil && options.Force {
		mode = "force"
	}
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / rm",
		Target:  "Remove selected files or clean project artifacts",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "scope", Value: "destructive filesystem mutation"},
			{Label: "route", Value: route},
			{Label: "mode", Value: mode},
		},
		FormCatalog: terminalRMFormCatalog(options),
	}
}

func terminalRMFormCatalog(options *Options) []terminalexperience.ConsoleFormStep {
	if options != nil && len(options.Paths) > 0 {
		return []terminalexperience.ConsoleFormStep{{
			ID:     rmExplicitConfirmationFormID,
			Name:   "Confirmation",
			Detail: "default No",
		}}
	}
	return []terminalexperience.ConsoleFormStep{
		{ID: rmSmartActionFormID, Name: "Clean action", Detail: "choose a cleanup action"},
		{ID: rmSmartTargetsFormID, Name: "Cleanup targets", Detail: "select paths to delete"},
	}
}

const (
	rmWorkCatalogID              = "rm-work"
	rmResolvePhaseID             = "resolve-explicit-targets"
	rmResolvePhaseName           = "Resolve explicit targets"
	rmScanPhaseID                = "scan-cleanup-targets"
	rmScanPhaseName              = "Scan cleanup targets"
	rmDeletePhaseID              = "delete-selected-paths"
	rmDeletePhaseName            = "Delete selected paths"
	rmExplicitConfirmationFormID = "confirm-deletion"
	rmSmartActionFormID          = "select-clean-action"
	rmSmartTargetsFormID         = "select-cleanup-targets"
)

func terminalRMWorkCatalog(explicit ...bool) terminalexperience.WorkCatalog {
	routeIsExplicit := len(explicit) > 0 && explicit[0]
	phases := []terminalexperience.PhaseDefinition{
		{ID: rmScanPhaseID, Name: rmScanPhaseName},
		{ID: rmDeletePhaseID, Name: rmDeletePhaseName},
	}
	if routeIsExplicit {
		phases = []terminalexperience.PhaseDefinition{
			{ID: rmResolvePhaseID, Name: rmResolvePhaseName},
			{ID: rmDeletePhaseID, Name: rmDeletePhaseName},
		}
	}
	return terminalexperience.WorkCatalog{
		ID:     rmWorkCatalogID,
		Label:  "Remove files and project artifacts",
		Phases: phases,
	}
}

func terminalRMRichResult(title, message string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / rm"},
		{Role: terminalexperience.VisualRoleTitle, Text: title},
		{Role: role, Text: message},
	}}
}

func terminalRMFinishRequest(outcome terminalexperience.FinishOutcome, explicit bool, location string) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{
		Outcome:  outcome,
		Location: rmFinishLocation(location),
	}
	summary := "Removal failed"
	role := terminalexperience.VisualRoleError
	switch outcome {
	case terminalexperience.Succeeded:
		summary = "Cleanup complete"
		if explicit {
			summary = "Removal complete"
		}
		role = terminalexperience.VisualRoleSuccess
	case terminalexperience.Cancelled:
		summary = "Removal cancelled"
		role = terminalexperience.VisualRoleWarning
	case terminalexperience.Failed:
		switch request.Location {
		case rmResolvePhaseName:
			summary = "Unable to resolve explicit targets"
		case rmScanPhaseName:
			summary = "Unable to scan cleanup targets"
		case rmDeletePhaseName:
			summary = "Unable to delete selected paths"
		}
	}
	request.Summary = terminalRMDocument(summary, role)
	return request
}

func rmFinishLocation(location string) string {
	switch location {
	case rmResolvePhaseName, rmScanPhaseName, rmDeletePhaseName:
		return location
	default:
		return ""
	}
}

func terminalRMNoValidPathsDocument(caps terminalexperience.Capabilities, root string, missing []string) terminalexperience.PresentationDocument {
	if caps.Interaction != terminalexperience.Automation {
		return terminalRMDocument("No valid paths to delete.", terminalexperience.VisualRoleWarning)
	}
	blocks := rmMissingBlocks(root, missing)
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "No valid paths to delete."})
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

// terminalRMAutomationDeletionDocument preserves the legacy command-result
// facts on successful noninteractive deletions without reintroducing terminal
// interactions, Work Phase diagnostics, or an unbounded path list.
func terminalRMAutomationDeletionDocument(root string, missing []string, result deletionResult) terminalexperience.PresentationDocument {
	blocks := rmMissingBlocks(root, missing)
	blocks = append(blocks, terminalexperience.PresentationBlock{
		Role: terminalexperience.VisualRoleMuted,
		Text: fmt.Sprintf("Deleted %d item%s", result.succeeded, rmPlural(result.succeeded)),
	})
	for _, failure := range result.failures {
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRoleWarning,
			Text: "  skipped: " + safeRMText(failure.Error()),
		})
	}
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: "Done!"})
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func rmMissingBlocks(root string, paths []string) []terminalexperience.PresentationBlock {
	blocks := make([]terminalexperience.PresentationBlock, 0, len(paths))
	for _, path := range paths {
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRoleWarning,
			Text: "  not found, skipping: " + safeRMPathLabel(relativeRMPath(root, path)),
		})
	}
	return blocks
}

func rmPlural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func rmDeletionDetail(requested int, result deletionResult) string {
	return fmt.Sprintf("Requested: %d; Succeeded: %d; Failed: %d", requested, result.succeeded, len(result.failures))
}

func presentRMMissing(caps terminalexperience.Capabilities, run terminalexperience.ExperienceRun, root string, paths []string) error {
	if caps.Interaction == terminalexperience.Automation {
		return nil
	}
	if caps.Interaction == terminalexperience.RichInteractive {
		if len(paths) == 0 {
			return nil
		}
		return run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
			{Role: terminalexperience.VisualRoleWarning, Text: fmt.Sprintf("Missing paths skipped: %d", len(paths))},
			{Role: terminalexperience.VisualRoleMuted, Text: "Paths: " + rmPathSummary(root, paths)},
		}})
	}
	for _, path := range paths {
		relative := safeRMPathLabel(relativeRMPath(root, path))
		if err := run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "  not found, skipping: " + relative}}}); err != nil {
			return err
		}
	}
	return nil
}

func presentRMExplicitTargets(caps terminalexperience.Capabilities, run terminalexperience.ExperienceRun, root string, paths []string) error {
	text := "Targets: " + rmPathSummary(root, paths)
	if caps.Interaction == terminalexperience.RichInteractive {
		blocks := []terminalexperience.PresentationBlock{
			{Role: terminalexperience.VisualRoleWarning, Text: text},
			{Role: terminalexperience.VisualRoleMuted, Text: "Recursive deletion removes all contents."},
		}
		for _, warning := range rmExplicitRiskWarnings(root, paths) {
			blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: warning})
		}
		return run.Milestone(terminalexperience.PresentationDocument{Blocks: blocks})
	}
	if caps.Interaction == terminalexperience.Automation {
		return nil
	}
	return run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleMuted, Text: text}}})
}

func presentRMSmartEmpty(caps terminalexperience.Capabilities, run terminalexperience.ExperienceRun) error {
	if caps.Interaction == terminalexperience.Automation {
		return nil
	}
	return run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleMuted, Text: "No targets found."}}})
}

func presentRMDeletion(caps terminalexperience.Capabilities, run terminalexperience.ExperienceRun, root string, result deletionResult) error {
	if caps.Interaction == terminalexperience.Automation {
		return nil
	}
	if caps.Interaction == terminalexperience.RichInteractive {
		blocks := []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Deleted %d item%s", result.succeeded, rmPlural(result.succeeded))}}
		for _, category := range rmFailureCategories(result.failures) {
			blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "  skipped (" + category + ")"})
		}
		return run.Milestone(terminalexperience.PresentationDocument{Blocks: blocks})
	}
	if err := run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Deleted %d item%s", result.succeeded, rmPlural(result.succeeded))}}}); err != nil {
		return err
	}
	for _, failure := range result.failures {
		if err := run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "  skipped: " + safeRMText(failure.Error())}}}); err != nil {
			return err
		}
	}
	return nil
}

func rmExplicitRiskWarnings(root string, paths []string) []string {
	var containsRootOrParent, containsOutside, containsDuplicate bool
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if _, exists := seen[path]; exists {
			containsDuplicate = true
		}
		seen[path] = struct{}{}
		relative, err := filepath.Rel(path, root)
		if err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))) {
			containsRootOrParent = true
		}
		relative, err = filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			containsOutside = true
		}
	}
	warnings := make([]string, 0, 3)
	if containsRootOrParent {
		warnings = append(warnings, "Warning: target includes the current directory or a parent scope.")
	}
	if containsOutside {
		warnings = append(warnings, "Warning: target is outside the current directory.")
	}
	if containsDuplicate {
		warnings = append(warnings, "Warning: duplicate target selected.")
	}
	return warnings
}

func rmFailureCategories(failures []error) []string {
	categories := make([]string, 0, len(failures))
	seen := make(map[string]struct{}, len(failures))
	for _, failure := range failures {
		category := rmFailureCategory(failure)
		if _, exists := seen[category]; exists {
			continue
		}
		seen[category] = struct{}{}
		categories = append(categories, category)
	}
	return categories
}

func rmPathSummary(root string, paths []string) string {
	const maxPaths = 8
	labels := make([]string, 0, minInt(len(paths), maxPaths))
	for index, path := range paths {
		if index >= maxPaths {
			break
		}
		labels = append(labels, safeRMPathLabel(relativeRMPath(root, path)))
	}
	if len(paths) > maxPaths {
		labels = append(labels, fmt.Sprintf("+%d more", len(paths)-maxPaths))
	}
	return strings.Join(labels, ", ")
}

func relativeRMPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "" {
		return "target"
	}
	return filepath.ToSlash(relative)
}

func safeRMPathLabel(value string) string {
	if !utf8.ValidString(value) {
		return "path"
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
				return "path"
			}
			builder.WriteRune(r)
		}
	}
	value = builder.String()
	if value == "" {
		return "path"
	}
	runes := []rune(value)
	if len(runes) > 160 {
		return string(runes[:160]) + "..."
	}
	return value
}

func safeRMText(value string) string {
	if !utf8.ValidString(value) {
		return "filesystem failure"
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "filesystem failure"
		}
	}
	if len([]rune(value)) > 256 {
		return string([]rune(value)[:256]) + "..."
	}
	return value
}

func rmFailureCategory(err error) string {
	if err == nil {
		return "filesystem"
	}
	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "permission") || strings.Contains(value, "denied"):
		return "permission"
	case strings.Contains(value, "not found") || strings.Contains(value, "no such"):
		return "not-found"
	case strings.Contains(value, "path"):
		return "path"
	default:
		return "filesystem"
	}
}

func minInt(first, second int) int {
	if first < second {
		return first
	}
	return second
}
