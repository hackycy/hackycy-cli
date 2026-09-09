package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

var errRichUnavailable = errors.New("rich terminal is unavailable")

const outcomeDwell = 800 * time.Millisecond

type richController struct {
	runtime *Runtime
	console ConsoleDescriptor
	model   *richRootModel
	program *tea.Program
	lease   *RendererLease
	output  *rendererTerminalWriter

	done chan struct{}
	mu   sync.Mutex
	err  error
	next uint64
}

func newRichController(runtime *Runtime, console ConsoleDescriptor) *richController {
	return &richController{runtime: runtime, console: console, done: make(chan struct{})}
}

func (controller *richController) start() error {
	if controller.runtime.inputTerminal == nil || controller.runtime.diagnosticTerminal == nil {
		return errRichUnavailable
	}
	width, height, err := term.GetSize(int(controller.runtime.diagnosticTerminal.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		return errRichUnavailable
	}

	controller.lease = controller.runtime.diagnostics.AcquireRendererLease()
	controller.model = newRichRootModelWithConsole(
		width,
		height,
		controller.runtime.capabilities.Stderr.Color,
		controller.console,
	)
	controller.output = &rendererTerminalWriter{
		writer:   controller.lease.Writer(),
		terminal: controller.runtime.diagnosticTerminal,
	}
	controller.program = tea.NewProgram(
		controller.model,
		tea.WithInput(controller.runtime.inputTerminal),
		// Keep the root's writes inside the renderer lease.  This makes the
		// renderer and the semantic replay share one serialized terminal owner.
		tea.WithOutput(controller.output),
		tea.WithoutSignalHandler(),
	)
	go func() {
		_, runErr := controller.program.Run()
		controller.mu.Lock()
		controller.err = runErr
		controller.mu.Unlock()
		close(controller.done)
	}()

	ack := make(chan struct{})
	if err := controller.send(richReadyMsg{ack: ack}, ack); err != nil {
		controller.program.Quit()
		<-controller.done
		return errors.Join(err, controller.programErrorOrNil(), controller.releaseLease())
	}
	return nil
}

// rendererTerminalWriter preserves Bubble Tea's terminal capability detection
// while routing every renderer write through the active diagnostic lease.
//
// Bubble Tea v2 queues DEC mode probes in a separate output buffer. A finite
// command can complete before that buffer is flushed, which causes the probe
// to reach the terminal only after input has been restored to the caller. Any
// terminal response then becomes shell input. Synchronized-output and Unicode
// mode probing are optional renderer optimizations, so finite Experience runs
// suppress those asynchronous probes while leaving normal ANSI rendering
// untouched.
type rendererTerminalWriter struct {
	writer   io.Writer
	terminal *os.File

	mu      sync.Mutex
	pending []byte
}

var rendererCapabilityQueries = [][]byte{
	[]byte("\x1b[?2026$p"),
	[]byte("\x1b[?2027$p"),
}

func (writer *rendererTerminalWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()

	writer.pending = append(writer.pending, value...)
	filtered, pending := filterRendererCapabilityQueries(writer.pending, false)
	writer.pending = append([]byte(nil), pending...)
	if err := writeRendererBytes(writer.writer, filtered); err != nil {
		return 0, err
	}
	return len(value), nil
}

func (writer *rendererTerminalWriter) Read(value []byte) (int, error) {
	return writer.terminal.Read(value)
}

// Close deliberately leaves the inherited diagnostic terminal open. Bubble Tea
// only needs the terminal-file shape for capability detection and sizing.
func (*rendererTerminalWriter) Close() error {
	return nil
}

func (writer *rendererTerminalWriter) Fd() uintptr {
	return writer.terminal.Fd()
}

func (writer *rendererTerminalWriter) Flush() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	filtered, _ := filterRendererCapabilityQueries(writer.pending, true)
	writer.pending = nil
	return writeRendererBytes(writer.writer, filtered)
}

func filterRendererCapabilityQueries(value []byte, final bool) (filtered, pending []byte) {
	filtered = make([]byte, 0, len(value))
	for len(value) > 0 {
		matched := false
		partial := false
		for _, query := range rendererCapabilityQueries {
			switch {
			case bytes.HasPrefix(value, query):
				value = value[len(query):]
				matched = true
			case !final && len(value) < len(query) && bytes.HasPrefix(query, value):
				partial = true
			}
			if matched || partial {
				break
			}
		}
		if matched {
			continue
		}
		if partial {
			return filtered, value
		}
		filtered = append(filtered, value[0])
		value = value[1:]
	}
	return filtered, nil
}

