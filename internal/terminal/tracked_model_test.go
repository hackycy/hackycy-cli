package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTrackedTeaModelRetainsOrderedRowsOnNarrowTerminals(t *testing.T) {
	cancellations := 0
	model := newRichRootModel(40, 20, false)
	model.mode = richTrackMode
	model.track = &trackedState{label: "Git Pulse", requestStop: func() { cancellations++ }}
	model.track.applyPhase(OperationPhase{Name: "Scanning repositories", Detail: "workspace/project", State: PhaseCompleted})
	model.track.applyPhase(OperationPhase{Name: "Fetching commits", Detail: "workspace/project", State: PhaseActive})

	rendered := model.View()
	if !rendered.AltScreen || !rendered.DisableBracketedPasteMode {
		t.Fatalf("v2 rich view terminal mode = %#v", rendered)
	}
	view := rendered.Content
	if !strings.Contains(view, "STATE / PHASE / DETAIL") || !strings.Contains(view, "Git Pulse") || !strings.Contains(view, "Fetching commits") || !strings.Contains(view, "workspace/project") {
		t.Fatalf("narrow view = %q", view)
	}
	if !strings.Contains(view, "Scanning repositories") || !strings.Contains(view, "✓ DONE") || !strings.Contains(view, "◆ ACTIVE") {
		t.Fatalf("narrow view did not retain B state rows: %q", view)
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
