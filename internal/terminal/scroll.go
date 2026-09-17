package terminal

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Each block has a stable identity so reflowing a path or a Chinese paragraph
// can retain the same logical line and column at the top of the viewport.
type scrollBlock struct {
	id   string
	text string
}

type scrollAnchor struct {
	block  string
	line   int
	column int
}

type consoleScroll struct {
	viewport  viewport.Model
	anchors   []scrollAnchor
	following bool
	unread    int
	focus     int
	reveal    bool
}

func newConsoleScroll() consoleScroll {
	return consoleScroll{viewport: viewport.New(), following: true, focus: -1}
}

func (scroll *consoleScroll) setContent(blocks []scrollBlock, width, height int) {
	var anchor scrollAnchor
	if offset := scroll.viewport.YOffset(); offset < len(scroll.anchors) {
		anchor = scroll.anchors[offset]
	}
	var lines []string
	var anchors []scrollAnchor
	for _, block := range blocks {
		for index, line := range strings.Split(block.text, "\n") {
			column := 0
			for _, wrapped := range strings.Split(ansi.Wrap(line, width, ""), "\n") {
				lines = append(lines, wrapped)
				anchors = append(anchors, scrollAnchor{block: block.id, line: index, column: column})
				column += ansi.StringWidth(wrapped)
			}
		}
	}
	scroll.viewport.SetWidth(width)
	scroll.viewport.SetHeight(height)
	scroll.viewport.SetContentLines(lines)
	scroll.anchors = anchors
	if scroll.following {
		scroll.viewport.GotoBottom()
		return
	}
	// Choose the wrapped row containing the previous top-left character.
	for index, candidate := range anchors {
		if candidate.block == anchor.block && candidate.line == anchor.line && candidate.column <= anchor.column {
			scroll.viewport.SetYOffset(index)
		}
	}
}

func (scroll *consoleScroll) focusVisible() bool {
	return scroll.focus >= scroll.viewport.YOffset() && scroll.focus < scroll.viewport.YOffset()+scroll.viewport.Height()
}

func (scroll *consoleScroll) revealFocus() {
	if scroll.focus < 0 {
		return
	}
	if scroll.focus < scroll.viewport.YOffset() {
		scroll.viewport.SetYOffset(scroll.focus)
	} else if !scroll.focusVisible() {
		scroll.viewport.SetYOffset(scroll.focus - scroll.viewport.Height() + 1)
	}
}

func (model *richRootModel) tooSmall() bool {
	return model.width < 30 || model.height < 10
}

func (model *richRootModel) scrollHelp() string {
	if model.form != nil {
		if form, ok := model.form.(*richHuhForm); ok {
			return form.help() + " · alt+↑/↓ scroll · esc cancel"
		}
		return "enter submit · alt+↑/↓ scroll · esc cancel"
	}
	if model.mode == richOutcomeMode {
		return "↑/↓ scroll · Home/End · enter close"
	}
	if model.track != nil {
		return "↑/↓ scroll · Home/End · esc cancel"
	}
	return "↑/↓ scroll · Home/End"
}

func (model *richRootModel) scrollFooter() string {
	v := &model.scroll.viewport
	up, down := "─", "─"
	if !v.AtTop() {
		up = "↑"
	}
	if !v.AtBottom() {
		down = "↓"
	}
	status := fmt.Sprintf("%s %d–%d/%d %s · wheel", up, v.YOffset()+1, min(v.YOffset()+v.Height(), v.TotalLineCount()), v.TotalLineCount(), down)
	if model.form == nil && model.mode != richOutcomeMode {
		if model.scroll.following {
			status += " · following"
		} else {
			status += fmt.Sprintf(" · +%d new · End follow", model.scroll.unread)
		}
	}
	if model.mouseDisabled {
		status += " · ctrl+g mouse"
	} else {
		status += " · ctrl+g copy"
	}
	return ansi.Wrap(status+"\n"+model.scrollHelp(), model.formWidth(), "")
}

func (model *richRootModel) prepareScroll() {
	if model.tooSmall() {
		return
	}
	width := model.formWidth()
	// Reserve the footer's actual wrapped height. The longest status includes
	// paused-follow information, so it cannot push the input out of the view.
	footerHeight := max(lineCount(model.scrollFooter()), 2)
	height := max(model.height-1-footerHeight, 1)
	blocks := model.scrollBlocks(width)
	model.scroll.setContent(blocks, width, height)
	// A change in digit count or follow state can add one footer line.
	height = max(model.height-1-lineCount(model.scrollFooter()), 1)
	model.scroll.viewport.SetHeight(height)
	if model.scroll.following {
		model.scroll.viewport.GotoBottom()
	} else {
		model.scroll.viewport.SetYOffset(model.scroll.viewport.YOffset())
	}
	model.scroll.focus = -1
	if model.form != nil {
		focus := 0
		if form, ok := model.form.(*richHuhForm); ok {
			focus = form.focusLine()
		}
		for index, anchor := range model.scroll.anchors {
			if anchor.block == "form" && anchor.line == focus {
				model.scroll.focus = index
				break
			}
		}
	}
	if model.mode == richOutcomeMode && model.outcomeOverflows() {
		model.outcomeReading = true
	}
	if model.scroll.reveal {
		model.scroll.revealFocus()
		// Keep validation feedback next to the input when it fits, without
		// moving the input itself off-screen for a very long error message.
		if form, ok := model.form.(*richHuhForm); ok && len(form.form.Errors()) > 0 {
			end := len(model.scroll.anchors) - 1
			if end-model.scroll.focus < model.scroll.viewport.Height() {
				model.scroll.viewport.SetYOffset(max(model.scroll.viewport.YOffset(), end-model.scroll.viewport.Height()+1))
			}
		}
		model.scroll.reveal = false
	}
}

