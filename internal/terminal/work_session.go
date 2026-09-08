package terminal

import (
	"errors"
	"fmt"
	"strings"
)

// runtimeWorkSession keeps one complete Work Catalog alive while a command
// moves through its declared interactions. Its methods share the run lock, so
// calls remain serialized without making the catalog own that lock between
// updates.
type runtimeWorkSession struct {
	run             *runtimeRun
	protocol        *phaseProtocol
	requestCancel   func() error
	done            chan struct{}
	closed          bool
	transcriptIndex int
	err             error
}

// StartWork opens one controlled Work Catalog. It is intentionally separate
// from Track so existing adapters retain their channel-based lifecycle.
func (run *runtimeRun) StartWork(catalog WorkCatalog) (WorkSession, error) {
	run.operation.Lock()
	defer run.operation.Unlock()
	if err := run.interactiveAvailable(); err != nil {
		return nil, err
	}
	if run.work != nil {
		return nil, ErrWorkSessionActive
	}
	if strings.TrimSpace(catalog.ID) == "" {
		return nil, fmt.Errorf("%w: catalog ID is required", ErrInvalidWorkCatalog)
	}
	if len(catalog.Phases) == 0 {
		return nil, fmt.Errorf("%w: at least one phase is required", ErrInvalidWorkCatalog)
	}
	protocol, err := newPhaseProtocol(TrackedOperation{
		ID:     catalog.ID,
		Label:  catalog.Label,
		Phases: catalog.Phases,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidWorkCatalog, err)
	}
	session := &runtimeWorkSession{
		run:           run,
		protocol:      protocol,
		requestCancel: requestCancellation(catalog.RequestCancel),
		done:          make(chan struct{}),
	}
	if run.richEnabled() {
		controller, err := run.ensureRich()
		if err == nil {
			err = controller.startWork(catalog.Label, protocol.snapshot(), session.requestCancel)
			if err != nil && controller.stopped() {
				err = run.recoverRichFailure(err)
			}
			if err != nil {
				return nil, err
			}
		} else if errors.Is(err, errRichUnavailable) {
			run.disableRich()
		} else {
			return nil, err
		}
	}
	run.work = session
	go session.watchContext()
	return session, nil
}

func (session *runtimeWorkSession) Update(update OperationPhase) error {
	session.run.operation.Lock()
	defer session.run.operation.Unlock()
	if session.closed || session.run.work != session {
		return ErrWorkSessionClosed
	}
	if err := session.run.interactiveAvailable(); err != nil {
		return session.remember(err)
	}
	phase, err := session.protocol.apply(update)
	if err != nil {
		return session.remember(err)
	}
	if err := session.present(phase); err != nil {
		return session.remember(err)
	}
	if isTerminalPhaseState(phase.State) {
		session.recordTerminalPhases()
	}
	return nil
}

func (session *runtimeWorkSession) Close() error {
	session.run.operation.Lock()
	defer session.run.operation.Unlock()
	return session.closeLocked()
}

func (session *runtimeWorkSession) present(phase OperationPhase) error {
	run := session.run
	if run.richEnabled() {
		controller, err := run.ensureRich()
		if err == nil {
			err = controller.updateTrack(phase)
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
	if run.runtime.capabilities.Interaction == Automation {
		return nil
	}
	return run.presentPhase(run.runtime.diagnostics, phase)
}

func (session *runtimeWorkSession) watchContext() {
	select {
	case <-session.done:
		return
	case <-session.run.ctx.Done():
		session.cancel()
	}
}

func (session *runtimeWorkSession) cancel() {
	session.run.operation.Lock()
	defer session.run.operation.Unlock()
	if session.closed || session.run.work != session {
		return
	}
	err := session.requestCancel()
	if session.run.controller != nil {
		controllerErr := session.run.controller.cancelTrack()
		if controllerErr != nil && session.run.controller.stopped() {
			controllerErr = session.run.recoverRichFailure(controllerErr)
		}
		err = errors.Join(err, controllerErr)
	}
	session.remember(err)
}

func (run *runtimeRun) closeActiveWork() error {
	if run.work == nil {
		return nil
	}
	return run.work.closeLocked()
}

func (session *runtimeWorkSession) closeLocked() error {
	if session.closed {
		return nil
	}
	session.closed = true
	close(session.done)
	session.recordTerminalPhases()
	if session.run.controller != nil {
		err := session.run.controller.finishTrack()
		if err != nil && session.run.controller.stopped() {
			err = session.run.recoverRichFailure(err)
		}
		session.remember(err)
	}
	if session.run.work == session {
		session.run.work = nil
	}
	return session.err
}

func (session *runtimeWorkSession) recordTerminalPhases() {
	for session.transcriptIndex < len(session.protocol.phases) {
		phase := session.protocol.phases[session.transcriptIndex]
		if !session.protocol.reached[phase.ID] || !isTerminalPhaseState(phase.State) {
			return
		}
		session.run.recordTranscript(TranscriptEvent{
			Kind:    TranscriptPhase,
			Label:   phase.Name,
			Text:    phase.Detail,
			PhaseID: phase.ID,
			State:   phase.State,
		})
		session.transcriptIndex++
	}
}

func (session *runtimeWorkSession) remember(err error) error {
	if err != nil {
		session.err = errors.Join(session.err, err)
	}
	return err
}

func (run *runtimeRun) validateWorkSessionForm(request InteractionRequest) error {
	if run.work == nil {
		return nil
	}
	id := strings.TrimSpace(stripTerminalControl(request.ConsoleStepID))
	if id == "" {
		return ErrUndeclaredWorkSessionForm
	}
	for _, step := range run.console.FormCatalog {
		if step.ID == id {
			return nil
		}
	}
	return ErrUndeclaredWorkSessionForm
}

var _ WorkSessionStarter = (*runtimeRun)(nil)
var _ WorkSession = (*runtimeWorkSession)(nil)