func writeRendererBytes(destination io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := destination.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func (controller *richController) ask(ctx context.Context, handler *InteractionHandler, request InteractionRequest) (InteractionAnswer, error) {
	if err := validateInteractionRequest(request); err != nil {
		return InteractionAnswer{}, err
	}
	controller.next++
	id := controller.next
	form, answer, err := newRichForm(handler, request, id)
	if err != nil {
		return InteractionAnswer{}, err
	}

	response := make(chan richAskResult, 1)
	ack := make(chan struct{})
	if err := controller.send(richShowFormMsg{
		id:       id,
		form:     form,
		answer:   answer,
		step:     newConsoleFormStep(id, request),
		response: response,
		ack:      ack,
	}, ack); err != nil {
		return InteractionAnswer{}, err
	}

	select {
	case result := <-response:
		return result.answer, result.err
	case <-ctx.Done():
		cancelAck := make(chan struct{})
		_ = controller.send(richCancelFormMsg{id: id, err: ctx.Err(), ack: cancelAck}, cancelAck)
		return InteractionAnswer{}, ctx.Err()
	case <-controller.done:
		return InteractionAnswer{}, controller.programError()
	}
}

func (controller *richController) notice(document PresentationDocument) error {
	ack := make(chan struct{})
	return controller.send(richNoticeMsg{document: document, ack: ack}, ack)
}

func (controller *richController) milestone(document PresentationDocument) error {
	ack := make(chan struct{})
	return controller.send(richMilestoneMsg{document: document, ack: ack}, ack)
}

func (controller *richController) startTrack(label string, phases []OperationPhase, requestCancel func() error) error {
	return controller.startTrackWithFormCatalog(label, phases, requestCancel, false)
}

func (controller *richController) startWork(label string, phases []OperationPhase, requestCancel func() error) error {
	return controller.startTrackWithFormCatalog(label, phases, requestCancel, true)
}

func (controller *richController) startTrackWithFormCatalog(label string, phases []OperationPhase, requestCancel func() error, retainFormCatalog bool) error {
	ack := make(chan struct{})
	return controller.send(richStartTrackMsg{label: label, phases: phases, requestCancel: requestCancel, retainFormCatalog: retainFormCatalog, ack: ack}, ack)
}

func (controller *richController) updateTrack(phase OperationPhase) error {
	ack := make(chan struct{})
	return controller.send(richTrackPhaseMsg{phase: phase, ack: ack}, ack)
}

func (controller *richController) cancelTrack() error {
	ack := make(chan struct{})
	return controller.send(richCancelTrackMsg{ack: ack}, ack)
}

func (controller *richController) finishTrack() error {
	ack := make(chan struct{})
	return controller.send(richFinishTrackMsg{ack: ack}, ack)
}

func (controller *richController) outcome(ctx context.Context, request FinishRequest) error {
	ack := make(chan struct{})
	if err := controller.send(richShowOutcomeMsg{request: request, ack: ack}, ack); err != nil {
		return err
	}
	select {
	case <-controller.done:
		return controller.programErrorOrNil()
	case <-ctx.Done():
		// Cancellation is allowed to interrupt the fixed outcome dwell. Quit the
		// renderer here so stopRich can still perform the normal restoration and
		// transcript replay path.
		controller.program.Quit()
		<-controller.done
		return controller.programErrorOrNil()
	}
}

func (controller *richController) send(message tea.Msg, ack <-chan struct{}) error {
	select {
	case <-controller.done:
		return controller.programError()
	default:
	}
	controller.program.Send(message)
	select {
	case <-ack:
		return nil
	case <-controller.done:
		return controller.programError()
	}
}

func (controller *richController) close(ledger *TranscriptLedger) error {
	return controller.closeWith(ledger, true)
}

// closeAfterFailure performs the same terminal restoration and replay as a
// normal close, but leaves the already-returned renderer error to the caller so
// it is not reported twice.
func (controller *richController) closeAfterFailure(ledger *TranscriptLedger) error {
	return controller.closeWith(ledger, false)
}

func (controller *richController) closeWith(ledger *TranscriptLedger, includeProgramError bool) error {
	if controller.program != nil {
		controller.program.Quit()
		<-controller.done
	}
	var rendererFlushErr error
	if controller.output != nil {
		rendererFlushErr = controller.output.Flush()
	}

	// Bubble Tea has restored the primary screen at this point, while the
	// lease still owns stderr.  Replay only the bounded semantic ledger; raw
	// frames and command results are deliberately not copied here.
	var replayErr error
	if ledger != nil {
		if transcript := renderRichTranscript(ledger, controller.runtime.capabilities.Stderr.Color); transcript != "" && controller.lease != nil {
			_, replayErr = io.WriteString(controller.lease.Writer(), transcript)
		}
	}
	var programErr error
	if includeProgramError {
		programErr = controller.programErrorOrNil()
	}
	return errors.Join(programErr, rendererFlushErr, replayErr, controller.releaseLease())
}

func (controller *richController) releaseLease() error {
	if controller.lease == nil {
		return nil
	}
	lease := controller.lease
	controller.lease = nil
	return lease.Close()
}

func (controller *richController) programError() error {
	if err := controller.programErrorOrNil(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

func (controller *richController) programErrorOrNil() error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.err
}

func (controller *richController) stopped() bool {
	if controller == nil || controller.done == nil {
		return false
	}
	select {
	case <-controller.done:
		return true
	default:
		return false
	}
}

type richMode uint8

const (
	richNoticeMode richMode = iota
	richFormMode
	richTrackMode
	richOutcomeMode
)

type richRootModel struct {
	width   int
	height  int
	color   bool
	console ConsoleDescriptor
	mode    richMode

	notices          []PresentationDocument
	formID           uint64
	form             richFormModel
	answer           func() InteractionAnswer
	response         chan<- richAskResult
	track            *trackedState
	trackRetainsForm bool
	outcome          FinishRequest
	formRows         []consoleFormStep
	statusRows       []consoleStatusRow
	trackRowStart    int
	trackRowsSynced  bool
}

func newRichRootModel(width, height int, color bool) *richRootModel {
	return newRichRootModelWithConsole(width, height, color, defaultConsoleDescriptor())
}

func newRichRootModelWithConsole(width, height int, color bool, console ConsoleDescriptor) *richRootModel {
	model := &richRootModel{
		width:   width,
		height:  height,
		color:   color,
		console: console,
	}
	model.initializeFormCatalog()
	return model
}

// initializeFormCatalog materializes the complete pre-work table before any
// interaction message arrives. The first step is the current active region;
// all later steps remain visible as pending rows.
func (model *richRootModel) initializeFormCatalog() {
	if len(model.console.FormCatalog) == 0 {
		return
	}
	model.formRows = make([]consoleFormStep, 0, len(model.console.FormCatalog))
	model.statusRows = make([]consoleStatusRow, 0, len(model.console.FormCatalog))
	for index, catalogStep := range model.console.FormCatalog {
		state := PhasePending
		if index == 0 {
			state = PhaseActive
		}
		detail := catalogStep.Detail
		if catalogStep.Sensitive {
			detail = "[redacted]"
		}
		step := consoleFormStep{
			catalogID: catalogStep.ID,
			name:      catalogStep.Name,
			detail:    detail,
			state:     state,
			row:       index,
		}
		model.formRows = append(model.formRows, step)
		model.statusRows = append(model.statusRows, consoleStatusRow{
			state:  state,
			phase:  step.name,
			detail: step.detail,
		})
	}
}

func (*richRootModel) Init() tea.Cmd {
	return nil
}

func (model *richRootModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case richReadyMsg:
		close(value.ack)
		return model, nil
	case tea.WindowSizeMsg:
		model.width = max(value.Width, 1)
		model.height = max(value.Height, 1)
		if model.form != nil {
			model.configureForm()
			updated, cmd := model.form.Update(tea.WindowSizeMsg{Width: model.width, Height: model.formHeight()})
			model.form = updated.(richFormModel)
			return model, cmd
		}
		return model, nil
	case richNoticeMsg:
		if model.mode == richOutcomeMode {
			close(value.ack)
			return model, nil
		}
		if model.track == nil || !model.trackRetainsForm {
			model.preserveTrack()
			model.mode = richNoticeMode
		}
		if len(value.document.Blocks) > 0 {
			model.notices = append(model.notices, value.document)
		}
		close(value.ack)
		return model, nil
	case richMilestoneMsg:
		if model.mode == richOutcomeMode {
			close(value.ack)
			return model, nil
		}
		if model.track == nil || !model.trackRetainsForm {
			model.preserveTrack()
			model.mode = richNoticeMode
		}
		if len(value.document.Blocks) > 0 {
			model.notices = append(model.notices, value.document)
		}
		close(value.ack)
		return model, nil
	case richShowFormMsg:
		if model.mode == richOutcomeMode {
			close(value.ack)
			return model, nil
		}
		if model.track != nil && model.trackRetainsForm {
			model.showFormCatalog()
		} else {
			model.preserveTrack()
		}
		model.mode = richFormMode
		model.formID = value.id
		model.form = value.form
		model.answer = value.answer
		model.response = value.response
		step := value.step
		if row := model.formCatalogRow(step.catalogID); row >= 0 {
			step.row = row
			model.formRows[row].id = step.id
			model.formRows[row].state = PhaseActive
			model.formRows[row].detail = step.detail
			model.statusRows[row] = consoleStatusRow{
				state:  PhaseActive,
				phase:  model.formRows[row].name,
				detail: step.detail,
			}
		}
		model.configureForm()
		close(value.ack)
		return model, model.form.Init()
	case richFormSubmittedMsg:
		if value.id == model.formID && model.form != nil {
			model.finishFormRow(value.id, PhaseCompleted, "answer captured")
			model.response <- richAskResult{answer: model.answer()}
			model.clearForm()
		}
		return model, nil
	case richFormCancelledMsg:
		if value.id == model.formID && model.form != nil {
			model.finishFormRow(value.id, PhaseCancelled, "cancelled")
			model.response <- richAskResult{err: ErrInteractionCancelled}
			model.clearForm()
		}
		return model, nil
	case richCancelFormMsg:
		if value.id == model.formID && model.form != nil {
			model.finishFormRow(value.id, PhaseCancelled, "cancelled")
			model.response <- richAskResult{err: value.err}
			model.clearForm()
		}
		close(value.ack)
		return model, nil
	case richStartTrackMsg:
		if model.mode == richOutcomeMode {
			close(value.ack)
			return model, nil
		}
		// Work is a replacement phase, not an append-only continuation of the
		// form. Ordinary Tracks clear Form rows; a controlled Work session keeps
		// its declared Form Catalog off-screen so the two catalog kinds never
		// mix and a later declared interaction can restore its own rows.
		if value.retainFormCatalog {
			model.form = nil
			model.formID = 0
			model.answer = nil
			model.response = nil
			model.statusRows = nil
			model.trackRowStart = 0
		} else if len(model.formRows) > 0 {
			model.track = nil
			model.form = nil
			model.formRows = nil
			model.statusRows = nil
			model.trackRowStart = 0
		} else {
			model.preserveTrack()
		}
		model.trackRowsSynced = true
		model.trackRetainsForm = value.retainFormCatalog
		model.mode = richTrackMode
		model.track = &trackedState{
			label:  value.label,
			phases: append([]OperationPhase(nil), value.phases...),
			requestStop: func() {
				_ = value.requestCancel()
			},
		}
		model.trackRowStart = len(model.statusRows)
		model.syncTrackRows()
		close(value.ack)
		return model, nil
	case richTrackPhaseMsg:
		if model.mode == richOutcomeMode {
			close(value.ack)
			return model, nil
		}
		if model.track != nil {
			model.track.applyPhase(value.phase)
			if model.mode == richTrackMode {
				model.syncTrackRows()
			}
		}
		close(value.ack)
		return model, nil
	case richCancelTrackMsg:
		if model.mode == richOutcomeMode {
			close(value.ack)
			return model, nil
		}
		if model.track != nil {
			model.track.requestCancellation()
		}
		close(value.ack)
		return model, nil
	case richFinishTrackMsg:
		if model.mode == richOutcomeMode {
			close(value.ack)
			return model, nil
		}
		if model.track != nil {
			model.track.cancelArmed = false
		}
		close(value.ack)
		return model, nil
	case richShowOutcomeMsg:
		if model.mode == richOutcomeMode {
			close(value.ack)
			return model, nil
		}
		// Preserve the final Work rows in the stable table before dropping the
		// mutable tracker. Outcome is a terminal projection, so later form/work
		// messages are acknowledged but cannot replace it.
		if model.track != nil {
			if model.trackRetainsForm {
				model.showWorkCatalog()
			} else {
				model.syncTrackRows()
			}
		}
		model.track = nil
		model.trackRetainsForm = false
		model.form = nil
		model.formID = 0
		model.answer = nil
		model.response = nil
		model.outcome = value.request
		model.mode = richOutcomeMode
		close(value.ack)
		return model, tea.Tick(outcomeDwell, func(time.Time) tea.Msg {
			return richOutcomeElapsedMsg{}
		})
	case richOutcomeElapsedMsg:
		if model.mode == richOutcomeMode {
			return model, tea.Quit
		}
		return model, nil
	case tea.KeyPressMsg:
		if model.mode == richTrackMode && model.track != nil {
			switch value.String() {
			case "ctrl+c":
				model.track.requestCancellation()
			case "esc":
				if model.track.cancelArmed {
					model.track.requestCancellation()
				} else {
					model.track.cancelArmed = true
				}
			default:
				model.track.cancelArmed = false
			}
			return model, nil
		}
		if model.mode == richFormMode && model.form != nil && value.String() == "esc" {
			if model.form.handlesEscape() {
				updated, command := model.form.Update(message)
				model.form = updated.(richFormModel)
				return model, command
			}
			model.finishFormRow(model.formID, PhaseCancelled, "cancelled")
			model.response <- richAskResult{err: ErrInteractionCancelled}
			model.clearForm()
			return model, nil
		}
	}

	if model.mode == richFormMode && model.form != nil {
		updated, cmd := model.form.Update(message)
		model.form = updated.(richFormModel)
		return model, cmd
	}
	return model, nil
}

func (model *richRootModel) View() tea.View {
	if model.width <= 0 || model.height <= 0 {
		return tea.View{AltScreen: true, DisableBracketedPasteMode: true}
	}
	var content string
	if model.consoleWideLayout() {
		content = model.consoleWideView()
	} else {
		content = model.consoleCompactView()
	}
	return tea.View{
		Content:                   takeFirstLines(content, model.height),
		AltScreen:                 true,
		DisableBracketedPasteMode: true,
	}
}

// consoleWideLayout is the production B threshold. The prototype keeps the
// complete table and active region at 70x20 and above; smaller surfaces use
// the compact slice until its dedicated renderer is installed.
func (model *richRootModel) consoleWideLayout() bool {
	return model.width >= 70 && model.height >= 20
}

func (model *richRootModel) consoleCompactView() string {
	styles := richStyles(model.color)
	inner := max(model.width-4, 1)
	parts := []string{
		model.consoleCompactBar(styles, inner),
	}
	if metadata := model.consoleMetadataView(styles, inner); metadata != "" {
		parts = append(parts, metadata)
	}
	parts = append(parts, styles[VisualRoleTitle].Render("STATE / PHASE / DETAIL"))
	for _, row := range model.consoleRows() {
		parts = append(parts, model.consoleCompactRow(row, inner, styles))
	}
	if active := model.consoleActiveView(inner); active != "" {
		parts = append(parts, "", takeFirstLines(active, model.consoleCompactActiveHeight()))
	}
	return lipgloss.NewStyle().Width(inner).Padding(1, 2).Render(strings.Join(parts, "\n"))
}

func (model *richRootModel) consoleCompactBar(styles map[VisualRole]lipgloss.Style, width int) string {
	command := model.consoleCommand()
	if command == "" {
		command = "YCY"
	}
	target := model.console.Target
	if target == "" {
		target = "terminal session"
	}
	bar := styles[VisualRoleActive].Render(stripTerminalControl(command)) +
		styles[VisualRoleMuted].Render(" · ") +
		styles[VisualRolePlain].Render(stripTerminalControl(target)) +
		styles[VisualRoleMuted].Render(" · ") +
		styles[VisualRoleActive].Render(model.consoleStatusLabel())
	return consoleTruncate(bar, width)
}

func (model *richRootModel) consoleCompactRow(row consoleStatusRow, width int, styles map[VisualRole]lipgloss.Style) string {
	glyph, label := consoleStateLabel(row.state)
	state := glyph + " " + label
	phase := stripTerminalControl(row.phase)
	detail := stripTerminalControl(row.detail)
	line := state + " · " + phase
	if detail != "" {
		line += " · " + detail
	}
	line = wrapText(line, width)
	return styles[consoleStateRole(row.state)].Render(line)
}

func (model *richRootModel) consoleCompactActiveHeight() int {
	reserved := 5 + len(model.consoleRows())
	return max(model.height-reserved-model.consoleContextHeight(model.formWidth()), 1)
}

func (model *richRootModel) consoleWideView() string {
	styles := richStyles(model.color)
	inner := max(model.width-6, 1)
	status := model.consoleStatusView(inner, styles)
	active := model.consoleActiveView(inner)
	parts := []string{
		model.consoleBar(styles, inner),
		model.consoleMetadataView(styles, inner),
		styles[VisualRoleMuted].Render(strings.Repeat("─", inner)),
		status,
	}
	if active != "" {
		parts = append(parts, "", takeFirstLines(active, model.consoleActiveHeight()))
	}
	return lipgloss.NewStyle().Padding(1, 3).Render(strings.Join(parts, "\n"))
}

func (model *richRootModel) consoleBar(styles map[VisualRole]lipgloss.Style, width int) string {
	command := model.consoleCommand()
	if command == "" {
		command = "YCY"
	}
	target := model.console.Target
	if target == "" {
		target = "terminal session"
	}
	bar := styles[VisualRoleActive].Render(stripTerminalControl(command)) +
		styles[VisualRoleMuted].Render("  |  ") +
		styles[VisualRolePlain].Render(stripTerminalControl(target)) +
		styles[VisualRoleMuted].Render("  |  ") +
		styles[VisualRoleActive].Render(model.consoleStatusLabel())
	return consoleTruncate(bar, width)
}

func (model *richRootModel) consoleMetadataView(styles map[VisualRole]lipgloss.Style, width int) string {
	fields := make([]string, 0, len(model.console.Metadata))
	for _, field := range model.console.Metadata {
		label := stripTerminalControl(field.Label)
		value := stripTerminalControl(field.Value)
		if label == "" || value == "" {
			continue
		}
		fields = append(fields, styles[VisualRoleMuted].Render(label+" ")+styles[VisualRolePlain].Render(value))
	}
	if len(fields) == 0 {
		return ""
	}
	separator := styles[VisualRoleMuted].Render("    ")
	return consoleTruncate(strings.Join(fields, separator), width)
}

func (model *richRootModel) consoleCommand() string {
	return model.console.Command
}

func (model *richRootModel) consoleStatusLabel() string {
	switch model.mode {
	case richFormMode, richTrackMode:
		return "ACTIVE"
	case richOutcomeMode:
		return strings.ToUpper(model.outcome.Outcome.String())
	}
	if model.console.Status != "" {
		return strings.ToUpper(stripTerminalControl(model.console.Status))
	}
	return "READY"
}

type consoleStatusRow struct {
	state  PhaseState
	phase  string
	detail string
}

type consoleFormStep struct {
	id        uint64
	catalogID string
	name      string
	detail    string
	state     PhaseState
	row       int
}

func newConsoleFormStep(id uint64, request InteractionRequest) consoleFormStep {
	detail := "text input"
	switch request.Kind {
	case InteractionSecret:
		detail = "redacted input"
	case InteractionSelect:
		detail = "single selection"
	case InteractionMultiSelect:
		detail = "multiple selection"
	case InteractionConfirm:
		detail = "confirmation"
	}
	if request.Sensitive {
		detail = "redacted input"
	}
	return consoleFormStep{id: id, catalogID: strings.TrimSpace(stripTerminalControl(request.ConsoleStepID)), detail: detail, state: PhaseActive}
}

func (model *richRootModel) formCatalogRow(catalogID string) int {
	catalogID = strings.TrimSpace(stripTerminalControl(catalogID))
	if catalogID == "" {
		return -1
	}
	for index, step := range model.formRows {
		if step.catalogID == catalogID {
			return index
		}
	}
	return -1
}

func (model *richRootModel) finishFormRow(id uint64, state PhaseState, detail string) {
	for index := len(model.formRows) - 1; index >= 0; index-- {
		if model.formRows[index].id == id {
			model.formRows[index].state = state
			model.formRows[index].detail = detail
			row := model.formRows[index].row
			if row >= 0 && row < len(model.statusRows) {
				model.statusRows[row].state = state
				model.statusRows[row].detail = detail
			}
			return
		}
	}
}

func (model *richRootModel) consoleRows() []consoleStatusRow {
	rows := append([]consoleStatusRow(nil), model.statusRows...)
	if model.mode == richTrackMode && model.track != nil && !model.trackRowsSynced {
		for _, phase := range model.track.phases {
			rows = append(rows, consoleStatusRow{state: phase.State, phase: phase.Name, detail: phase.Detail})
		}
	}
	if model.mode == richTrackMode && model.track != nil && len(model.track.phases) == 0 {
		return append(rows, consoleStatusRow{state: PhaseActive, phase: model.trackLabel(), detail: "working"})
	}
	if len(rows) > 0 {
		return rows
	}
	if len(model.notices) > 0 {
		return []consoleStatusRow{{state: PhaseActive, phase: "Context", detail: "active command context"}}
	}
	return []consoleStatusRow{{state: PhasePending, phase: "Ready", detail: "awaiting command input"}}
}

// syncTrackRows keeps the one ordered Console table as the visible projection
// of the active Track catalog. A completed Track stays in that table when the
// next form or Notice replaces the active region.
func (model *richRootModel) syncTrackRows() {
	if model.track == nil {
		return
	}
	for index, phase := range model.track.phases {
		row := consoleStatusRow{state: phase.State, phase: phase.Name, detail: phase.Detail}
		rowIndex := model.trackRowStart + index
		if rowIndex < len(model.statusRows) {
			model.statusRows[rowIndex] = row
			continue
		}
		model.statusRows = append(model.statusRows, row)
	}
}

func (model *richRootModel) showFormCatalog() {
	model.statusRows = make([]consoleStatusRow, 0, len(model.formRows))
	for _, step := range model.formRows {
		model.statusRows = append(model.statusRows, consoleStatusRow{
			state:  step.state,
			phase:  step.name,
			detail: step.detail,
		})
	}
}

func (model *richRootModel) showWorkCatalog() {
	model.statusRows = nil
	model.trackRowStart = 0
	model.trackRowsSynced = true
	model.syncTrackRows()
}

func (model *richRootModel) trackLabel() string {
	if model.track == nil || strings.TrimSpace(model.track.label) == "" {
		return "Work"
	}
	return model.track.label
}

func (model *richRootModel) consoleStatusView(width int, styles map[VisualRole]lipgloss.Style) string {
	const stateColumnWidth = 12
	phaseColumnWidth := min(26, max(width-stateColumnWidth, 1))
	detailColumnWidth := max(width-stateColumnWidth-phaseColumnWidth, 0)
	rows := []string{
		styles[VisualRoleTitle].Render(consolePad("STATE", stateColumnWidth) + consolePad("PHASE", phaseColumnWidth) + "DETAIL"),
	}
	for _, row := range model.consoleRows() {
		state, label := consoleStateLabel(row.state)
		stateText := styles[consoleStateRole(row.state)].Render(consolePad(consoleTruncate(state+" "+label, stateColumnWidth), stateColumnWidth))
		phaseText := styles[VisualRolePlain].Render(consolePad(consoleTruncate(stripTerminalControl(row.phase), phaseColumnWidth), phaseColumnWidth))
		detail := styles[VisualRoleMuted].Render(consoleTruncate(stripTerminalControl(row.detail), detailColumnWidth))
		rows = append(rows, stateText+phaseText+detail)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(rows, "\n"))
}

func (model *richRootModel) consoleActiveView(width int) string {
	context := model.consoleNoticeContext(width)
	var active string
	switch model.mode {
	case richFormMode:
		if model.form != nil {
			active = model.form.View().Content
		}
	case richTrackMode:
		if model.track != nil {
			phase := model.track.currentPhase()
			if phase.Name == "" {
				phase.Name = model.trackLabel()
			}
			parts := make([]string, 0, 4)
			if label := stripTerminalControl(model.trackLabel()); label != "" && label != phase.Name {
				parts = append(parts, label)
			}
			parts = append(parts, stripTerminalControl(phase.Name))
			if detail := stripTerminalControl(phase.Detail); detail != "" {
				parts = append(parts, detail)
			}
			if model.track.cancelArmed && !model.track.cancellationState {
				parts = append(parts, "Press Esc again to cancel")
			}
			if model.track.cancellationState {
				parts = append(parts, "Cancelling...")
			}
			active = wrapText(strings.Join(parts, "\n"), width)
		}
	case richOutcomeMode:
		active = model.consoleOutcomeView(width)
	}
	if model.mode == richOutcomeMode {
		return active
	}
	if active == "" {
		return context
	}
	if context == "" {
		return active
	}
	return context + "\n" + active
}

func (model *richRootModel) consoleOutcomeView(width int) string {
	styles := richStyles(model.color)
	role := VisualRoleSuccess
	glyph := "✓"
	switch model.outcome.Outcome {
	case Cancelled:
		role = VisualRoleWarning
		glyph = "⊘"
	case Failed:
		role = VisualRoleError
		glyph = "✕"
	}
	parts := []string{styles[role].Render(glyph + " " + strings.ToUpper(model.outcome.Outcome.String()))}
	if location := stripTerminalControl(model.outcome.Location); location != "" {
		parts = append(parts, styles[VisualRoleMuted].Render("Location: ")+styles[VisualRolePlain].Render(location))
	}
	if len(model.outcome.Summary.Blocks) > 0 {
		rendered := strings.TrimSuffix(renderRich(model.outcome.Summary, RichOptions{
			Width: width,
			Color: model.color,
		}), "\n")
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, "\n")
}

func (model *richRootModel) consoleActiveHeight() int {
	// One line each for the bar, metadata, divider, table heading, and the
	// separator before the active region, plus outer vertical padding.
	reserved := 7 + len(model.consoleRows()) + model.consoleContextHeight(model.formWidth())
	return max(model.height-reserved, 1)
}

func consolePad(value string, width int) string {
	missing := width - lipgloss.Width(value)
	if missing <= 0 {
		return value
	}
	return value + strings.Repeat(" ", missing)
}

func consoleTruncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func consoleStateLabel(state PhaseState) (string, string) {
	switch state {
	case PhaseActive:
		return "◆", "ACTIVE"
	case PhaseCompleted:
		return "✓", "DONE"
	case PhaseCancelled:
		return "⊘", "CANCELLED"
	case PhaseFailed:
		return "✕", "FAILED"
	default:
		return "○", "PENDING"
	}
}

func consoleStateRole(state PhaseState) VisualRole {
	switch state {
	case PhaseCompleted:
		return VisualRoleSuccess
	case PhaseCancelled:
		return VisualRoleWarning
	case PhaseFailed:
		return VisualRoleError
	case PhaseActive:
		return VisualRoleActive
	default:
		return VisualRoleMuted
	}
}

func (model *richRootModel) configureForm() {
	if model.form == nil {
		return
	}
	model.form.configure(model.formWidth(), model.formHeight(), !model.compact())
}

func (model *richRootModel) compact() bool {
	return !model.consoleWideLayout()
}

func (model *richRootModel) formWidth() int {
	if model.consoleWideLayout() {
		return max(model.width-6, 1)
	}
	return max(model.width-4, 1)
}

func (model *richRootModel) formHeight() int {
	if model.consoleWideLayout() {
		return max(model.consoleActiveHeight(), 5)
	}
	// The root clips the whole compact view to the physical terminal. Huh still
	// needs enough internal rows to keep filtering and long-list navigation
	// usable while that small surface is being resized.
	return max(model.consoleCompactActiveHeight(), 7)
}

// consoleNoticeContext keeps only the latest non-empty Notice as transient
// active-region context. It cannot grow into a second header or push the B
// status table out of the view.
func (model *richRootModel) consoleNoticeContext(width int) string {
	if width <= 0 {
		return ""
	}
	for index := len(model.notices) - 1; index >= 0; index-- {
		rendered := strings.TrimSuffix(renderRich(model.notices[index], RichOptions{
			Width: width,
			Color: model.color,
		}), "\n")
		if rendered == "" {
			continue
		}
		return takeFirstLines(rendered, min(max(model.height/3, 1), 4))
	}
	return ""
}

func (model *richRootModel) consoleContextHeight(width int) int {
	context := model.consoleNoticeContext(width)
	if context == "" {
		return 0
	}
	return lineCount(context)
}

func lineCount(value string) int {
	if value == "" {
		return 0
	}
	return len(strings.Split(value, "\n"))
}

func (model *richRootModel) preserveTrack() {
	model.track = nil
	model.trackRetainsForm = false
	model.trackRowStart = len(model.statusRows)
	model.trackRowsSynced = false
}

func (model *richRootModel) clearForm() {
	if model.track != nil && model.trackRetainsForm {
		model.mode = richTrackMode
		model.formID = 0
		model.form = nil
		model.answer = nil
		model.response = nil
		model.showWorkCatalog()
		return
	}
	model.mode = richNoticeMode
	model.formID = 0
	model.form = nil
	model.answer = nil
	model.response = nil
}

func takeFirstLines(value string, count int) string {
	if count <= 0 || value == "" {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) > count {
		lines = lines[:count]
	}
	return strings.Join(lines, "\n")
}

type richReadyMsg struct{ ack chan struct{} }

type richNoticeMsg struct {
	document PresentationDocument
	ack      chan struct{}
}

type richMilestoneMsg struct {
	document PresentationDocument
	ack      chan struct{}
}

type richAskResult struct {
	answer InteractionAnswer
	err    error
}

type richShowFormMsg struct {
	id       uint64
	form     richFormModel
	answer   func() InteractionAnswer
	step     consoleFormStep
	response chan<- richAskResult
	ack      chan struct{}
}

type richFormSubmittedMsg struct{ id uint64 }
type richFormCancelledMsg struct{ id uint64 }

type richCancelFormMsg struct {
	id  uint64
	err error
	ack chan struct{}
}

type richStartTrackMsg struct {
	label             string
	phases            []OperationPhase
	requestCancel     func() error
	retainFormCatalog bool
	ack               chan struct{}
}

type richTrackPhaseMsg struct {
	phase OperationPhase
	ack   chan struct{}
}

type richCancelTrackMsg struct{ ack chan struct{} }
type richFinishTrackMsg struct{ ack chan struct{} }

type richShowOutcomeMsg struct {
	request FinishRequest
	ack     chan struct{}
}

type richOutcomeElapsedMsg struct{}