func (model *richRootModel) scrollBlocks(width int) []scrollBlock {
	styles := richStyles(model.color)
	var blocks []scrollBlock
	// The full command target remains accessible even when the fixed bar is
	// abbreviated. Metadata and status rows share the body's scroll position.
	if ansi.StringWidth(stripTerminalControl(model.consoleCommand()+" | "+model.console.Target+" | "+model.consoleStatusLabel())) > width {
		blocks = append(blocks, scrollBlock{"identity", stripTerminalControl(model.consoleCommand() + " · " + model.console.Target)})
	}
	for i, field := range model.console.Metadata {
		blocks = append(blocks, scrollBlock{fmt.Sprintf("metadata-%d", i), styles[VisualRoleMuted].Render(stripTerminalControl(field.Label)+" ") + stripTerminalControl(field.Value)})
	}
	if model.consoleWideLayout() {
		// Retain the established table on wide terminals, wrapping each cell.
		blocks = append(blocks, scrollBlock{"heading", styles[VisualRoleTitle].Render(consolePad("STATE", 12) + consolePad("PHASE", 27) + "DETAIL")})
		for i, row := range model.consoleRows() {
			blocks = append(blocks, scrollBlock{fmt.Sprintf("row-%d", i), consoleWrappedRow(row, width, styles)})
		}
	} else {
		blocks = append(blocks, scrollBlock{"heading", styles[VisualRoleTitle].Render("STATE / PHASE / DETAIL")})
		for i, row := range model.consoleRows() {
			blocks = append(blocks, scrollBlock{fmt.Sprintf("row-%d", i), model.consoleCompactRow(row, 0, styles)})
		}
	}
	for i, document := range model.notices {
		if text := strings.TrimSuffix(renderRich(document, RichOptions{Color: model.color}), "\n"); text != "" {
			blocks = append(blocks, scrollBlock{fmt.Sprintf("notice-%d", i), text})
		}
	}
	if active := model.consoleActiveView(0); active != "" {
		blocks = append(blocks, scrollBlock{"separator", ""}, scrollBlock{"form", active})
	}
	return blocks
}

func (model *richRootModel) handleScroll(message tea.Msg) bool {
	v := &model.scroll.viewport
	handled := false
	switch value := message.(type) {
	case tea.MouseWheelMsg:
		if model.mouseDisabled || value.X < 1 || value.X >= model.width-1 || value.Y < 1 || value.Y >= 1+v.Height() {
			return true
		}
		switch value.Button {
		case tea.MouseWheelUp:
			v.ScrollUp(3)
			handled = true
		case tea.MouseWheelDown:
			v.ScrollDown(3)
			handled = true
		}
	case tea.KeyPressMsg:
		key := value.String()
		if model.form != nil {
			// These bindings do not conflict with text editing or list selection.
			switch key {
			case "alt+up":
				v.ScrollUp(1)
				handled = true
			case "alt+down":
				v.ScrollDown(1)
				handled = true
			}
		} else {
			switch key {
			case "up":
				v.ScrollUp(1)
				handled = true
			case "down":
				v.ScrollDown(1)
				handled = true
			case "home":
				v.GotoTop()
				handled = true
			case "end":
				v.GotoBottom()
				handled = true
			}
		}
	}
	if handled {
		if model.track != nil {
			model.track.cancelArmed = false
		}
		model.scroll.following = model.form == nil && v.AtBottom()
		if model.scroll.following {
			model.scroll.unread = 0
		}
		if model.mode == richOutcomeMode {
			model.outcomeReading = true
		}
	}
	return handled
}

func consoleWrappedRow(row consoleStatusRow, width int, styles map[VisualRole]lipgloss.Style) string {
	glyph, label := consoleStateLabel(row.state)
	state := styles[consoleStateRole(row.state)].Render(consolePad(glyph+" "+label, 12))
	phase := strings.Split(ansi.Hardwrap(stripTerminalControl(row.phase), 26, true), "\n")
	detail := strings.Split(ansi.Hardwrap(stripTerminalControl(row.detail), max(width-39, 1), true), "\n")
	var lines []string
	for i := 0; i < max(len(phase), len(detail)); i++ {
		left, right := "", ""
		if i < len(phase) {
			left = phase[i]
		}
		if i < len(detail) {
			right = detail[i]
		}
		lines = append(lines, state+styles[VisualRolePlain].Render(consolePad(left, 27))+styles[VisualRoleMuted].Render(right))
		state = strings.Repeat(" ", 12)
	}
	return strings.Join(lines, "\n")
}

func (model *richRootModel) outcomeOverflows() bool {
	lines := 0
	for _, anchor := range model.scroll.anchors {
		if anchor.block == "form" {
			lines++
		}
	}
	return model.tooSmall() || lines > model.scroll.viewport.Height()
}
