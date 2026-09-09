package remove

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type StoreProvider func() (Reader, RemoveWriter, error)

type Options struct {
	Context  context.Context
	Profile  string
	Store    StoreProvider
	Terminal *terminalexperience.Runtime
}

func NewCmdRemove(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runRemove
	}
	return &cobra.Command{Use: "remove <profile>", Short: "Remove a commit message provider profile", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, arguments []string) error {
		if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
			return errors.New("config cm remove Factory is incomplete")
		}
		return runF(&Options{Context: command.Context(), Profile: arguments[0], Store: func() (Reader, RemoveWriter, error) {
			store, err := factory.ConfigStore()
			if err != nil {
				return nil, nil, err
			}
			return store, store, nil
		}, Terminal: factory.Terminal})
	}}
}

func runRemove(options *Options) error {
	_, err := executeRemove(options)
	return err
}

func executeRemove(options *Options) (RemoveResult, error) {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return RemoveResult{}, errors.New("config cm remove options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalCMRemoveConsoleDescriptor(options.Profile))
	if err != nil {
		return RemoveResult{}, err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	if err := ctx.Err(); err != nil {
		return RemoveResult{}, errors.Join(err, run.Finish(terminalCMRemoveFinishRequest(terminalexperience.Cancelled, ""), nil))
	}
	adapter := newTerminalCMRemoveAdapter(run)
	phases := newCMRemovePhaseSink(run, caps)
	if err := phases.beginValidation(); err != nil {
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, cmRemoveValidationPhaseName, nil, err)
	}
	reader, writer, workErr := options.Store()
	if workErr != nil {
		return RemoveResult{}, finishCMRemoveValidationError(run, phases, workErr)
	}
	if reader == nil {
		workErr = errors.New("config cm remove reader is nil")
		return RemoveResult{}, finishCMRemoveValidationError(run, phases, workErr)
	}
	if writer == nil {
		workErr = errors.New("config cm remove writer is nil")
		return RemoveResult{}, finishCMRemoveValidationError(run, phases, workErr)
	}
	profiles, workErr := reader.ListCMProfiles()
	if workErr != nil {
		return RemoveResult{}, finishCMRemoveValidationError(run, phases, workErr)
	}
	if err := ctx.Err(); err != nil {
		phaseErr := phases.endValidation(terminalexperience.PhaseCancelled, "CM profile validation cancelled")
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Cancelled, cmRemoveValidationPhaseName, nil, errors.Join(err, phaseErr))
	}
	var target *appconfig.CMProfile
	for index := range profiles.Profiles {
		if profiles.Profiles[index].Name == options.Profile {
			target = &profiles.Profiles[index]
			break
		}
	}
	if target == nil {
		workErr = fmt.Errorf("CM profile not found: %s", options.Profile)
		phaseErr := phases.endValidation(terminalexperience.PhaseFailed, "Unable to validate CM profile")
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, cmRemoveValidationPhaseName, nil, errors.Join(workErr, phaseErr))
	}
	role := "Configured profile"
	if profiles.DefaultProfile == options.Profile {
		role = "Current default"
	}
	if err := phases.endValidation(terminalexperience.PhaseCompleted, "Profile: "+safeCMRemoveName(options.Profile)+"; Role: "+role); err != nil {
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, cmRemoveValidationPhaseName, nil, err)
	}
	if err := ctx.Err(); err != nil {
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Cancelled, cmRemoveValidationPhaseName, nil, err)
	}
	if caps.Interaction == terminalexperience.Automation {
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, "", nil, errConfigCMRemoveRequiresInteractive)
	}
	question := RemoveConfirmPrompt{Message: fmt.Sprintf("Remove CM profile \"%s\"?", safeCMRemoveName(options.Profile))}
	if role == "Current default" {
		question.Description = "Removing the default selects the first remaining stored profile, or clears the default when none remain."
	}
	confirmed, cancelled, workErr := adapter.Confirm(question)
	if workErr != nil {
		return RemoveResult{}, finishCMRemoveInteractionError(run, phases, workErr)
	}
	if cancelled {
		if caps.Interaction == terminalexperience.RichInteractive {
			if err := run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "Confirmation cancelled"}}}); err != nil {
				return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, "", nil, err)
			}
		}
		document := terminalCMRemoveDocument("Cancelled", true)
		return RemoveResult{Cancelled: true}, finishCMRemove(run, phases, terminalexperience.Cancelled, "", &document, nil)
	}
	if !confirmed {
		if caps.Interaction == terminalexperience.RichInteractive {
			if err := run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "Removal declined"}}}); err != nil {
				return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, "", nil, err)
			}
		}
		document := terminalCMRemoveDocument("Cancelled", true)
		return RemoveResult{Declined: true}, finishCMRemove(run, phases, terminalexperience.Cancelled, "", &document, nil)
	}
	if err := ctx.Err(); err != nil {
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Cancelled, "", nil, err)
	}
	if caps.Interaction == terminalexperience.RichInteractive {
		if err := run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: fmt.Sprintf("Remove CM profile \"%s\": confirmed", safeCMRemoveName(options.Profile))}}}); err != nil {
			return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, "", nil, err)
		}
	}
	if err := phases.beginRemoval(); err != nil {
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, cmRemovePhaseName, nil, err)
	}
	removed, workErr := writer.RemoveCMProfile(options.Profile)
	if workErr != nil {
		phaseErr := phases.endRemoval(terminalexperience.PhaseFailed, "Unable to remove CM profile")
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, cmRemovePhaseName, nil, errors.Join(workErr, phaseErr))
	}
	if !removed {
		workErr = fmt.Errorf("CM profile not found: %s", options.Profile)
		phaseErr := phases.endRemoval(terminalexperience.PhaseFailed, "Unable to remove CM profile")
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, cmRemovePhaseName, nil, errors.Join(workErr, phaseErr))
	}
	if err := phases.endRemoval(terminalexperience.PhaseCompleted, "Profile removed"); err != nil {
		return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Failed, cmRemovePhaseName, nil, err)
	}
	document := terminalCMRemoveDocument(fmt.Sprintf("Profile %s removed", safeCMRemoveName(options.Profile)), false)
	if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
		document = terminalCMRemoveSuccessDocument(options.Profile)
	}
	return RemoveResult{}, finishCMRemove(run, phases, terminalexperience.Succeeded, "", &document, nil)
}

