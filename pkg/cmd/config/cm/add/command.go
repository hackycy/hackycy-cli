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

type StoreProvider func() (AddWriter, error)

type Options struct {
	Context  context.Context
	Store    StoreProvider
	Terminal *terminal.Runtime
}

func NewCmdAdd(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runAdd
	}
	return &cobra.Command{
		Use:   "add",
		Short: "Add a CM profile",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
				return errors.New("config cm add Factory is incomplete")
			}
			return runF(&Options{Context: command.Context(), Store: func() (AddWriter, error) {
				store, err := factory.ConfigStore()
				if err != nil {
					return nil, err
				}
				return store, nil
			}, Terminal: factory.Terminal})
		},
	}
}

func runAdd(options *Options) error {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return errors.New("config cm add options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalCMAddConsoleDescriptor())
	if err != nil {
		return err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	if err := ctx.Err(); err != nil {
		return errors.Join(err, run.Finish(terminalCMAddFinishRequest(terminal.Cancelled, cmAddCollectPhaseName), nil))
	}
	if caps.Interaction == terminal.Automation {
		return errors.Join(errConfigCMAddRequiresInteractive, run.Finish(terminalCMAddFinishRequest(terminal.Failed, cmAddCollectPhaseName), nil))
	}
	adapter := newTerminalCMAddAdapter(run)
	phases := newCMAddPhaseSink(run, caps)
	if err := phases.beginCollect(); err != nil {
		return finishCMAdd(run, phases, terminal.Failed, cmAddCollectPhaseName, nil, err)
	}
	writer, workErr := options.Store()
	if workErr == nil && writer == nil {
		workErr = errors.New("config cm add store is nil")
	}
	if workErr != nil {
		phaseErr := phases.endCollect(terminal.PhaseFailed, "Unable to collect CM profile details")
		return finishCMAdd(run, phases, terminal.Failed, cmAddCollectPhaseName, nil, errors.Join(workErr, phaseErr))
	}
	input, cancelled, promptErr := PromptAdd(adapter)
	if promptErr != nil {
		phaseErr := phases.endCollect(terminal.PhaseFailed, "Unable to collect CM profile details")
		return finishCMAdd(run, phases, terminal.Failed, cmAddCollectPhaseName, nil, errors.Join(promptErr, phaseErr))
	}
	if cancelled {
		phaseErr := phases.endCollect(terminal.PhaseCancelled, "Profile setup cancelled")
		document := terminalCMAddDocument("Cancelled", true)
		return finishCMAdd(run, phases, terminal.Cancelled, cmAddCollectPhaseName, &document, phaseErr)
	}
	if err := ctx.Err(); err != nil {
		phaseErr := phases.endCollect(terminal.PhaseCancelled, "Profile setup cancelled")
		document := terminalCMAddDocument("Cancelled", true)
		return finishCMAdd(run, phases, terminal.Cancelled, cmAddCollectPhaseName, &document, errors.Join(err, phaseErr))
	}
	if err := phases.endCollect(terminal.PhaseCompleted, cmAddCollectDetail(input)); err != nil {
		return finishCMAdd(run, phases, terminal.Failed, cmAddCollectPhaseName, nil, err)
	}
	if err := phases.beginSave(); err != nil {
		return finishCMAdd(run, phases, terminal.Failed, cmAddSavePhaseName, nil, err)
	}
	workErr = SaveAdd(writer, input)
	if workErr != nil {
		phaseErr := phases.endSave(terminal.PhaseFailed, "Unable to save CM profile")
		return finishCMAdd(run, phases, terminal.Failed, cmAddSavePhaseName, nil, errors.Join(workErr, phaseErr))
	}
	if err := phases.endSave(terminal.PhaseCompleted, "Profile saved"); err != nil {
		return finishCMAdd(run, phases, terminal.Failed, cmAddSavePhaseName, nil, err)
	}
	document := terminalCMAddDocument(fmt.Sprintf("Profile %s added", safeCMAddName(input.Name)), false)
	if caps.Interaction == terminal.RichInteractive && caps.Stdout.Terminal {
		document = terminalCMAddSuccessDocument(input)
	}
	return finishCMAdd(run, phases, terminal.Succeeded, "", &document, nil)
}

var errConfigCMAddRequiresInteractive = errors.New("config cm add requires an interactive terminal")

func terminalCMAddConsoleDescriptor() terminal.ConsoleDescriptor {
	return terminal.ConsoleDescriptor{
		Command: "YCY / config cm add",
		Target:  "Add commit message profile - Configure an OpenAI-compatible provider",
		Status:  "READY",
		FormCatalog: []terminal.ConsoleFormStep{
			{ID: cmAddIdentityFormID, Name: "Identity", Detail: "profile name"},
			{ID: cmAddEndpointFormID, Name: "Endpoint", Detail: "base URL"},
			{ID: cmAddModelFormID, Name: "Model", Detail: "model"},
			{ID: cmAddCredentialFormID, Name: "Credential", Detail: "API key", Sensitive: true},
		},
	}
}

var _ AddWriter = (*appconfig.Store)(nil)

type cmAddPhaseSink struct {
	run         terminal.ExperienceRun
	caps        terminal.Capabilities
	work        terminal.WorkSession
	workClosed  bool
	workStarted bool
}

func newCMAddPhaseSink(run terminal.ExperienceRun, caps terminal.Capabilities) *cmAddPhaseSink {
	return &cmAddPhaseSink{run: run, caps: caps}
}

func (sink *cmAddPhaseSink) beginCollect() error {
	if sink.caps.Interaction == terminal.PlainInteractive {
		_ = sink.run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleActive, Text: "Collecting CM profile details..."}}})
		return nil
	}
	if sink.caps.Interaction != terminal.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminal.OperationPhase{ID: cmAddCollectPhaseID, State: terminal.PhaseActive, Detail: "Answer the four profile fields"})
}

