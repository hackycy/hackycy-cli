package env

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

var errExportEnvRequiresInteractive = errors.New("export env requires an interactive terminal")

const (
	exportEnvWorkCatalogID = "export-env-work"
	exportEnvSelectFormID  = "select-environment"
)

func runEnv(options *Options) error {
	if options == nil || options.WorkingDirectory == nil || options.Terminal == nil || options.Reader == nil || options.Writer == nil {
		return errors.New("export env options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalExportEnvConsoleDescriptor(options))
	if err != nil {
		return err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	work, err := terminalexperience.StartWork(run, terminalExportEnvWorkCatalog(options.Output != ""))
	if err != nil {
		return errors.Join(err, run.Finish(terminalExportEnvFinishRequest(terminalexperience.Failed, "", 0, false, options.Output != ""), nil))
	}
	adapter := newTerminalExportEnvAdapter(run, caps.Interaction == terminalexperience.Automation)
	sink := newExportEnvPhaseSink(run, work, caps, options.Output != "")
	module, err := New(Dependencies{
		WorkingDirectory: options.WorkingDirectory,
		Selector:         adapter,
		Reader:           options.Reader,
		Writer:           options.Writer,
		Presenter:        adapter,
	})
	if err != nil {
		err = errors.Join(err, sink.close())
		return errors.Join(err, run.Finish(sink.finishRequest(terminalexperience.Failed), nil))
	}
	observer := &runObserver{}
	observer.phase = sink.phase
	observer.selected = sink.selected
	observer.variables = sink.variables
	result, err := module.run(ctx, Input{
		Directory:   options.Directory,
		Environment: options.Environment,
		Merge:       options.Merge,
		Output:      options.Output,
	}, observer)
	err = errors.Join(err, sink.close())
	if result.Cancelled {
		document := terminalExportEnvResultDocument("Cancelled", terminalexperience.VisualRoleWarning)
		return errors.Join(err, run.Finish(sink.finishRequest(terminalexperience.Cancelled), &document))
	}
	if err != nil {
		return errors.Join(err, run.Finish(sink.finishRequest(terminalexperience.Failed), nil))
	}
	if options.Output != "" {
		document := terminalExportEnvResultDocument("Wrote output to "+safeExportTarget(options.Output), terminalexperience.VisualRoleSuccess)
		return run.Finish(sink.finishRequest(terminalexperience.Succeeded), &document)
	}
	// The JSON is intentionally a separate plain block so Rich styling never
	// inserts symbols or alters its durable structure.
	contents := observer.output
	document := terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleSuccess, Text: "Exported variables:"},
		{Role: terminalexperience.VisualRolePlain, Text: contents},
	}}
	return run.Finish(sink.finishRequest(terminalexperience.Succeeded), &document)
}

func terminalExportEnvConsoleDescriptor(options *Options) terminalexperience.ConsoleDescriptor {
	directory := "."
	merge := "off"
	if options != nil {
		directory = options.Directory
		if strings.TrimSpace(directory) == "" {
			directory = "."
		}
		if options.Merge {
			merge = "on"
		}
	}
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / export env",
		Target:  "environment JSON",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "directory", Value: safeExportText(directory)},
			{Label: "merge base .env", Value: merge},
		},
		FormCatalog: []terminalexperience.ConsoleFormStep{{
			ID:     exportEnvSelectFormID,
			Name:   "Select environment",
			Detail: "choose an environment file",
		}},
	}
}

type exportEnvPhaseSink struct {
	run              terminalexperience.ExperienceRun
	work             terminalexperience.WorkSession
	caps             terminalexperience.Capabilities
	withOutput       bool
	pendingVariables *terminalexperience.PresentationDocument
	variableCount    int
	hasVariableCount bool
	lastLocation     string
	closed           bool
	err              error
}

func newExportEnvPhaseSink(run terminalexperience.ExperienceRun, work terminalexperience.WorkSession, caps terminalexperience.Capabilities, withOutput bool) *exportEnvPhaseSink {
	return &exportEnvPhaseSink{run: run, work: work, caps: caps, withOutput: withOutput}
}