var _ Reader = (*appconfig.Store)(nil)
var _ RemoveWriter = (*appconfig.Store)(nil)

func terminalCMRemoveConsoleDescriptor(profile string) terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / config cm remove",
		Target:  "Remove CM profile - Delete one stored commit message provider",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "scope", Value: "commit message configuration"},
			{Label: "profile", Value: safeCMRemoveName(profile)},
		},
		FormCatalog: []terminalexperience.ConsoleFormStep{{
			ID:     cmRemoveConfirmationFormID,
			Name:   "Confirmation",
			Detail: "default No",
		}},
	}
}

type cmRemovePhaseSink struct {
	run         terminalexperience.ExperienceRun
	caps        terminalexperience.Capabilities
	work        terminalexperience.WorkSession
	workStarted bool
	workClosed  bool
}

func newCMRemovePhaseSink(run terminalexperience.ExperienceRun, caps terminalexperience.Capabilities) *cmRemovePhaseSink {
	return &cmRemovePhaseSink{run: run, caps: caps}
}

func (sink *cmRemovePhaseSink) beginValidation() error {
	if sink.caps.Interaction == terminalexperience.PlainInteractive {
		return sink.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: "Checking CM profile..."}}})
	}
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{ID: cmRemoveValidationPhaseID, State: terminalexperience.PhaseActive, Detail: "Checking profile"})
}

func (sink *cmRemovePhaseSink) endValidation(state terminalexperience.PhaseState, detail string) error {
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{ID: cmRemoveValidationPhaseID, State: state, Detail: detail})
}

func (sink *cmRemovePhaseSink) beginRemoval() error {
	if sink.caps.Interaction == terminalexperience.PlainInteractive {
		return sink.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: "Removing CM profile..."}}})
	}
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{ID: cmRemovePhaseID, State: terminalexperience.PhaseActive, Detail: "Deleting stored profile"})
}

func (sink *cmRemovePhaseSink) endRemoval(state terminalexperience.PhaseState, detail string) error {
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{ID: cmRemovePhaseID, State: state, Detail: detail})
}

func (sink *cmRemovePhaseSink) ensureWork() error {
	if sink.workStarted {
		return nil
	}
	work, err := terminalexperience.StartWork(sink.run, terminalCMRemoveWorkCatalog())
	if err != nil {
		return err
	}
	sink.work = work
	sink.workStarted = true
	return nil
}

