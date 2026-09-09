package remove

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// StoreProvider resolves the shared appconfig store only when removal runs.
type StoreProvider func() (RemoveReader, RemoveWriter, error)

// Options contains the parsed remove request and leaf-owned capabilities.
type Options struct {
	Context  context.Context
	Store    StoreProvider
	Terminal *terminalexperience.Runtime
}

// NewCmdRemove creates the config fork remove command with an optional test runner.
func NewCmdRemove(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runRemove
	}
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove a provider instance",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
				return errors.New("config fork remove Factory is incomplete")
			}
			return runF(&Options{
				Context: command.Context(),
				Store: func() (RemoveReader, RemoveWriter, error) {
					store, err := factory.ConfigStore()
					if err != nil {
						return nil, nil, err
					}
					return store, store, nil
				},
				Terminal: factory.Terminal,
			})
		},
	}
}

func runRemove(options *Options) error {
	_, err := executeRemove(options)
	return err
}

func executeRemove(options *Options) (RemoveResult, error) {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return RemoveResult{}, errors.New("config fork remove options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalForkRemoveConsoleDescriptor())
	if err != nil {
		return RemoveResult{}, err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	if err := ctx.Err(); err != nil {
		return RemoveResult{}, errors.Join(err, run.Finish(terminalForkRemoveFinishRequest(terminalexperience.Cancelled, "", false), nil))
	}
	adapter := newTerminalForkRemoveAdapter(run)
	phases := newForkRemovePhaseSink(run, caps)
	if err := phases.beginLoad(); err != nil {
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, forkRemoveLoadPhaseName, false, nil, err)
	}
	reader, writer, workErr := options.Store()
	if workErr != nil {
		return RemoveResult{}, finishForkRemoveLoadError(run, phases, workErr)
	}
	if reader == nil {
		return RemoveResult{}, finishForkRemoveLoadError(run, phases, errors.New("config fork remove reader is nil"))
	}
	if writer == nil {
		return RemoveResult{}, finishForkRemoveLoadError(run, phases, errors.New("config fork remove writer is nil"))
	}
	if err := ctx.Err(); err != nil {
		phaseErr := phases.endLoad(terminalexperience.PhaseCancelled, "Loading fork provider instances cancelled")
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Cancelled, forkRemoveLoadPhaseName, false, nil, errors.Join(err, phaseErr))
	}
	instances, workErr := reader.ListForkInstances()
	if workErr != nil {
		return RemoveResult{}, finishForkRemoveLoadError(run, phases, workErr)
	}
	if err := ctx.Err(); err != nil {
		phaseErr := phases.endLoad(terminalexperience.PhaseCancelled, "Loading fork provider instances cancelled")
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Cancelled, forkRemoveLoadPhaseName, false, nil, errors.Join(err, phaseErr))
	}
	if len(instances) == 0 {
		if err := phases.endLoad(terminalexperience.PhaseCompleted, "No instances configured"); err != nil {
			return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, forkRemoveLoadPhaseName, false, nil, err)
		}
		if caps.Interaction == terminalexperience.RichInteractive {
			if err := run.Milestone(terminalForkRemoveDocument("No instances configured", terminalexperience.VisualRoleMuted)); err != nil {
				return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, forkRemoveLoadPhaseName, false, nil, err)
			}
		} else if caps.Interaction == terminalexperience.PlainInteractive {
			if err := run.Notice(terminalForkRemoveDocument("No instances configured", terminalexperience.VisualRoleMuted)); err != nil {
				return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, forkRemoveLoadPhaseName, false, nil, err)
			}
		}
		document := terminalForkRemoveDocument("Nothing to remove", terminalexperience.VisualRoleSuccess)
		return RemoveResult{Empty: true}, finishForkRemove(run, phases, terminalexperience.Succeeded, forkRemoveLoadPhaseName, true, &document, nil)
	}
	if err := phases.endLoad(terminalexperience.PhaseCompleted, fmt.Sprintf("Loaded %d provider instance%s", len(instances), pluralForkRemove(len(instances)))); err != nil {
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, forkRemoveLoadPhaseName, false, nil, err)
	}
	if caps.Interaction == terminalexperience.Automation {
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, "", false, nil, errConfigForkRemoveRequiresInteractive)
	}
	choices := make([]Choice, len(instances))
	for index, instance := range instances {
		choices[index] = Choice{Value: instance.Name, Label: safeForkRemoveName(instance.Name), Description: safeForkRemoveHost(instance.Host)}
	}
	selected, cancelled, workErr := adapter.Select(SelectPrompt{Message: "Select instance to remove", Choices: choices})
	if workErr != nil {
		return RemoveResult{}, finishForkRemoveInteractionError(run, phases, workErr)
	}
	if cancelled {
		if caps.Interaction == terminalexperience.RichInteractive {
			if err := run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "Selection cancelled"}}}); err != nil {
				return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, "", false, nil, err)
			}
		}
		document := terminalForkRemoveDocument("Cancelled", terminalexperience.VisualRoleWarning)
		return RemoveResult{Cancelled: true}, finishForkRemove(run, phases, terminalexperience.Cancelled, "", false, &document, nil)
	}
	var selectedInstance appconfig.ForkInstance
	found := false
	for _, instance := range instances {
		if instance.Name == selected {
			selectedInstance = instance
			found = true
			break
		}
	}
	if !found {
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, "", false, nil, errors.New("selected fork instance is no longer configured"))
	}
	if caps.Interaction == terminalexperience.RichInteractive {
		if err := run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleMuted, Text: "Host: " + safeForkRemoveHost(selectedInstance.Host)}}}); err != nil {
			return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, "", false, nil, err)
		}
	}
	question := ConfirmPrompt{Message: fmt.Sprintf("Remove instance \"%s\"?", safeForkRemoveName(selected))}
	confirmed, cancelled, workErr := adapter.Confirm(question)
	if workErr != nil {
		return RemoveResult{}, finishForkRemoveInteractionError(run, phases, workErr)
	}
	if cancelled {
		if caps.Interaction == terminalexperience.RichInteractive {
			if err := run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "Confirmation cancelled"}}}); err != nil {
				return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, "", false, nil, err)
			}
		}
		document := terminalForkRemoveDocument("Cancelled", terminalexperience.VisualRoleWarning)
		return RemoveResult{Cancelled: true}, finishForkRemove(run, phases, terminalexperience.Cancelled, "", false, &document, nil)
	}
	if !confirmed {
		if caps.Interaction == terminalexperience.RichInteractive {
			if err := run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: "Removal declined"}}}); err != nil {
				return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, "", false, nil, err)
			}
		}
		document := terminalForkRemoveDocument("Cancelled", terminalexperience.VisualRoleWarning)
		return RemoveResult{Declined: true}, finishForkRemove(run, phases, terminalexperience.Cancelled, "", false, &document, nil)
	}
	if err := ctx.Err(); err != nil {
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Cancelled, "", false, nil, err)
	}
	if caps.Interaction == terminalexperience.RichInteractive {
		if err := run.Milestone(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: fmt.Sprintf("Remove instance \"%s\": confirmed", safeForkRemoveName(selected))}}}); err != nil {
			return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, "", false, nil, err)
		}
	}
	if err := phases.beginRemoval(); err != nil {
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, forkRemovePhaseName, false, nil, err)
	}
	_, workErr = writer.RemoveForkInstance(selected)
	if workErr != nil {
		phaseErr := phases.endRemoval(terminalexperience.PhaseFailed, "Unable to remove provider instance")
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, forkRemovePhaseName, false, nil, errors.Join(workErr, phaseErr))
	}
	if err := phases.endRemoval(terminalexperience.PhaseCompleted, "Provider instance removed"); err != nil {
		return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Failed, forkRemovePhaseName, false, nil, err)
	}
	document := terminalForkRemoveDocument(forkRemoveSuccessMessage(selected), terminalexperience.VisualRoleSuccess)
	if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
		document = terminalForkRemoveSuccessDocument(selected)
	}
	return RemoveResult{}, finishForkRemove(run, phases, terminalexperience.Succeeded, "", false, &document, nil)
}

