package terminal

import (
	"errors"
	"testing"
)

func TestPhaseProtocolRejectsConflictingCatalogAliases(t *testing.T) {
	_, err := newPhaseProtocol(TrackedOperation{
		Phases:           []PhaseDefinition{{ID: "scan", Name: "Scan"}},
		PhaseDefinitions: []PhaseDefinition{{ID: "fetch", Name: "Fetch"}},
	})
	if !errors.Is(err, ErrInvalidPhaseProtocol) {
		t.Fatalf("newPhaseProtocol() error = %v, want ErrInvalidPhaseProtocol", err)
	}

	protocol, err := newPhaseProtocol(TrackedOperation{
		Phases:           []PhaseDefinition{{ID: "scan", Name: "Scan"}},
		PhaseDefinitions: []PhaseDefinition{{ID: "scan", Name: "Scan"}},
	})
	if err != nil || protocol == nil {
		t.Fatalf("identical catalog aliases = (%v, %#v), want success", err, protocol)
	}
}

func TestPhaseProtocolRejectsConflictingOperationIDAliases(t *testing.T) {
	_, err := newPhaseProtocol(TrackedOperation{
		ID:          "refresh",
		OperationID: "other",
	})
	if !errors.Is(err, ErrInvalidPhaseProtocol) {
		t.Fatalf("newPhaseProtocol() error = %v, want ErrInvalidPhaseProtocol", err)
	}
}

func TestPhaseProtocolRejectsConflictingUpdateIDs(t *testing.T) {
	protocol, err := newPhaseProtocol(TrackedOperation{
		Phases: []PhaseDefinition{{ID: "scan", Name: "Scan"}},
	})
	if err != nil {
		t.Fatalf("newPhaseProtocol() error = %v", err)
	}

	if _, err := protocol.apply(OperationPhase{ID: "scan", PhaseID: "other", State: PhaseActive}); !errors.Is(err, ErrInvalidPhaseProtocol) {
		t.Fatalf("apply() error = %v, want ErrInvalidPhaseProtocol", err)
	}

	phase, err := protocol.apply(OperationPhase{PhaseID: "scan", State: PhaseActive})
	if err != nil || phase.ID != "scan" || phase.PhaseID != "scan" {
		t.Fatalf("PhaseID-only update = (%#v, %v), want canonical phase", phase, err)
	}
}