func (sink *exportEnvPhaseSink) phase(id, name string, state terminalPhaseState, detail string) {
	if sink.err != nil {
		return
	}
	if id == "select-environment" {
		sink.lastLocation = name
		if state == terminalPhaseActive {
			sink.notice(name, detail, terminalexperience.VisualRoleActive)
			return
		}
		role := terminalexperience.VisualRoleSuccess
		if state == terminalPhaseCancelled {
			role = terminalexperience.VisualRoleWarning
		} else if state == terminalPhaseFailed {
			role = terminalexperience.VisualRoleError
		}
		sink.milestone(terminalExportEnvPhaseDocument(name, detail, state, role))
		return
	}
	sink.lastLocation = name
	update := terminalexperience.OperationPhase{ID: id, State: exportEnvPhaseState(state), Detail: detail}
	sink.err = errors.Join(sink.err, sink.work.Update(update))
	if state != terminalPhaseActive && sink.isWorkEnd(id) {
		sink.flushVariables()
	}
}

func (sink *exportEnvPhaseSink) selected(selection Selection, source string, merge bool) {
	if sink.caps.Interaction != terminalexperience.RichInteractive || sink.err != nil {
		return
	}
	sink.milestone(terminalExportEnvSelectionDocument(selection, source, merge))
}

func (sink *exportEnvPhaseSink) variables(count int) {
	sink.variableCount = count
	sink.hasVariableCount = true
	if sink.caps.Interaction != terminalexperience.RichInteractive || sink.err != nil {
		return
	}
	document := terminalExportEnvVariableDocument(count)
	sink.pendingVariables = &document
}

func (sink *exportEnvPhaseSink) isWorkEnd(id string) bool {
	if sink.withOutput {
		return id == "write-output-file"
	}
	return id == "encode-json"
}

func (sink *exportEnvPhaseSink) flushVariables() {
	if sink.pendingVariables != nil && sink.err == nil {
		document := *sink.pendingVariables
		sink.pendingVariables = nil
		sink.err = errors.Join(sink.err, sink.run.Milestone(document))
	}
}

func (sink *exportEnvPhaseSink) notice(name, detail string, role terminalexperience.VisualRole) {
	if sink.caps.Interaction == terminalexperience.Automation {
		return
	}
	document := terminalExportEnvPhaseDocument(name, detail, terminalPhaseActive, role)
	sink.err = errors.Join(sink.err, sink.run.Notice(document))
}

func (sink *exportEnvPhaseSink) milestone(document terminalexperience.PresentationDocument) {
	if sink.caps.Interaction == terminalexperience.Automation {
		return
	}
	sink.err = errors.Join(sink.err, sink.run.Milestone(document))
}

func (sink *exportEnvPhaseSink) close() error {
	if sink.closed {
		return sink.err
	}
	sink.closed = true
	sink.err = errors.Join(sink.err, sink.work.Close())
	sink.flushVariables()
	return sink.err
}

func exportEnvPhaseState(state terminalPhaseState) terminalexperience.PhaseState {
	switch state {
	case terminalPhaseSucceeded:
		return terminalexperience.PhaseCompleted
	case terminalPhaseCancelled:
		return terminalexperience.PhaseCancelled
	case terminalPhaseFailed:
		return terminalexperience.PhaseFailed
	default:
		return terminalexperience.PhaseActive
	}
}