var _ RemoveReader = (*appconfig.Store)(nil)
var _ RemoveWriter = (*appconfig.Store)(nil)

func terminalForkRemoveConsoleDescriptor() terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / config fork remove",
		Target:  "Remove fork provider instance - Choose a configured provider connection to remove",
		Status:  "READY",
		FormCatalog: []terminalexperience.ConsoleFormStep{
			{ID: forkRemoveSelectionFormID, Name: "Selection", Detail: "provider instance"},
			{ID: forkRemoveConfirmationFormID, Name: "Confirmation", Detail: "default No"},
		},
	}
}

type forkRemovePhaseSink struct {
	run         terminalexperience.ExperienceRun
	caps        terminalexperience.Capabilities
	work        terminalexperience.WorkSession
	workClosed  bool
	workStarted bool
}

func newForkRemovePhaseSink(run terminalexperience.ExperienceRun, caps terminalexperience.Capabilities) *forkRemovePhaseSink {
	return &forkRemovePhaseSink{run: run, caps: caps}
}

func (sink *forkRemovePhaseSink) beginLoad() error {
	if sink.caps.Interaction == terminalexperience.PlainInteractive {
		return sink.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: "Loading fork provider instances..."}}})
	}
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{ID: forkRemoveLoadPhaseID, State: terminalexperience.PhaseActive, Detail: "Reading provider configuration"})
}

