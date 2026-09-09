package add

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// StoreProvider resolves the appconfig writer at command execution time.
type StoreProvider func() (AddWriter, error)

// Options contains the parsed add request and leaf-owned capabilities.
type Options struct {
	Context  context.Context
	Store    StoreProvider
	Terminal *terminal.Runtime
}

// NewCmdAdd creates the config fork add command with an optional test runner.
func NewCmdAdd(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runAdd
	}
	return &cobra.Command{
		Use:   "add",
		Short: "Add a provider instance",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
				return errors.New("config fork add Factory is incomplete")
			}
			return runF(&Options{
				Context: command.Context(),
				Store: func() (AddWriter, error) {
					store, err := factory.ConfigStore()
					if err != nil {
						return nil, err
					}
					return store, nil
				},
				Terminal: factory.Terminal,
			})
		},
	}
}

func runAdd(options *Options) error {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return errors.New("config fork add options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalForkAddConsoleDescriptor())
	if err != nil {
		return err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	if caps.Interaction == terminal.Automation {
		return errors.Join(errConfigForkAddRequiresInteractive, run.Finish(terminalForkAddFinishRequest(terminal.Failed, forkAddCollectPhaseName), nil))
	}
	phases := newForkAddPhaseSink(run, caps)
	if err := ctx.Err(); err != nil {
		phaseErr := phases.endCollect(terminal.PhaseCancelled, "Provider setup cancelled")
		document := terminalForkAddDocument("Cancelled", true)
		return finishForkAdd(run, phases, terminal.Cancelled, forkAddCollectPhaseName, &document, errors.Join(err, phaseErr))
	}
	adapter := newTerminalForkAddAdapter(run)
	if err := phases.beginCollect(); err != nil {
		return finishForkAdd(run, phases, terminal.Failed, forkAddCollectPhaseName, nil, err)
	}
	input, cancelled, promptErr := PromptAdd(adapter)
	if promptErr != nil {
		phaseErr := phases.endCollect(terminal.PhaseFailed, "Unable to collect provider details")
		return finishForkAdd(run, phases, terminal.Failed, forkAddCollectPhaseName, nil, errors.Join(promptErr, phaseErr))
	}
	if cancelled {
		phaseErr := phases.endCollect(terminal.PhaseCancelled, "Provider setup cancelled")
		document := terminalForkAddDocument("Cancelled", true)
		return finishForkAdd(run, phases, terminal.Cancelled, forkAddCollectPhaseName, &document, phaseErr)
	}
	if err := ctx.Err(); err != nil {
		phaseErr := phases.endCollect(terminal.PhaseCancelled, "Provider setup cancelled")
		document := terminalForkAddDocument("Cancelled", true)
		return finishForkAdd(run, phases, terminal.Cancelled, forkAddCollectPhaseName, &document, errors.Join(err, phaseErr))
	}
	if err := phases.endCollect(terminal.PhaseCompleted, forkAddCollectDetail(input)); err != nil {
		return finishForkAdd(run, phases, terminal.Failed, forkAddCollectPhaseName, nil, err)
	}
	if err := phases.beginSave(); err != nil {
		return finishForkAdd(run, phases, terminal.Failed, forkAddSavePhaseName, nil, err)
	}
	writer, workErr := options.Store()
	if workErr == nil && writer == nil {
		workErr = errors.New("config fork add store is nil")
	}
	if workErr != nil {
		phaseErr := phases.endSave(terminal.PhaseFailed, "Unable to save provider instance")
		return finishForkAdd(run, phases, terminal.Failed, forkAddSavePhaseName, nil, errors.Join(workErr, phaseErr))
	}
	workErr = SaveAdd(writer, input)
	if workErr != nil {
		phaseErr := phases.endSave(terminal.PhaseFailed, "Unable to save provider instance")
		return finishForkAdd(run, phases, terminal.Failed, forkAddSavePhaseName, nil, errors.Join(workErr, phaseErr))
	}
	if err := phases.endSave(terminal.PhaseCompleted, "Provider instance saved"); err != nil {
		return finishForkAdd(run, phases, terminal.Failed, forkAddSavePhaseName, nil, err)
	}
	document := terminalForkAddDocument(fmt.Sprintf("Instance %s (%s) added successfully", safeForkAddField(input.Alias, "Instance configured"), safeForkAddHost(input.Host)), false)
	if caps.Interaction == terminal.RichInteractive && caps.Stdout.Terminal {
		document = terminalForkAddSuccessDocument(input)
	}
	return finishForkAdd(run, phases, terminal.Succeeded, "", &document, nil)
}

var errConfigForkAddRequiresInteractive = errors.New("config fork add requires an interactive terminal")

func terminalForkAddConsoleDescriptor() terminal.ConsoleDescriptor {
	return terminal.ConsoleDescriptor{
		Command: "YCY / config fork add",
		Target:  "Add fork provider instance - Store a provider connection for git fork operations",
		Status:  "READY",
		FormCatalog: []terminal.ConsoleFormStep{
			{ID: forkAddIdentityFormID, Name: "Identity", Detail: "alias"},
			{ID: forkAddHostFormID, Name: "Host", Detail: "hostname"},
			{ID: forkAddProviderFormID, Name: "Provider", Detail: "type"},
			{ID: forkAddProtocolFormID, Name: "Protocol", Detail: "scheme"},
			{ID: forkAddCredentialFormID, Name: "Credential", Detail: "token", Sensitive: true},
		},
	}
}

var _ AddWriter = (*appconfig.Store)(nil)

type forkAddPhaseSink struct {
	run         terminal.ExperienceRun
	caps        terminal.Capabilities
	work        terminal.WorkSession
	workClosed  bool
	workStarted bool
}

func newForkAddPhaseSink(run terminal.ExperienceRun, caps terminal.Capabilities) *forkAddPhaseSink {
	return &forkAddPhaseSink{run: run, caps: caps}
}

func (sink *forkAddPhaseSink) beginCollect() error {
	if sink.caps.Interaction == terminal.PlainInteractive {
		return sink.run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleActive, Text: "Collecting provider details..."}}})
	}
	return nil
}

