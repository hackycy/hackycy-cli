package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

var (
	// ErrExperienceRunClosed reports use after a terminal run has returned control.
	ErrExperienceRunClosed = errors.New("terminal experience run is closed")
	// ErrExperienceRunFinished reports interactive use after the first durable result.
	ErrExperienceRunFinished = errors.New("terminal experience run has emitted its result")
	// ErrInvalidFinishOutcome reports a completion request outside the semantic contract.
	ErrInvalidFinishOutcome = errors.New("terminal finish outcome is invalid")
	// ErrInvalidResultCheckpoint reports a checkpoint without a stable ID.
	ErrInvalidResultCheckpoint = errors.New("terminal result checkpoint ID is invalid")
	// ErrResultCheckpointEmitted reports an attempt to write the same checkpoint twice.
	ErrResultCheckpointEmitted = errors.New("terminal result checkpoint was already emitted")
)

// ExperienceOptions supplies terminal-owned dependencies for one invocation.
type ExperienceOptions struct {
	Capabilities Capabilities
	Input        io.Reader
	Output       io.Writer
	Diagnostics  io.Writer
	Width        int
	Height       int
	Transcript   TranscriptOptions
}

// Runtime is the concrete terminal Experience for one invocation.
type Runtime struct {
	capabilities       Capabilities
	input              io.Reader
	output             io.Writer
	diagnostics        *LeaseAwareDiagnosticWriter
	inputTerminal      *os.File
	outputTerminal     *os.File
	diagnosticTerminal *os.File
	width              int
	height             int
	transcriptOptions  TranscriptOptions
}