func (sink *forkRemovePhaseSink) endLoad(state terminalexperience.PhaseState, detail string) error {
	if sink.caps.Interaction == terminalexperience.PlainInteractive {
		if state == terminalexperience.PhaseCompleted && detail != "No instances configured" {
			return sink.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleSuccess, Text: "Loaded fork provider instances"}}})
		}
		return nil
	}
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{ID: forkRemoveLoadPhaseID, State: state, Detail: detail})
}

func (sink *forkRemovePhaseSink) beginRemoval() error {
	if sink.caps.Interaction == terminalexperience.PlainInteractive {
		return sink.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: "Removing provider instance..."}}})
	}
	if sink.caps.Interaction == terminalexperience.RichInteractive {
		if err := sink.ensureWork(); err != nil {
			return err
		}
		return sink.work.Update(terminalexperience.OperationPhase{ID: forkRemovePhaseID, State: terminalexperience.PhaseActive, Detail: "Deleting stored provider instance"})
	}
	return nil
}

func (sink *forkRemovePhaseSink) endRemoval(state terminalexperience.PhaseState, detail string) error {
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{ID: forkRemovePhaseID, State: state, Detail: detail})
}

func (sink *forkRemovePhaseSink) ensureWork() error {
	if sink.workStarted {
		return nil
	}
	work, err := terminalexperience.StartWork(sink.run, terminalForkRemoveWorkCatalog())
	if err != nil {
		return err
	}
	sink.work = work
	sink.workStarted = true
	return nil
}

func (sink *forkRemovePhaseSink) close() error {
	if sink.workClosed || sink.work == nil {
		return nil
	}
	sink.workClosed = true
	return sink.work.Close()
}

func finishForkRemove(run terminalexperience.ExperienceRun, sink *forkRemovePhaseSink, outcome terminalexperience.FinishOutcome, location string, empty bool, document *terminalexperience.PresentationDocument, workErr error) error {
	return errors.Join(workErr, sink.close(), run.Finish(terminalForkRemoveFinishRequest(outcome, location, empty), document))
}

