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
	toggle := "ctrl+g copy"
	if model.mouseDisabled {
		toggle = "ctrl+g mouse"
	}
	lines := []string{model.scrollHelp() + " · " + toggle}
	overflow := v.TotalLineCount() > v.Height()
	paused := model.form == nil && model.mode != richOutcomeMode && !model.scroll.following
	if overflow || paused || model.scroll.unread > 0 {
		up, down := "─", "─"
		if !v.AtTop() {
			up = "↑"
		}
		if !v.AtBottom() {
			down = "↓"
		}
		status := fmt.Sprintf("%s %d–%d/%d %s", up, v.YOffset()+1, min(v.YOffset()+v.Height(), v.TotalLineCount()), v.TotalLineCount(), down)
		if overflow && !model.mouseDisabled {
			status += " · wheel"
		}
		if model.form == nil && model.mode != richOutcomeMode {
			if model.scroll.following {
				status += " · following"
			} else {
				status += fmt.Sprintf(" · +%d new · End follow", model.scroll.unread)
			}
		}
		lines = append([]string{status}, lines...)
	}
	return ansi.Wrap(strings.Join(lines, "\n"), model.formWidth(), "")
}

func (model *richRootModel) prepareScroll() {
	if model.tooSmall() {
		return
	}
	width := model.formWidth()
	bodyTop := model.focusBodyTop()
	// Reserve the footer's actual wrapped height. The longest status includes
	// paused-follow information, so it cannot push the input out of the view.
	footerHeight := max(lineCount(model.scrollFooter()), 1)
	height := max(model.height-bodyTop-footerHeight, 1)
	blocks := model.scrollBlocks(width)
	model.scroll.setContent(blocks, width, height)
	// A change in digit count or follow state can add one footer line.
	height = max(model.height-bodyTop-lineCount(model.scrollFooter()), 1)
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
	// Keep the full command target accessible when the fixed header abbreviates it.
	if ansi.StringWidth(stripTerminalControl(model.consoleCommand()+"  "+model.console.Target+"  "+model.consoleStatusLabel())) > width {
		blocks = append(blocks, scrollBlock{"identity", stripTerminalControl(model.consoleCommand() + " · " + model.console.Target)})
	}
	if metadata := consoleMetadataText(model.console.Metadata, width, styles[VisualRoleMuted]); metadata != "" {
		blocks = append(blocks, scrollBlock{"metadata", metadata})
	}
	for i, document := range model.notices {
		if text := strings.TrimSuffix(renderRich(document, RichOptions{Color: model.color}), "\n"); text != "" {
			if len(blocks) > 0 {
				blocks = append(blocks, scrollBlock{fmt.Sprintf("before-notice-%d", i), ""})
			}
			blocks = append(blocks, scrollBlock{fmt.Sprintf("notice-%d", i), text})
		}
	}
	if active := model.consoleActiveView(0); active != "" {
		if len(blocks) > 0 {
			blocks = append(blocks, scrollBlock{"before-current", ""})
		}
		blocks = append(blocks, scrollBlock{"current-heading", model.currentHeading(width)})
		blocks = append(blocks, scrollBlock{"form", active})
	}
	return blocks
}

func (model *richRootModel) currentHeading(width int) string {
	name := "Input"
	switch model.mode {
	case richFormMode:
		for _, step := range model.formRows {
			if step.id == model.formID && step.name != "" {
				name = step.name
				break
			}
		}
	case richTrackMode:
		name = "Work"
	case richOutcomeMode:
		name = "Result"
	}
	label := ansi.Truncate("── "+stripTerminalControl(name)+" ", width, "…")
	line := label + strings.Repeat("─", max(width-ansi.StringWidth(label), 0))
	return focusThemeEmphasis(focusThemeStyle(model.color, focusAccent), model.color).Render(line)
}

func consoleMetadataText(fields []ConsoleMetadata, width int, muted lipgloss.Style) string {
	const separator = " · "
	var output strings.Builder
	lineWidth := 0
	for _, field := range fields {
		item := muted.Render(stripTerminalControl(field.Label)+":") + " " + stripTerminalControl(field.Value)
		itemWidth := ansi.StringWidth(item)
		separatorWidth := ansi.StringWidth(separator)
		if lineWidth > 0 && width > 0 && lineWidth+separatorWidth+itemWidth > width {
			output.WriteByte('\n')
			lineWidth = 0
		}
		if lineWidth > 0 {
			output.WriteString(muted.Render(separator))
			lineWidth += separatorWidth
		}
		output.WriteString(item)
		lineWidth += itemWidth
	}
	return output.String()
}

func (model *richRootModel) handleScroll(message tea.Msg) bool {
	v := &model.scroll.viewport
	handled := false
	switch value := message.(type) {
	case tea.MouseWheelMsg:
		top := model.focusBodyTop()
		if model.mouseDisabled || value.X < 0 || value.X >= model.width || value.Y < top || value.Y >= top+v.Height() {
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

func (model *richRootModel) outcomeOverflows() bool {
	lines := 0
	for _, anchor := range model.scroll.anchors {
		if anchor.block == "form" {
			lines++
		}
	}
	return model.tooSmall() || lines > model.scroll.viewport.Height()
}
