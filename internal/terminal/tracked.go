package terminal

import (
	"errors"
	"io"
	"sync"
)

func (run *runtimeRun) trackPlain(output io.Writer, operation TrackedOperation, protocol *phaseProtocol) error {
	err := run.consumeTracked(operation, protocol, func(phase OperationPhase) error {
		return run.presentPhase(output, phase)
	}, requestCancellation(operation.RequestCancel), nil)
	run.recordFinalPhases(protocol)
	return err
}

func (run *runtimeRun) trackSilently(operation TrackedOperation, protocol *phaseProtocol) error {
	err := run.consumeTracked(operation, protocol, nil, requestCancellation(operation.RequestCancel), nil)
	run.recordFinalPhases(protocol)
	return err
}

func (run *runtimeRun) consumeTracked(
	operation TrackedOperation,
	protocol *phaseProtocol,
	render func(OperationPhase) error,
	onCancel func() error,
	onClose func() error,
) error {
	if operation.Updates == nil {
		if onClose == nil {
			return nil
		}
		return onClose()
	}

	contextDone := run.ctx.Done()
	var firstErr error
	for {
		select {
		case <-contextDone:
			if onCancel != nil {
				firstErr = errors.Join(firstErr, onCancel())
			}
			contextDone = nil
		case phase, open := <-operation.Updates:
			if !open {
				if onClose != nil {
					firstErr = errors.Join(firstErr, onClose())
				}
				return firstErr
			}
			if firstErr != nil {
				continue
			}
			phase, err := protocol.apply(phase)
			if err != nil {
				firstErr = err
				continue
			}
			if render != nil {
				if err := render(phase); err != nil {
					firstErr = err
				}
			}
		}
	}
}

func (run *runtimeRun) trackRich(controller *richController, operation TrackedOperation, protocol *phaseProtocol) error {
	if operation.Updates == nil {
		return nil
	}
	requestCancel := requestCancellation(operation.RequestCancel)
	if err := controller.startTrack(operation.Label, protocol.snapshot(), requestCancel); err != nil {
		drainTrackedUpdates(operation.Updates)
		return err
	}
	err := run.consumeTracked(
		operation,
		protocol,
		controller.updateTrack,
		func() error {
			return errors.Join(requestCancel(), controller.cancelTrack())
		},
		controller.finishTrack,
	)
	run.recordFinalPhases(protocol)
	return err
}

func (run *runtimeRun) recordFinalPhases(protocol *phaseProtocol) {
	if protocol == nil {
		return
	}
	for _, phase := range protocol.finalSnapshot() {
		run.recordTranscript(TranscriptEvent{
			Kind:    TranscriptPhase,
			Label:   phase.Name,
			Text:    phase.Detail,
			PhaseID: phase.ID,
			State:   phase.State,
		})
	}
}

func requestCancellation(request func()) func() error {
	var once sync.Once
	return func() error {
		if request != nil {
			// A producer may publish its cancellation phase on an unbuffered
			// update channel. Start its callback separately so this run can begin
			// draining that phase instead of waiting on the producer.
			once.Do(func() { go request() })
		}
		return nil
	}
}

func drainTrackedUpdates(updates <-chan OperationPhase) {
	if updates == nil {
		return
	}
	for range updates {
	}
}

type trackedState struct {
	label             string
	phases            []OperationPhase
	cancelArmed       bool
	cancellationState bool
	requestStop       func()
}

func (state *trackedState) requestCancellation() {
	if state.cancellationState {
		return
	}
	state.cancellationState = true
	if state.requestStop != nil {
		state.requestStop()
	}
}

func (state *trackedState) applyPhase(phase OperationPhase) {
	for index := len(state.phases) - 1; index >= 0; index-- {
		if (phase.ID != "" && state.phases[index].ID == phase.ID) ||
			(phase.ID == "" && state.phases[index].Name == phase.Name) {
			if phase.Name == "" {
				phase.Name = state.phases[index].Name
			}
			if phase.ID == "" {
				phase.ID = state.phases[index].ID
			}
			phase.PhaseID = phase.ID
			state.phases[index] = phase
			return
		}
	}
	if phase.ID != "" && phase.Name == "" {
		phase.PhaseID = phase.ID
	}
	state.phases = append(state.phases, phase)
}

func (state *trackedState) currentPhase() OperationPhase {
	for index := len(state.phases) - 1; index >= 0; index-- {
		if state.phases[index].State == PhaseActive || state.phases[index].State == PhasePending {
			return state.phases[index]
		}
	}
	if len(state.phases) > 0 {
		return state.phases[len(state.phases)-1]
	}
	return OperationPhase{}
}