// NewExperience constructs a terminal Experience from explicit inherited streams.
func NewExperience(options ExperienceOptions) *Runtime {
	if options.Input == nil {
		options.Input = emptyInput{}
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	runtime := &Runtime{
		capabilities:      options.Capabilities,
		input:             options.Input,
		output:            options.Output,
		diagnostics:       NewLeaseAwareDiagnosticWriter(options.Diagnostics),
		width:             options.Width,
		height:            options.Height,
		transcriptOptions: options.Transcript,
	}
	runtime.inputTerminal, _ = options.Input.(*os.File)
	runtime.outputTerminal, _ = options.Output.(*os.File)
	runtime.diagnosticTerminal, _ = options.Diagnostics.(*os.File)
	return runtime
}

// Capabilities returns the immutable capabilities selected for this Experience.
func (runtime *Runtime) Capabilities() Capabilities {
	return runtime.capabilities
}

// Open starts a per-command terminal run.
func (runtime *Runtime) Open(ctx context.Context) ExperienceRun {
	return runtime.open(ctx, defaultConsoleDescriptor(), false)
}

// OpenConsole starts a run with command-owned safe Console context. The
// descriptor is validated before a Rich form, Notice, or Track can begin.
func (runtime *Runtime) OpenConsole(ctx context.Context, descriptor ConsoleDescriptor) (ExperienceRun, error) {
	descriptor, err := normalizeConsoleDescriptor(descriptor)
	if err != nil {
		return nil, err
	}
	return runtime.open(ctx, descriptor, true), nil
}

func (runtime *Runtime) open(ctx context.Context, descriptor ConsoleDescriptor, eagerRich bool) *runtimeRun {
	if ctx == nil {
		ctx = context.Background()
	}
	run := &runtimeRun{
		runtime: runtime,
		ctx:     ctx,
		console: descriptor,
		interactions: NewInteractionHandler(InteractionOptions{
			Capabilities: runtime.capabilities,
			Input:        runtime.input,
			Diagnostics:  runtime.diagnostics,
		}),
		ledger:      NewTranscriptLedger(runtime.transcriptOptions),
		checkpoints: make(map[string]struct{}),
	}
	if eagerRich && run.richEnabled() {
		// OpenConsole has not committed semantic state yet. A renderer that
		// cannot start at this point can therefore safely degrade to Plain.
		if _, err := run.ensureRich(); err != nil {
			run.disableRich()
		}
	}
	return run
}

// DiagnosticWriter coordinates normal diagnostic records with the active Rich UI.
func (runtime *Runtime) DiagnosticWriter() io.Writer {
	return runtime.diagnostics
}

type runtimeRun struct {
	runtime      *Runtime
	ctx          context.Context
	interactions *InteractionHandler
	console      ConsoleDescriptor

	operation        sync.Mutex
	state            runState
	finishedByFinish bool
	richDisabled     bool
	richFailure      error
	controller       *richController
	ledger           *TranscriptLedger
	checkpoints      map[string]struct{}
}

type runState uint8

const (
	runActive runState = iota
	runFinished
	runClosed
)

func (run *runtimeRun) Ask(request InteractionRequest) (InteractionAnswer, error) {
	run.operation.Lock()
	defer run.operation.Unlock()
	if err := run.interactiveAvailable(); err != nil {
		return InteractionAnswer{}, err
	}
	if err := validateInteractionRequest(request); err != nil {
		return InteractionAnswer{}, err
	}
	if run.richEnabled() {
		controller, err := run.ensureRich()
		if err == nil {
			answer, askErr := controller.ask(run.ctx, run.interactions, request)
			if askErr != nil && controller.stopped() {
				askErr = run.recoverRichFailure(askErr)
			}
			run.recordInteraction(request, answer, askErr)
			return answer, askErr
		}
		if !errors.Is(err, errRichUnavailable) {
			return InteractionAnswer{}, err
		}
		run.disableRich()
	}
	answer, askErr := run.interactions.Ask(run.ctx, request)
	run.recordInteraction(request, answer, askErr)
	return answer, askErr
}

func (run *runtimeRun) Track(operation TrackedOperation) error {
	run.operation.Lock()
	defer run.operation.Unlock()
	if err := run.interactiveAvailable(); err != nil {
		return err
	}
	protocol, err := newPhaseProtocol(operation)
	if err != nil {
		drainTrackedUpdates(operation.Updates)
		return err
	}
	if run.richEnabled() {
		controller, err := run.ensureRich()
		if err == nil {
			err = run.trackRich(controller, operation, protocol)
			if err != nil && controller.stopped() {
				return run.recoverRichFailure(err)
			}
			return err
		}
		if !errors.Is(err, errRichUnavailable) {
			drainTrackedUpdates(operation.Updates)
			return err
		}
		run.disableRich()
	}
	if run.runtime.capabilities.Interaction == Automation {
		return run.trackSilently(operation, protocol)
	}
	return run.trackPlain(run.runtime.diagnostics, operation, protocol)
}

func (run *runtimeRun) Notice(document PresentationDocument) error {
	run.operation.Lock()
	defer run.operation.Unlock()
	if err := run.interactiveAvailable(); err != nil {
		return err
	}
	if run.runtime.capabilities.Interaction == Automation {
		return nil
	}
	if run.richEnabled() {
		controller, err := run.ensureRich()
		if err == nil {
			err = controller.notice(document)
			if err != nil && controller.stopped() {
				return run.recoverRichFailure(err)
			}
			return err
		}
		if !errors.Is(err, errRichUnavailable) {
			return err
		}
		run.disableRich()
	}
	return WritePlain(run.runtime.diagnostics, document)
}

// Milestone publishes one explicit durable checkpoint in the active view.
func (run *runtimeRun) Milestone(document PresentationDocument) error {
	run.operation.Lock()
	defer run.operation.Unlock()
	if err := run.interactiveAvailable(); err != nil {
		return err
	}
	if strings.TrimSpace(normalizeTranscriptField(document.transcriptText(), run.ledger.maxFieldSize)) == "" {
		return nil
	}
	if run.runtime.capabilities.Interaction == Automation {
		return nil
	}
	if run.richEnabled() {
		controller, err := run.ensureRich()
		if err == nil {
			err = controller.milestone(document)
			if err != nil && controller.stopped() {
				return run.recoverRichFailure(err)
			}
			if err == nil {
				run.recordTranscript(TranscriptEvent{Kind: TranscriptMilestone, Text: document.transcriptText()})
			}
			return err
		}
		if !errors.Is(err, errRichUnavailable) {
			return err
		}
		run.disableRich()
	}
	err := WritePlain(run.runtime.diagnostics, document)
	if err == nil {
		run.recordTranscript(TranscriptEvent{Kind: TranscriptMilestone, Text: document.transcriptText()})
	}
	return err
}

// Finish commits one finite command outcome and emits its optional result once.
// FinishRequest is the production semantic form; the legacy outcome/document
// shape remains accepted so command adapters can migrate independently.
func (run *runtimeRun) Finish(value any, documents ...*PresentationDocument) error {
	run.operation.Lock()
	defer run.operation.Unlock()
	if run.state == runClosed {
		return ErrExperienceRunClosed
	}
	if run.state == runFinished {
		return ErrExperienceRunFinished
	}
	request, result, legacy, err := finishRequestFromValue(value, documents...)
	if err != nil {
		return err
	}
	normalized, err := normalizeFinishRequest(request)
	if err != nil {
		if legacy && !request.Outcome.valid() {
			return ErrInvalidFinishOutcome
		}
		return err
	}

	run.state = runFinished
	run.finishedByFinish = true
	run.recordTranscript(TranscriptEvent{
		Kind:     TranscriptOutcome,
		Outcome:  normalized.Outcome,
		Location: normalized.Location,
		Summary:  normalized.Summary.transcriptText(),
	})
	run.freezeTranscript()
	var outcomeErr error
	if run.controller != nil {
		outcomeErr = run.controller.outcome(run.ctx, normalized)
		if outcomeErr != nil {
			// An unexpected renderer termination uses the same recovery path as
			// interactive operations: preserve the renderer error, restore the
			// terminal, replay the bounded transcript, and fall back to Plain for
			// the durable result.
			outcomeErr = run.recoverRichFailure(outcomeErr)
		}
	}
	restoreErr := run.stopRich()
	if result == nil {
		return errors.Join(outcomeErr, run.richFailure, restoreErr)
	}
	return errors.Join(outcomeErr, run.richFailure, restoreErr, run.writeResult(*result))
}

func finishRequestFromValue(value any, documents ...*PresentationDocument) (FinishRequest, *PresentationDocument, bool, error) {
	if len(documents) > 1 {
		return FinishRequest{}, nil, false, fmt.Errorf("%w: at most one durable result is allowed", ErrInvalidFinishRequest)
	}
	var result *PresentationDocument
	if len(documents) == 1 {
		result = documents[0]
	}
	switch request := value.(type) {
	case FinishRequest:
		return request, result, false, nil
	case *FinishRequest:
		if request == nil {
			return FinishRequest{}, nil, false, fmt.Errorf("%w: request is nil", ErrInvalidFinishRequest)
		}
		return *request, result, false, nil
	case FinishOutcome:
		// Existing adapters pass a durable Result as the second argument. Keep
		// it out of the new completion projection until the adapter provides an
		// explicit FinishRequest in its own migration slice.
		return FinishRequest{Outcome: request}, result, true, nil
	default:
		return FinishRequest{}, nil, false, fmt.Errorf("%w: unsupported request type %T", ErrInvalidFinishRequest, value)
	}
}

// ResultCheckpoint writes one stable service-command checkpoint while leaving
// the run active for later checkpoints and shutdown cleanup.
func (run *runtimeRun) ResultCheckpoint(id string, document PresentationDocument) error {
	run.operation.Lock()
	defer run.operation.Unlock()
	if run.state == runClosed {
		return ErrExperienceRunClosed
	}
	if run.state == runFinished {
		return ErrExperienceRunFinished
	}
	if strings.TrimSpace(id) == "" {
		return ErrInvalidResultCheckpoint
	}
	if run.checkpoints == nil {
		run.checkpoints = make(map[string]struct{})
	}
	if _, emitted := run.checkpoints[id]; emitted {
		return ErrResultCheckpointEmitted
	}
	if run.richFailure != nil {
		return run.richFailure
	}
	run.checkpoints[id] = struct{}{}
	if err := run.writeResult(document); err != nil {
		// The checkpoint is intentionally not retried. Keep the ID consumed so
		// a caller cannot turn a write failure into duplicate durable output.
		return err
	}
	return nil
}

// Result is the compatibility durable-result operation used by un-migrated commands.
func (run *runtimeRun) Result(document PresentationDocument) error {
	run.operation.Lock()
	defer run.operation.Unlock()
	if run.state == runClosed {
		return ErrExperienceRunClosed
	}
	if run.finishedByFinish {
		return ErrExperienceRunFinished
	}
	if run.richFailure != nil {
		return run.richFailure
	}

	var restoreErr error
	if run.state == runActive {
		run.state = runFinished
		restoreErr = run.stopRich()
	}
	return errors.Join(restoreErr, run.writeResult(document))
}

func (run *runtimeRun) Close() error {
	run.operation.Lock()
	defer run.operation.Unlock()
	if run.state == runClosed {
		return nil
	}
	run.state = runClosed
	run.freezeTranscript()
	return run.stopRich()
}

func (run *runtimeRun) recordInteraction(request InteractionRequest, answer InteractionAnswer, err error) {
	if errors.Is(err, ErrInteractionCancelled) || errors.Is(err, context.Canceled) {
		run.recordTranscript(TranscriptEvent{Kind: TranscriptAsk, Label: request.TranscriptLabel, Text: "cancelled"})
		return
	}
	if err != nil {
		return
	}
	text := interactionTranscriptText(request, answer)
	run.recordTranscript(TranscriptEvent{Kind: TranscriptAsk, Label: request.TranscriptLabel, Text: text})
}

func (run *runtimeRun) recordTranscript(event TranscriptEvent) {
	if run.runtime.capabilities.Interaction == Automation {
		return
	}
	run.ledger.Append(event)
}

func (run *runtimeRun) freezeTranscript() {
	if run.runtime.capabilities.Interaction == Automation {
		return
	}
	run.ledger.Freeze()
}

func (run *runtimeRun) interactiveAvailable() error {
	switch run.state {
	case runFinished:
		return ErrExperienceRunFinished
	case runClosed:
		return ErrExperienceRunClosed
	default:
		if run.richFailure != nil {
			return run.richFailure
		}
		return nil
	}
}

func (run *runtimeRun) richEnabled() bool {
	return run.runtime.capabilities.Interaction == RichInteractive && !run.richDisabled
}

func (run *runtimeRun) disableRich() {
	run.richDisabled = true
	capabilities := run.interactions.capabilities
	capabilities.Interaction = PlainInteractive
	run.interactions.capabilities = capabilities
}

func (run *runtimeRun) ensureRich() (*richController, error) {
	if run.controller != nil {
		return run.controller, nil
	}
	controller := newRichController(run.runtime, run.console)
	if err := controller.start(); err != nil {
		return nil, err
	}
	run.controller = controller
	return controller, nil
}

func (run *runtimeRun) stopRich() error {
	if run.controller == nil {
		return nil
	}
	controller := run.controller
	run.controller = nil
	return controller.close(run.ledger)
}

func (run *runtimeRun) recoverRichFailure(rendererErr error) error {
	if rendererErr == nil {
		return nil
	}
	run.richFailure = rendererErr
	run.disableRich()
	run.freezeTranscript()
	if run.controller == nil {
		return rendererErr
	}
	controller := run.controller
	run.controller = nil
	return errors.Join(rendererErr, controller.closeAfterFailure(run.ledger))
}

func (run *runtimeRun) writeResult(document PresentationDocument) error {
	// Only an active Rich run may style a durable result. Plain and Automation
	// capabilities (including a preflight Rich fallback) must remain control-free.
	if run.runtime.capabilities.Interaction != RichInteractive || run.richDisabled || !run.runtime.capabilities.Stdout.Terminal {
		return WritePlain(run.runtime.output, document)
	}
	width := run.runtime.width
	if run.runtime.outputTerminal != nil {
		if terminalWidth, _, err := term.GetSize(int(run.runtime.outputTerminal.Fd())); err == nil {
			width = terminalWidth
		}
	}
	return WriteRich(run.runtime.output, document, RichOptions{
		Width: width,
		Color: run.runtime.capabilities.Stdout.Color,
	})
}

func (run *runtimeRun) presentPhase(output io.Writer, phase OperationPhase) error {
	document := PresentationDocument{Blocks: []PresentationBlock{{
		Role: phaseRole(phase.State),
		Text: phase.Name,
	}}}
	if phase.Detail != "" {
		document.Blocks = append(document.Blocks, PresentationBlock{Role: VisualRoleMuted, Text: phase.Detail})
	}
	return WritePlain(output, document)
}

func phaseRole(state PhaseState) VisualRole {
	switch state {
	case PhaseCompleted:
		return VisualRoleSuccess
	case PhaseCancelled, PhaseFailed:
		return VisualRoleError
	case PhasePending, PhaseActive:
		return VisualRoleActive
	default:
		return VisualRolePlain
	}
}

func (outcome FinishOutcome) valid() bool {
	switch outcome {
	case Succeeded, Cancelled, Failed:
		return true
	default:
		return false
	}
}

type emptyInput struct{}

func (emptyInput) Read([]byte) (int, error) {
	return 0, io.EOF
}

var _ Experience = (*Runtime)(nil)
var _ ExperienceRun = (*runtimeRun)(nil)