func (sink *forkAddPhaseSink) endCollect(state terminal.PhaseState, detail string) error {
	if sink.caps.Interaction != terminal.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	if err := sink.work.Update(terminal.OperationPhase{ID: forkAddCollectPhaseID, State: terminal.PhaseActive, Detail: "Answer the five provider fields"}); err != nil {
		return err
	}
	return sink.work.Update(terminal.OperationPhase{ID: forkAddCollectPhaseID, State: state, Detail: detail})
}

func (sink *forkAddPhaseSink) beginSave() error {
	if sink.caps.Interaction == terminal.PlainInteractive {
		return sink.run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleActive, Text: "Saving provider instance..."}}})
	}
	if sink.caps.Interaction == terminal.RichInteractive {
		if err := sink.ensureWork(); err != nil {
			return err
		}
		return sink.work.Update(terminal.OperationPhase{ID: forkAddSavePhaseID, State: terminal.PhaseActive, Detail: "Writing encrypted provider configuration"})
	}
	return nil
}

func (sink *forkAddPhaseSink) endSave(state terminal.PhaseState, detail string) error {
	if sink.caps.Interaction == terminal.PlainInteractive {
		if state == terminal.PhaseCompleted {
			return sink.run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleSuccess, Text: "Saved provider instance"}}})
		}
		return nil
	}
	if sink.caps.Interaction != terminal.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminal.OperationPhase{ID: forkAddSavePhaseID, State: state, Detail: detail})
}

func (sink *forkAddPhaseSink) ensureWork() error {
	if sink.workStarted {
		return nil
	}
	work, err := terminal.StartWork(sink.run, terminalForkAddWorkCatalog())
	if err != nil {
		return err
	}
	sink.work = work
	sink.workStarted = true
	return nil
}