func (sink *cmAddPhaseSink) endCollect(state terminal.PhaseState, detail string) error {
	if sink.caps.Interaction != terminal.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminal.OperationPhase{ID: cmAddCollectPhaseID, State: state, Detail: detail})
}

func (sink *cmAddPhaseSink) beginSave() error {
	if sink.caps.Interaction == terminal.PlainInteractive {
		_ = sink.run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Role: terminal.VisualRoleActive, Text: "Saving CM profile..."}}})
		return nil
	}
	if sink.caps.Interaction != terminal.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminal.OperationPhase{ID: cmAddSavePhaseID, State: terminal.PhaseActive, Detail: "Writing encrypted profile"})
}

func (sink *cmAddPhaseSink) endSave(state terminal.PhaseState, detail string) error {
	if sink.caps.Interaction != terminal.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminal.OperationPhase{ID: cmAddSavePhaseID, State: state, Detail: detail})
}

func (sink *cmAddPhaseSink) ensureWork() error {
	if sink.workStarted {
		return nil
	}
	work, err := terminal.StartWork(sink.run, terminalCMAddWorkCatalog())
	if err != nil {
		return err
	}
	sink.work = work
	sink.workStarted = true
	return nil
}

func (sink *cmAddPhaseSink) close() error {
	if sink.workClosed || sink.work == nil {
		return nil
	}
	sink.workClosed = true
	return sink.work.Close()
}

func finishCMAdd(run terminal.ExperienceRun, sink *cmAddPhaseSink, outcome terminal.FinishOutcome, location string, document *terminal.PresentationDocument, workErr error) error {
	return errors.Join(workErr, sink.close(), run.Finish(terminalCMAddFinishRequest(outcome, location), document))
}

const (
	cmAddWorkCatalogID    = "config-cm-add-work"
	cmAddIdentityFormID   = "identity"
	cmAddEndpointFormID   = "endpoint"
	cmAddModelFormID      = "model"
	cmAddCredentialFormID = "credential"

	cmAddCollectPhaseID   = "collect-cm-profile-details"
	cmAddCollectPhaseName = "Collect CM profile details"
	cmAddSavePhaseID      = "save-cm-profile"
	cmAddSavePhaseName    = "Save CM profile"
)

func terminalCMAddWorkCatalog() terminal.WorkCatalog {
	return terminal.WorkCatalog{
		ID:    cmAddWorkCatalogID,
		Label: "Add commit message profile",
		Phases: []terminal.PhaseDefinition{
			{ID: cmAddCollectPhaseID, Name: cmAddCollectPhaseName},
			{ID: cmAddSavePhaseID, Name: cmAddSavePhaseName},
		},
	}
}

func terminalCMAddFinishRequest(outcome terminal.FinishOutcome, location string) terminal.FinishRequest {
	request := terminal.FinishRequest{Outcome: outcome, Location: cmAddFinishLocation(location)}
	summary := "Unable to collect CM profile details"
	role := terminal.VisualRoleError
	switch outcome {
	case terminal.Succeeded:
		summary = "Profile saved"
		role = terminal.VisualRoleSuccess
	case terminal.Cancelled:
		summary = "Profile setup cancelled"
		role = terminal.VisualRoleWarning
	case terminal.Failed:
		if request.Location == cmAddSavePhaseName {
			summary = "Unable to save CM profile"
		}
	}
	request.Summary = terminalCMAddOutcomeDocument(summary, role)
	return request
}

func cmAddFinishLocation(location string) string {
	switch location {
	case cmAddCollectPhaseName, cmAddSavePhaseName:
		return location
	default:
		return ""
	}
}

func terminalCMAddSuccessDocument(input AddInput) terminal.PresentationDocument {
	return terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{
		{Role: terminal.VisualRoleMuted, Text: "YCY / config cm add"},
		{Role: terminal.VisualRoleTitle, Text: "Add commit message profile"},
		{Role: terminal.VisualRoleSuccess, Text: "Profile " + safeCMAddName(input.Name) + " added"},
	}}
}

func cmAddCollectDetail(input AddInput) string {
	return fmt.Sprintf("Profile: %s; Base URL: %s; Model: %s; API key: [redacted]", safeCMAddName(input.Name), safeCMAddURL(input.BaseURL), safeCMAddModel(input.Model))
}

func safeCMAddName(value string) string { return safeCMAddField(value, "Profile configured") }

func safeCMAddModel(value string) string { return safeCMAddField(value, "Model configured") }

func safeCMAddField(value, fallback string) string {
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

func safeCMAddURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !utf8.ValidString(trimmed) || strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return "Base URL configured"
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "Base URL configured"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return safeCMAddField(parsed.Scheme+"://"+parsed.Host+parsed.EscapedPath(), "Base URL configured")
}
