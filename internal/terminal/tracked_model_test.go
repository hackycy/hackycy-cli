package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTrackedTeaModelShowsTrailAndCurrentWorkOnNarrowTerminals(t *testing.T) {
	cancellations := 0
	model := newRichRootModel(40, 20, false)
	model.mode = richTrackMode
	model.track = &trackedState{
		label:       "Git Pulse",
		requestStop: func() { cancellations++ },
		phases: []OperationPhase{
			{ID: "scan", Name: "Scanning repositories", Detail: "workspace/project", State: PhaseCompleted},
			{ID: "fetch", Name: "Fetching commits", Detail: "workspace/project", State: PhaseActive},
		},
	}

	rendered := model.View()
	if !rendered.AltScreen || !rendered.DisableBracketedPasteMode {
		t.Fatalf("v2 rich view terminal mode = %#v", rendered)
	}
	view := rendered.Content
	if !strings.Contains(view, "◆ Fetching commits") || !strings.Contains(view, "workspace/project") {
		t.Fatalf("narrow view = %q", view)
	}
	for _, duplicate := range []string{"STEPS", "✓ DONE", "◆ ACTIVE", "Git Pulse", "Scanning repositories"} {
		if strings.Contains(view, duplicate) {
			t.Fatalf("narrow active-only view retained %q: %q", duplicate, view)
		}
	}

	_, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cancellations != 0 || !strings.Contains(model.View().Content, "Press Esc again to cancel") {
		t.Fatalf("first Esc = cancellations=%d, view=%q", cancellations, model.View().Content)
	}
	_, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cancellations != 1 || !strings.Contains(model.View().Content, "Cancelling...") {
		t.Fatalf("second Esc = cancellations=%d, view=%q", cancellations, model.View().Content)
	}
	_, _ = model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cancellations != 1 {
		t.Fatalf("Ctrl-C requested cancellation more than once: %d", cancellations)
	}
}

func TestTrackedTeaModelSanitizesPhaseText(t *testing.T) {
	model := newRichRootModel(80, 20, false)
	model.mode = richTrackMode
	model.track = &trackedState{label: "Work\x1b[2K"}
	model.track.applyPhase(OperationPhase{
		Name:   "Scanning\x1b[31m",
		Detail: "path\x01\tvalue",
		State:  PhaseActive,
	})

	view := model.View().Content
	if strings.ContainsRune(view, '\x1b') || strings.ContainsRune(view, '\x01') || strings.Contains(view, "\t") {
		t.Fatalf("phase view contains terminal control: %q", view)
	}
	if !strings.Contains(view, "Scanning") || !strings.Contains(view, "path") {
		t.Fatalf("phase view lost semantic text: %q", view)
	}
}

func TestTrackedCurrentPhasePrioritizesActiveWorkOverFutureSteps(t *testing.T) {
	state := trackedState{phases: []OperationPhase{
		{Name: "Read", State: PhaseActive},
		{Name: "Write", State: PhasePending},
		{Name: "Finish", State: PhasePending},
	}}
	if got := state.currentPhase().Name; got != "Read" {
		t.Fatalf("current phase = %q, want active Read", got)
	}
	state.phases[0].State = PhaseCompleted
	if got := state.currentPhase().Name; got != "Write" {
		t.Fatalf("current phase = %q, want next Write", got)
	}
}