func (sink *forkAddPhaseSink) close() error {
	if sink.workClosed || sink.work == nil {
		return nil
	}
	sink.workClosed = true
	return sink.work.Close()
}

func finishForkAdd(run terminal.ExperienceRun, sink *forkAddPhaseSink, outcome terminal.FinishOutcome, location string, document *terminal.PresentationDocument, workErr error) error {
	return errors.Join(workErr, sink.close(), run.Finish(terminalForkAddFinishRequest(outcome, location), document))
}

const (
	forkAddWorkCatalogID    = "config-fork-add-work"
	forkAddCollectPhaseID   = "collect-provider-details"
	forkAddCollectPhaseName = "Collect provider details"
	forkAddSavePhaseID      = "save-provider-instance"
	forkAddSavePhaseName    = "Save provider instance"
	forkAddIdentityFormID   = "identity"
	forkAddHostFormID       = "host"
	forkAddProviderFormID   = "provider"
	forkAddProtocolFormID   = "protocol"
	forkAddCredentialFormID = "credential"
)

func terminalForkAddWorkCatalog() terminal.WorkCatalog {
	return terminal.WorkCatalog{
		ID:    forkAddWorkCatalogID,
		Label: "Add fork provider instance",
		Phases: []terminal.PhaseDefinition{
			{ID: forkAddCollectPhaseID, Name: forkAddCollectPhaseName},
			{ID: forkAddSavePhaseID, Name: forkAddSavePhaseName},
		},
	}
}

func terminalForkAddFinishRequest(outcome terminal.FinishOutcome, location string) terminal.FinishRequest {
	request := terminal.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminal.Succeeded:
		request.Summary = terminalForkAddOutcomeDocument("Provider instance saved", terminal.VisualRoleSuccess)
	case terminal.Cancelled:
		request.Location = forkAddFinishLocation(location)
		request.Summary = terminalForkAddOutcomeDocument("Provider setup cancelled", terminal.VisualRoleWarning)
	case terminal.Failed:
		request.Location = forkAddFinishLocation(location)
		summary := "Unable to collect provider details"
		if request.Location == forkAddSavePhaseName {
			summary = "Unable to save provider instance"
		}
		request.Summary = terminalForkAddOutcomeDocument(summary, terminal.VisualRoleError)
	}
	return request
}

func forkAddFinishLocation(location string) string {
	switch location {
	case forkAddCollectPhaseName, forkAddSavePhaseName:
		return location
	default:
		return ""
	}
}

func terminalForkAddSuccessDocument(input AddInput) terminal.PresentationDocument {
	return terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{
		{Role: terminal.VisualRoleMuted, Text: "YCY / config fork add"},
		{Role: terminal.VisualRoleTitle, Text: "Add fork provider instance"},
		{Role: terminal.VisualRoleSuccess, Text: "Instance " + safeForkAddField(input.Alias, "Instance configured") + " (" + safeForkAddHost(input.Host) + ") added successfully"},
	}}
}

func forkAddCollectDetail(input AddInput) string {
	return fmt.Sprintf("Instance: %s; Host: %s; Provider: %s; Protocol: %s; Access token: [redacted]", safeForkAddField(input.Alias, "Instance configured"), safeForkAddHost(input.Host), forkAddProviderLabel(input.Type), forkAddProtocolLabel(input.Scheme))
}

func forkAddProviderLabel(value string) string {
	for _, choice := range providerChoices {
		if choice.Value == value {
			return choice.Label
		}
	}
	return "Provider configured"
}

func forkAddProtocolLabel(value string) string {
	for _, choice := range protocolChoices {
		if choice.Value == value {
			return choice.Label
		}
	}
	return "Protocol configured"
}

func safeForkAddHost(value string) string {
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
	result := parsed.Host + parsed.EscapedPath()
	return safeForkAddField(result, "Host configured")
}

func safeForkAddField(value, fallback string) string {
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
