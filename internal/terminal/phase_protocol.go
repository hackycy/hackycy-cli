package terminal

import (
	"errors"
	"fmt"
)

// ErrInvalidPhaseProtocol reports an invalid tracked-operation catalog or update.
var ErrInvalidPhaseProtocol = errors.New("terminal phase protocol is invalid")

type phaseProtocol struct {
	legacy  bool
	phases  []OperationPhase
	index   map[string]int
	reached map[string]bool
	active  string
}

func newPhaseProtocol(operation TrackedOperation) (*phaseProtocol, error) {
	if operation.ID != "" && operation.OperationID != "" && operation.ID != operation.OperationID {
		return nil, phaseProtocolError("operation ID %q conflicts with operation ID %q", operation.ID, operation.OperationID)
	}
	definitions := operation.Phases
	if len(operation.Phases) > 0 && len(operation.PhaseDefinitions) > 0 && !samePhaseDefinitions(operation.Phases, operation.PhaseDefinitions) {
		return nil, phaseProtocolError("phase catalogs conflict")
	}
	if len(definitions) == 0 {
		definitions = operation.PhaseDefinitions
	}
	if len(definitions) == 0 {
		return &phaseProtocol{legacy: true}, nil
	}

	protocol := &phaseProtocol{
		phases:  make([]OperationPhase, len(definitions)),
		index:   make(map[string]int, len(definitions)),
		reached: make(map[string]bool, len(definitions)),
	}
	for index, definition := range definitions {
		if definition.ID == "" {
			return nil, phaseProtocolError("phase ID is required")
		}
		if definition.Name == "" {
			return nil, phaseProtocolError("phase name is required")
		}
		if _, exists := protocol.index[definition.ID]; exists {
			return nil, phaseProtocolError("phase ID %q is duplicated", definition.ID)
		}
		protocol.index[definition.ID] = index
		protocol.phases[index] = OperationPhase{ID: definition.ID, Name: definition.Name, State: PhasePending}
	}
	return protocol, nil
}

func (protocol *phaseProtocol) apply(update OperationPhase) (OperationPhase, error) {
	if protocol.legacy {
		return update, nil
	}
	if update.ID != "" && update.PhaseID != "" && update.ID != update.PhaseID {
		return OperationPhase{}, phaseProtocolError("phase update ID %q conflicts with phase ID %q", update.ID, update.PhaseID)
	}
	if update.ID == "" {
		update.ID = update.PhaseID
	}
	if update.ID == "" {
		return OperationPhase{}, phaseProtocolError("phase update ID is required")
	}
	if update.Name != "" {
		return OperationPhase{}, phaseProtocolError("phase update %q must not replace its catalog name", update.ID)
	}
	index, exists := protocol.index[update.ID]
	if !exists {
		return OperationPhase{}, phaseProtocolError("phase update ID %q is not in the catalog", update.ID)
	}
	if !validPhaseState(update.State) {
		return OperationPhase{}, phaseProtocolError("phase %q has an unknown state", update.ID)
	}

	current := protocol.phases[index]
	if !validPhaseTransition(current.State, update.State) {
		return OperationPhase{}, phaseProtocolError("phase %q cannot transition from %s to %s", update.ID, current.State, update.State)
	}
	if update.State == PhaseActive && protocol.active != "" && protocol.active != update.ID {
		return OperationPhase{}, phaseProtocolError("phase %q cannot become active while %q is active", update.ID, protocol.active)
	}

	next := OperationPhase{
		ID:      current.ID,
		PhaseID: current.ID,
		Name:    current.Name,
		Detail:  update.Detail,
		State:   update.State,
	}
	protocol.phases[index] = next
	protocol.reached[next.ID] = true
	if next.State == PhaseActive {
		protocol.active = next.ID
	} else if protocol.active == next.ID && isTerminalPhaseState(next.State) {
		protocol.active = ""
	}
	return next, nil
}

func samePhaseDefinitions(first, second []PhaseDefinition) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func (protocol *phaseProtocol) finalSnapshot() []OperationPhase {
	if protocol == nil || protocol.legacy {
		return nil
	}
	result := make([]OperationPhase, 0, len(protocol.phases))
	for _, phase := range protocol.phases {
		if protocol.reached[phase.ID] {
			result = append(result, phase)
		}
	}
	return result
}

func (protocol *phaseProtocol) snapshot() []OperationPhase {
	return append([]OperationPhase(nil), protocol.phases...)
}

func validPhaseState(state PhaseState) bool {
	switch state {
	case PhasePending, PhaseActive, PhaseCompleted, PhaseCancelled, PhaseFailed:
		return true
	default:
		return false
	}
}

func validPhaseTransition(current, next PhaseState) bool {
	if current == next {
		return current == PhasePending || current == PhaseActive
	}
	switch current {
	case PhasePending:
		return next == PhaseActive || next == PhaseFailed || next == PhaseCancelled
	case PhaseActive:
		return next == PhaseCompleted || next == PhaseFailed || next == PhaseCancelled
	default:
		return false
	}
}

func isTerminalPhaseState(state PhaseState) bool {
	return state == PhaseCompleted || state == PhaseCancelled || state == PhaseFailed
}

func phaseProtocolError(format string, arguments ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidPhaseProtocol}, arguments...)...)
}
