package terminal

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type focusStep struct {
	state PhaseState
	name  string
}

func (model *richRootModel) focusHeader() string {
	styles := richStyles(model.color)
	width := model.formWidth()
	command, target := model.consoleCommand(), model.console.Target
	if command == "" {
		command = "YCY"
	}
	if target == "" {
		target = "terminal session"
	}
	role := VisualRoleActive
	if model.mode == richOutcomeMode {
		role = transcriptOutcomeRole(model.outcome.Outcome)
	}
	status := styles[role].Render(consoleTruncate(model.consoleStatusLabel(), width/2))
	identity := styles[VisualRoleActive].Render("◆") + " " +
		focusThemeEmphasis(styles[VisualRolePlain], model.color).Render(stripTerminalControl(command)) +
		styles[VisualRoleMuted].Render("  "+stripTerminalControl(target))
	header := consoleTruncate(identity, max(width-lipgloss.Width(status)-2, 1)) + "  " + status
	if trail := model.focusTrail(width); trail != "" {
		header += "\n" + trail
	}
	return header
}

func (model *richRootModel) focusTrail(width int) string {
	if model.mode == richOutcomeMode {
		return ""
	}
	rows := make([]focusStep, 0, len(model.formRows))
	for _, step := range model.formRows {
		rows = append(rows, focusStep{state: step.state, name: step.name})
	}
	current := -1
	if model.mode == richFormMode {
		for index, step := range model.formRows {
			if step.id == model.formID {
				current = index
				break
			}
		}
	} else if model.mode == richTrackMode && model.track != nil {
		rows = make([]focusStep, 0, len(model.track.phases))
		currentPhase := model.track.currentPhase()
		for index, phase := range model.track.phases {
			rows = append(rows, focusStep{state: phase.State, name: phase.Name})
			if phase == currentPhase {
				current = index
			}
		}
	}
	if len(rows) == 0 {
		return ""
	}
	if current < 0 || current >= len(rows) {
		current = focusRowIndex(rows)
	}
	styles := richStyles(model.color)
	separator := focusThemeStyle(model.color, focusDim).Render("  →  ")
	render := func(start, end int) string {
		parts := make([]string, 0, end-start)
		for _, row := range rows[start:end] {
			glyph, _ := consoleStateLabel(row.state)
			style := styles[consoleStateRole(row.state)]
			if row.state == PhasePending {
				style = focusThemeStyle(model.color, focusDim)
			}
			parts = append(parts, style.Render(glyph+" "+stripTerminalControl(row.name)))
		}
		return strings.Join(parts, separator)
	}
	start := max(current-1, 0)
	end := min(start+3, len(rows))
	for end-start > 1 && lipgloss.Width(render(start, end)) > width {
		if end-1 > current {
			end--
		} else {
			start++
		}
	}
	return consoleTruncate(render(start, end), width)
}

func focusRowIndex(rows []focusStep) int {
	for index := len(rows) - 1; index >= 0; index-- {
		if rows[index].state == PhaseActive {
			return index
		}
	}
	for index := len(rows) - 1; index >= 0; index-- {
		if rows[index].state == PhaseFailed || rows[index].state == PhaseCancelled {
			return index
		}
	}
	for index := len(rows) - 1; index >= 0; index-- {
		if rows[index].state == PhaseCompleted {
			return index
		}
	}
	return 0
}