func finishForkRemoveLoadError(run terminalexperience.ExperienceRun, sink *forkRemovePhaseSink, workErr error) error {
	if forkRemoveContextCancelled(workErr) {
		phaseErr := sink.endLoad(terminalexperience.PhaseCancelled, "Loading fork provider instances cancelled")
		return finishForkRemove(run, sink, terminalexperience.Cancelled, forkRemoveLoadPhaseName, false, nil, errors.Join(workErr, phaseErr))
	}
	phaseErr := sink.endLoad(terminalexperience.PhaseFailed, "Unable to load fork provider instances")
	return finishForkRemove(run, sink, terminalexperience.Failed, forkRemoveLoadPhaseName, false, nil, errors.Join(workErr, phaseErr))
}

func finishForkRemoveInteractionError(run terminalexperience.ExperienceRun, sink *forkRemovePhaseSink, workErr error) error {
	outcome := terminalexperience.Failed
	if forkRemoveContextCancelled(workErr) {
		outcome = terminalexperience.Cancelled
	}
	return finishForkRemove(run, sink, outcome, "", false, nil, workErr)
}

func forkRemoveContextCancelled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func terminalForkRemoveFinishRequest(outcome terminalexperience.FinishOutcome, location string, empty bool) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome, Location: forkRemoveFinishLocation(location)}
	summary := "Provider removal failed"
	role := terminalexperience.VisualRoleError
	switch outcome {
	case terminalexperience.Succeeded:
		summary = "Provider instance removed"
		if empty {
			summary = "No instances configured"
		}
		role = terminalexperience.VisualRoleSuccess
	case terminalexperience.Cancelled:
		summary = "Provider removal cancelled"
		role = terminalexperience.VisualRoleWarning
	case terminalexperience.Failed:
		switch request.Location {
		case forkRemoveLoadPhaseName:
			summary = "Unable to load fork provider instances"
		case forkRemovePhaseName:
			summary = "Unable to remove provider instance"
		}
	}
	request.Summary = terminalForkRemoveDocument(summary, role)
	return request
}

func forkRemoveFinishLocation(location string) string {
	switch location {
	case forkRemoveLoadPhaseName, forkRemovePhaseName:
		return location
	default:
		return ""
	}
}

const (
	forkRemoveWorkCatalogID      = "config-fork-remove-work"
	forkRemoveLoadPhaseID        = "load-fork-provider-instances"
	forkRemoveLoadPhaseName      = "Load fork provider instances"
	forkRemovePhaseID            = "remove-provider-instance"
	forkRemovePhaseName          = "Remove provider instance"
	forkRemoveSelectionFormID    = "select-instance"
	forkRemoveConfirmationFormID = "confirm-removal"
)

func terminalForkRemoveWorkCatalog() terminalexperience.WorkCatalog {
	return terminalexperience.WorkCatalog{
		ID:    forkRemoveWorkCatalogID,
		Label: "Remove fork provider instance",
		Phases: []terminalexperience.PhaseDefinition{
			{ID: forkRemoveLoadPhaseID, Name: forkRemoveLoadPhaseName},
			{ID: forkRemovePhaseID, Name: forkRemovePhaseName},
		},
	}
}

func terminalForkRemoveSuccessDocument(name string) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / config fork remove"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Remove fork provider instance"},
		{Role: terminalexperience.VisualRoleSuccess, Text: forkRemoveSuccessMessage(name)},
	}}
}

func pluralForkRemove(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func safeForkRemoveName(value string) string {
	return safeForkRemoveField(value, "Selected instance")
}

func safeForkRemoveHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !utf8.ValidString(trimmed) || strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return "Host configured"
	}
	parseValue := trimmed
	if !strings.Contains(parseValue, "://") {
		parseValue = "https://" + parseValue
	}
	parsed, err := url.Parse(parseValue)
	if err != nil || parsed.Host == "" {
		return "Host configured"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return safeForkRemoveField(parsed.Host+parsed.EscapedPath(), "Host configured")
}

func forkRemoveSuccessMessage(name string) string {
	projected := safeForkRemoveName(name)
	if projected == "Selected instance" {
		return "Instance removed"
	}
	return "Instance " + projected + " removed"
}

func safeForkRemoveField(value, fallback string) string {
	if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fallback
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > 256 {
		return string(runes[:256]) + "..."
	}
	return value
}