func terminalExportEnvPhaseDocument(name, detail string, state terminalPhaseState, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	text := name
	if detail != "" {
		text += ": " + detail
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func terminalExportEnvResultDocument(text string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func terminalExportEnvWorkCatalog(withOutput bool) terminalexperience.WorkCatalog {
	phases := []terminalexperience.PhaseDefinition{
		{ID: "resolve-directory", Name: "Resolve directory"},
		{ID: "discover-environment-files", Name: "Discover environment files"},
		{ID: "read-selected-files", Name: "Read selected files"},
		{ID: "parse-and-merge-values", Name: "Parse and merge values"},
		{ID: "encode-json", Name: "Encode JSON"},
	}
	if withOutput {
		phases = append(phases, terminalexperience.PhaseDefinition{ID: "write-output-file", Name: "Write output file"})
	}
	return terminalexperience.WorkCatalog{
		ID:     exportEnvWorkCatalogID,
		Label:  "Export environment",
		Phases: phases,
	}
}

func (sink *exportEnvPhaseSink) finishRequest(outcome terminalexperience.FinishOutcome) terminalexperience.FinishRequest {
	return terminalExportEnvFinishRequest(outcome, sink.lastLocation, sink.variableCount, sink.hasVariableCount, sink.withOutput)
}

func terminalExportEnvFinishRequest(outcome terminalexperience.FinishOutcome, location string, variableCount int, hasVariableCount, withOutput bool) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminalexperience.Succeeded:
		if withOutput {
			request.Summary = terminalExportEnvResultDocument("Environment export written", terminalexperience.VisualRoleSuccess)
			return request
		}
		if hasVariableCount {
			request.Summary = terminalExportEnvVariableDocument(variableCount)
			return request
		}
		request.Summary = terminalExportEnvResultDocument("Environment export complete", terminalexperience.VisualRoleSuccess)
	case terminalexperience.Cancelled:
		if location != "" {
			request.Location = safeExportText(location)
		}
		request.Summary = terminalExportEnvResultDocument("Environment export cancelled", terminalexperience.VisualRoleWarning)
	case terminalexperience.Failed:
		if location != "" {
			request.Location = safeExportText(location)
		}
		request.Summary = terminalExportEnvResultDocument("Unable to export environment", terminalexperience.VisualRoleError)
	}
	return request
}

func terminalExportEnvSelectionDocument(selection Selection, source string, merge bool) terminalexperience.PresentationDocument {
	files := make([]string, 0, len(selection.Files))
	for _, file := range selection.Files {
		files = append(files, safeExportText(filepath.ToSlash(file)))
	}
	mergeValue := "off"
	if merge {
		mergeValue = "on"
	}
	selected := "environment"
	if len(selection.Files) > 0 {
		selected = environmentLabel(selection.Files[len(selection.Files)-1])
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleSuccess, Text: "Selected environment: " + safeExportText(selected) + " (" + safeExportText(source) + ")"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Files: " + strings.Join(files, ", ") + "  Merge: " + mergeValue},
	}}
}

func terminalExportEnvVariableDocument(count int) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleSuccess,
		Text: fmt.Sprintf("Exported %d variable%s", count, pluralSuffix(count)),
	}}}
}

func safeExportTarget(value string) string {
	value = safeExportText(value)
	if filepath.IsAbs(value) {
		return filepath.Base(filepath.Clean(value))
	}
	return value
}

func safeExportText(value string) string {
	if !utf8.ValidString(value) {
		return "configured path"
	}
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			return "configured path"
		}
		builder.WriteRune(r)
	}
	value = strings.TrimSpace(builder.String())
	if value == "" {
		return "."
	}
	runes := []rune(value)
	if len(runes) > 160 {
		return fmt.Sprintf("%s...", string(runes[:160]))
	}
	return value
}

type terminalExportEnvAdapter struct {
	run        terminalexperience.ExperienceRun
	automation bool
}

func newTerminalExportEnvAdapter(run terminalexperience.ExperienceRun, automation bool) *terminalExportEnvAdapter {
	return &terminalExportEnvAdapter{run: run, automation: automation}
}

func (adapter *terminalExportEnvAdapter) SelectEnvironment(message string, choices []EnvironmentChoice) (string, bool, error) {
	if adapter.automation && len(choices) == 1 {
		return choices[0].Value, false, nil
	}
	answer, err := adapter.run.Ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionSelect,
		Message:         message,
		Options:         exportEnvInteractionOptions(choices),
		CancelValues:    []string{"", "q", "quit", "cancel"},
		ConsoleStepID:   exportEnvSelectFormID,
		TranscriptLabel: "Selected environment",
	})
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) || errors.Is(err, context.Canceled) {
		return "", true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return "", false, errExportEnvRequiresInteractive
	}
	if err != nil {
		return "", false, err
	}
	return answer.Value, false, nil
}

func (adapter *terminalExportEnvAdapter) Outro(message string) {
	_ = adapter.run.Result(terminalExportEnvDocument(message, terminalexperience.VisualRoleMuted))
}

func (adapter *terminalExportEnvAdapter) Print(value string) {
	_ = adapter.run.Result(terminalExportEnvDocument(value, terminalexperience.VisualRolePlain))
}

func (adapter *terminalExportEnvAdapter) Cancel(message string) {
	_ = adapter.run.Result(terminalExportEnvDocument(message, terminalexperience.VisualRoleWarning))
}

func exportEnvInteractionOptions(choices []EnvironmentChoice) []terminalexperience.InteractionOption {
	options := make([]terminalexperience.InteractionOption, 0, len(choices))
	for _, choice := range choices {
		options = append(options, terminalexperience.InteractionOption{Label: choice.Label, Value: choice.Value, Description: choice.Value})
	}
	return options
}

func terminalExportEnvDocument(text string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}
