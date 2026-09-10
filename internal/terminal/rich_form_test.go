package terminal

import (
	"fmt"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

func TestNewRichFormUsesAHuhChildForEveryInteractionKind(t *testing.T) {
	handler := NewInteractionHandler(InteractionOptions{})
	requests := []InteractionRequest{
		{Kind: InteractionText, Message: "Text"},
		{Kind: InteractionSecret, Message: "Secret"},
		{Kind: InteractionSelect, Message: "Select", Options: []InteractionOption{{Label: "One", Value: "one", Description: "First option"}}},
		{Kind: InteractionMultiSelect, Message: "Multi-select", Options: []InteractionOption{{Label: "One", Value: "one", Description: "First option"}}},
		{Kind: InteractionConfirm, Message: "Confirm"},
	}

	for index, request := range requests {
		t.Run(request.Message, func(t *testing.T) {
			form, _, err := newRichForm(handler, request, uint64(index+1))
			if err != nil {
				t.Fatalf("newRichForm() error = %v", err)
			}
			if _, ok := form.(*richHuhForm); !ok {
				t.Fatalf("newRichForm() = %T, want *richHuhForm", form)
			}
		})
	}
}

func TestRichHuhFormLetsHuhEndListFiltering(t *testing.T) {
	for _, kind := range []InteractionKind{InteractionSelect, InteractionMultiSelect} {
		t.Run(fmt.Sprintf("%d", kind), func(t *testing.T) {
			form, _, err := newRichForm(NewInteractionHandler(InteractionOptions{}), InteractionRequest{
				Kind:    kind,
				Message: "Choose",
				Options: []InteractionOption{{Label: "One", Value: "one"}},
			}, 1)
			if err != nil {
				t.Fatalf("newRichForm() error = %v", err)
			}
			huhForm := form.(*richHuhForm)

			updated, _ := huhForm.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
			huhForm = updated.(*richHuhForm)
			filter, ok := huhForm.form.GetFocusedField().(interface{ GetFiltering() bool })
			if !ok || !filter.GetFiltering() {
				t.Fatal("Huh list did not enter filtering state")
			}

			updated, _ = huhForm.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			huhForm = updated.(*richHuhForm)
			filter = huhForm.form.GetFocusedField().(interface{ GetFiltering() bool })
			if filter.GetFiltering() {
				t.Fatal("Huh list remained filtering after Escape")
			}
		})
	}
}

func TestRichHuhFormSequenceEndsListAndSubmits(t *testing.T) {
	form, _, err := newRichForm(NewInteractionHandler(InteractionOptions{}), InteractionRequest{
		Kind:    InteractionSelect,
		Message: "Choose",
		Options: []InteractionOption{{Label: "item-173", Value: "item-173"}},
	}, 1)
	if err != nil {
		t.Fatalf("newRichForm() error = %v", err)
	}
	huhForm := form.(*richHuhForm)
	apply := func(message tea.Msg) {
		updated, _ := huhForm.Update(message)
		huhForm = updated.(*richHuhForm)
	}
	apply(tea.KeyPressMsg{Code: '/', Text: "/"})
	apply(tea.KeyPressMsg{Code: 'i', Text: "i"})
	apply(tea.KeyPressMsg{Code: 't', Text: "t"})
	apply(tea.KeyPressMsg{Code: 'e', Text: "e"})
	apply(tea.KeyPressMsg{Code: 'm', Text: "m"})
	apply(tea.KeyPressMsg{Code: '-', Text: "-"})
	apply(tea.KeyPressMsg{Code: '1', Text: "1"})
	apply(tea.KeyPressMsg{Code: '7', Text: "7"})
	apply(tea.KeyPressMsg{Code: '3', Text: "3"})
	apply(tea.KeyPressMsg{Code: tea.KeyEnter})
	if filter := huhForm.form.GetFocusedField().(interface{ GetFiltering() bool }); filter.GetFiltering() {
		t.Fatal("list remained filtering after Enter")
	}
	if huhForm.form.State != huh.StateNormal {
		t.Fatalf("form state after committing filter = %v, want normal", huhForm.form.State)
	}
	updated, command := huhForm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	huhForm = updated.(*richHuhForm)
	if command != nil {
		updated, command = huhForm.Update(command())
		huhForm = updated.(*richHuhForm)
		if command != nil {
			updated, _ = huhForm.Update(command())
			huhForm = updated.(*richHuhForm)
		}
	}
	if huhForm.form.State != huh.StateCompleted {
		t.Fatalf("form state = %v, want completed", huhForm.form.State)
	}
}

func TestMultiSelectionOrderTracksReselectionOrder(t *testing.T) {
	current := InteractionAnswer{Values: []string{"one"}}
	tracker := newMultiSelectionOrder(func() InteractionAnswer { return current })

	// Huh reports selected values in option order, even when the user selected
	// them in a different order.
	current.Values = []string{"one", "two"}
	tracker.observe(current.Values)
	current.Values = []string{"two"}
	tracker.observe(current.Values)
	current.Values = []string{"one", "two"}
	tracker.observe(current.Values)

	if got, want := tracker.answer().Values, []string{"two", "one"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selection order = %#v, want %#v", got, want)
	}
}

func TestHuhOptionsPreserveValuesAndProjectDescriptions(t *testing.T) {
	options := huhOptions([]InteractionOption{{
		Label:       "Production\x1b[31m",
		Value:       "prod",
		Description: "Shared\tdeployment environment\x01",
	}})
	if len(options) != 1 {
		t.Fatalf("option count = %d, want 1", len(options))
	}
	if got, want := options[0].Value, "prod"; got != want {
		t.Fatalf("option value = %q, want %q", got, want)
	}
	if got, want := options[0].Key, "Production - Shared\tdeployment environment�"; got != want {
		t.Fatalf("option key = %q, want %q", got, want)
	}
}