func (sink *cmRemovePhaseSink) close() error {
	if sink.workClosed || sink.work == nil {
		return nil
	}
	sink.workClosed = true
	return sink.work.Close()
}

func finishCMRemove(run terminalexperience.ExperienceRun, sink *cmRemovePhaseSink, outcome terminalexperience.FinishOutcome, location string, document *terminalexperience.PresentationDocument, workErr error) error {
	return errors.Join(workErr, sink.close(), run.Finish(terminalCMRemoveFinishRequest(outcome, location), document))
}

func finishCMRemoveValidationError(run terminalexperience.ExperienceRun, sink *cmRemovePhaseSink, workErr error) error {
	outcome := terminalexperience.Failed
	state := terminalexperience.PhaseFailed
	detail := "Unable to validate CM profile"
	if cmRemoveContextCancelled(workErr) {
		outcome = terminalexperience.Cancelled
		state = terminalexperience.PhaseCancelled
		detail = "CM profile validation cancelled"
	}
	phaseErr := sink.endValidation(state, detail)
	return finishCMRemove(run, sink, outcome, cmRemoveValidationPhaseName, nil, errors.Join(workErr, phaseErr))
}

func finishCMRemoveInteractionError(run terminalexperience.ExperienceRun, sink *cmRemovePhaseSink, workErr error) error {
	outcome := terminalexperience.Failed
	if cmRemoveContextCancelled(workErr) {
		outcome = terminalexperience.Cancelled
	}
	return finishCMRemove(run, sink, outcome, "", nil, workErr)
}

func cmRemoveContextCancelled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

const (
	cmRemoveValidationPhaseID   = "validate-cm-profile"
	cmRemoveValidationPhaseName = "Validate CM profile"
	cmRemovePhaseID             = "remove-cm-profile"
	cmRemovePhaseName           = "Remove CM profile"
	cmRemoveConfirmationFormID  = "confirm-removal"
	cmRemoveWorkCatalogID       = "config-cm-remove-work"
)

func terminalCMRemoveWorkCatalog() terminalexperience.WorkCatalog {
	return terminalexperience.WorkCatalog{
		ID:    cmRemoveWorkCatalogID,
		Label: "Remove CM profile",
		Phases: []terminalexperience.PhaseDefinition{
			{ID: cmRemoveValidationPhaseID, Name: cmRemoveValidationPhaseName},
			{ID: cmRemovePhaseID, Name: cmRemovePhaseName},
		},
	}
}

func terminalCMRemoveFinishRequest(outcome terminalexperience.FinishOutcome, location string) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{
		Outcome:  outcome,
		Location: cmRemoveFinishLocation(location),
	}
	summary := "CM profile removal failed"
	role := terminalexperience.VisualRoleError
	switch outcome {
	case terminalexperience.Succeeded:
		summary = "Profile removed"
		role = terminalexperience.VisualRoleSuccess
	case terminalexperience.Cancelled:
		summary = "CM profile removal cancelled"
		role = terminalexperience.VisualRoleWarning
	case terminalexperience.Failed:
		switch request.Location {
		case cmRemoveValidationPhaseName:
			summary = "Unable to validate CM profile"
		case cmRemovePhaseName:
			summary = "Unable to remove CM profile"
		}
	}
	request.Summary = terminalCMRemoveOutcomeDocument(summary, role)
	return request
}

func cmRemoveFinishLocation(location string) string {
	switch location {
	case cmRemoveValidationPhaseName, cmRemovePhaseName:
		return location
	default:
		return ""
	}
}

func terminalCMRemoveOutcomeDocument(text string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func terminalCMRemoveIntroDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / config cm remove"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Remove CM profile"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Delete one stored commit message provider"},
	}}
}

func terminalCMRemoveSuccessDocument(name string) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / config cm remove"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Remove CM profile"},
		{Role: terminalexperience.VisualRoleSuccess, Text: "Profile " + safeCMRemoveName(name) + " removed"},
	}}
}

func safeCMRemoveName(value string) string {
	if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "Profile configured"
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "Profile configured"
	}
	runes := []rune(value)
	if len(runes) > 256 {
		return string(runes[:256]) + "..."
	}
	return value
}
