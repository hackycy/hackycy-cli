package terminal

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"
)

type richFormModel interface {
	tea.Model
	configure(width int)
	handlesEscape() bool
}

func newRichForm(handler *InteractionHandler, request InteractionRequest, id uint64) (richFormModel, func() InteractionAnswer, error) {
	form, answer, err := handler.huhForm(request)
	if err != nil {
		return nil, nil, err
	}
	var selectionOrder *multiSelectionOrder
	if request.Kind == InteractionMultiSelect {
		selectionOrder = newMultiSelectionOrder(answer)
	}
	keyMap := huh.NewDefaultKeyMap()
	// Keep Select and MultiSelect on the same Huh-native two-step filter flow:
	// the first Enter commits filtering and the next Enter submits the answer.
	keyMap.Select.SetFilter.SetKeys("enter", "esc")
	form.WithKeyMap(keyMap)
	form.WithTheme(bHuhTheme(handler.capabilities.Stderr.Color))
	form.SubmitCmd = func() tea.Msg { return richFormSubmittedMsg{id: id} }
	form.CancelCmd = func() tea.Msg { return richFormCancelledMsg{id: id} }
	return &richHuhForm{form: form, selectionOrder: selectionOrder, request: request, color: handler.capabilities.Stderr.Color}, func() InteractionAnswer {
		if selectionOrder != nil {
			return selectionOrder.answer()
		}
		return answer()
	}, nil
}

type richHuhForm struct {
	form           *huh.Form
	selectionOrder *multiSelectionOrder
	request        InteractionRequest
	width          int
	color          bool
}

func (form *richHuhForm) Init() tea.Cmd {
	return form.form.Init()
}

func (form *richHuhForm) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	updated, command := form.form.Update(message)
	form.form = updated.(*huh.Form)
	if form.selectionOrder != nil {
		form.selectionOrder.observe(form.form.GetFocusedField().GetValue())
	}
	return form, command
}

// Render the focused field directly: Huh's group and list viewports otherwise
// clip content before the root viewport ever receives it. Huh still owns all
// editing, filtering, validation, and submission state.
func (form *richHuhForm) View() tea.View {
	lines := strings.Split(form.form.GetFocusedField().View(), "\n")
	for len(lines) > 0 && strings.Trim(ansi.Strip(lines[len(lines)-1]), " ─│╰╯") == "" {
		lines = lines[:len(lines)-1]
	}
	content := strings.Join(lines, "\n")
	for _, err := range form.form.Errors() {
		content += "\n" + richStyles(form.color)[VisualRoleError].Render(ansi.Hardwrap(stripTerminalControl(err.Error()), max(form.width, 1), true))
	}

	return tea.NewView(content)
}

func (form *richHuhForm) configure(width int) {
	form.width = width
	form.form.WithWidth(width).WithShowHelp(false)
	field := form.form.GetFocusedField()
	field.WithHeight(0)
	if form.request.Kind == InteractionMultiSelect {
		// Huh's unbounded MultiSelect subtracts its header from the option
		// height, so explicitly budget every wrapped option plus the header.
		height := 0
		for _, text := range []string{form.request.Message, form.request.Description} {
			if text != "" {
				height += lineCount(ansi.Hardwrap(stripTerminalControl(text), width, true))
			}
		}
		for _, option := range huhOptions(form.request.Options) {
			height += lineCount(ansi.Hardwrap(option.Key, max(width-4, 1), true))
		}
		field.WithHeight(max(height, 1))
	}
}

func (form *richHuhForm) focusLine() int {
	lines := strings.Split(ansi.Strip(form.form.GetFocusedField().View()), "\n")
	if form.handlesEscape() {
		return 0 // Huh places the active filter in the field title.
	}
	if form.request.Kind == InteractionSelect || form.request.Kind == InteractionMultiSelect {
		for index, line := range lines {
			if strings.HasPrefix(line, "◆ ") {
				return index
			}
		}
		return 0
	}
	// The input or confirmation buttons are below the title and description,
	// above the theme's trailing padding and bottom rule.
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.Trim(lines[index], " ─│╰╯") != "" {
			return index
		}
	}
	return 0
}

func (form *richHuhForm) help() string {
	if form.handlesEscape() {
		return "type filter · enter apply"
	}
	switch form.request.Kind {
	case InteractionSelect:
		return "↑/↓ select · / filter · enter confirm"
	case InteractionMultiSelect:
		return "multiple selection · ↑/↓ · space toggle · / filter · enter confirm"
	case InteractionConfirm:
		return "confirmation · ←/→ · enter confirm"
	default:
		return "text input · enter submit"
	}
}

func (form *richHuhForm) handlesEscape() bool {
	field, ok := form.form.GetFocusedField().(interface{ GetFiltering() bool })
	return ok && field.GetFiltering()
}

// multiSelectionOrder preserves the user's selection order because Huh's
// MultiSelect accessor intentionally returns values in option order.
type multiSelectionOrder struct {
	value func() InteractionAnswer
	order []string
}

func newMultiSelectionOrder(value func() InteractionAnswer) *multiSelectionOrder {
	tracker := &multiSelectionOrder{value: value}
	tracker.observe(value().Values)
	return tracker
}

func (tracker *multiSelectionOrder) observe(raw any) {
	values, ok := raw.([]string)
	if !ok {
		return
	}
	for _, value := range values {
		if !slices.Contains(tracker.order, value) {
			tracker.order = append(tracker.order, value)
		}
	}
	tracker.order = slices.DeleteFunc(tracker.order, func(value string) bool {
		return !slices.Contains(values, value)
	})
}

func (tracker *multiSelectionOrder) answer() InteractionAnswer {
	current := tracker.value()
	tracker.observe(current.Values)
	selected := make([]string, 0, len(tracker.order))
	for _, value := range tracker.order {
		if slices.Contains(current.Values, value) {
			selected = append(selected, value)
		}
	}
	return InteractionAnswer{Values: selected}
}
