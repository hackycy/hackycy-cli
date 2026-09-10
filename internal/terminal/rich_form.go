package terminal

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

type richFormModel interface {
	tea.Model
	configure(width, height int, showHelp bool)
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
	return &richHuhForm{form: form, selectionOrder: selectionOrder}, func() InteractionAnswer {
		if selectionOrder != nil {
			return selectionOrder.answer()
		}
		return answer()
	}, nil
}

type richHuhForm struct {
	form           *huh.Form
	selectionOrder *multiSelectionOrder
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

func (form *richHuhForm) View() tea.View {
	return tea.NewView(form.form.View())
}

func (form *richHuhForm) configure(width, height int, showHelp bool) {
	form.form.WithWidth(width).WithHeight(height).WithShowHelp(showHelp)
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
