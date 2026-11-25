package terminal

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func (labels WorkChecklistLabels) normalized() WorkChecklistLabels {
	label := func(value, fallback string) string {
		if value = strings.TrimSpace(stripTerminalControl(value)); value != "" {
			return value
		}
		return fallback
	}
	return WorkChecklistLabels{
		Active: label(labels.Active, "Active"), Completed: label(labels.Completed, "Done"),
		Skipped: label(labels.Skipped, "Skipped"), Confirm: label(labels.Confirm, "Continue"),
		Decline: label(labels.Decline, "Stop"),
	}
}

func (labels WorkChecklistLabels) status(state PhaseState) string {
	switch state {
	case PhaseActive:
		return labels.Active
	case PhaseCompleted:
		return labels.Completed
	case PhaseFailed:
		return "Failed"
	case PhaseCancelled:
		return "Cancelled"
	case PhaseSkipped:
		return labels.Skipped
	default:
		return "Pending"
	}
}

func (labels WorkChecklistLabels) summary(phases []OperationPhase) string {
	counts := make(map[PhaseState]int)
	for _, phase := range phases {
		counts[phase.State]++
	}
	var parts []string
	for _, state := range []PhaseState{PhaseCompleted, PhasePending, PhaseActive, PhaseFailed, PhaseCancelled, PhaseSkipped} {
		if count := counts[state]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", labels.status(state), count))
		}
	}
	return strings.Join(parts, " · ")
}

func (labels WorkChecklistLabels) row(phase OperationPhase, next bool) PresentationBlock {
	glyph, _ := consoleStateLabel(phase.State)
	role := consoleStateRole(phase.State)
	prefix := "  "
	if next {
		prefix, role = "› ", VisualRoleActive
	}
	status := labels.status(phase.State)
	width := max(ansi.StringWidth(labels.Active), ansi.StringWidth(labels.Completed), ansi.StringWidth(labels.Skipped), len("Cancelled"))
	return PresentationBlock{Role: role, Text: prefix + glyph + " " + status + strings.Repeat(" ", width-ansi.StringWidth(status)+2) + phase.Name}
}

func (labels WorkChecklistLabels) document(phases []OperationPhase) PresentationDocument {
	blocks := []PresentationBlock{{Role: VisualRoleTitle, Text: labels.summary(phases)}}
	next := checklistFocus(phases)
	for _, phase := range phases {
		blocks = append(blocks, labels.row(phase, phase.ID == next))
	}
	return PresentationDocument{Blocks: blocks}
}

func checklistFocus(phases []OperationPhase) string {
	pending := ""
	for _, phase := range phases {
		if phase.State == PhaseActive {
			return phase.ID
		}
		if phase.State == PhasePending && pending == "" {
			pending = phase.ID
		}
	}
	return pending
}

func (model *richRootModel) checklistConfirm() bool {
	form, ok := model.form.(*richHuhForm)
	return model.checklist != nil && ok && form.request.Kind == InteractionConfirm
}

func (model *richRootModel) checklistFooter() string {
	controls := "Esc stop · ↑/↓ scroll"
	if model.checklistConfirm() {
		form := model.form.(*richHuhForm)
		confirmed, _ := form.form.GetFocusedField().GetValue().(bool)
		accept, decline := model.checklist.Confirm, model.checklist.Decline
		if confirmed {
			accept = "[" + accept + "]"
		} else {
			decline = "[" + decline + "]"
		}
		controls = "Enter " + accept + "  " + decline + " ←/→\n" + controls
	} else if model.track.cancellationState {
		controls = "Cancelling…"
	}
	return ansi.Wrap(controls, model.formWidth(), "")
}

func (model *richRootModel) checklistBlocks() []scrollBlock {
	phases := model.track.phases
	blocks := make([]scrollBlock, 0, len(phases))
	next := checklistFocus(phases)
	for _, phase := range phases {
		row := model.checklist.row(phase, phase.ID == next)
		text := renderRich(PresentationDocument{Blocks: []PresentationBlock{row}}, RichOptions{Profile: profileForColor(model.color)})
		blocks = append(blocks, scrollBlock{"work-" + phase.ID, strings.TrimSuffix(text, "\n")})
	}
	return blocks
}
