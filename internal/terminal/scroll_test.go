package terminal

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestConsoleScrollReachesEntireNoticeHistory(t *testing.T) {
	model := newRichRootModel(60, 15, false)
	for i := range 30 {
		model.Update(richNoticeMsg{document: PresentationDocument{Blocks: []PresentationBlock{{Text: fmt.Sprintf("history-%02d", i)}}}, ack: make(chan struct{})})
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if !strings.Contains(model.View().Content, "history-00") {
		t.Fatalf("earliest content cannot be reached: %s", model.View().Content)
	}
	for range 40 {
		model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 5, Y: 5})
	}
	if !strings.Contains(model.View().Content, "history-29") {
		t.Fatalf("last content cannot be reached with wheel: %s", model.View().Content)
	}
}

func TestConsoleScrollPausesAndResumesFollow(t *testing.T) {
	model := newRichRootModel(60, 15, false)
	appendNotice := func(text string) {
		model.Update(richNoticeMsg{document: PresentationDocument{Blocks: []PresentationBlock{{Text: text}}}, ack: make(chan struct{})})
	}
	appendNotice(strings.Repeat("older\n", 30) + "tail")
	if !strings.Contains(model.View().Content, "tail") {
		t.Fatal("not following latest output")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	before := model.scroll.viewport.YOffset()
	appendNotice("new-entry")
	if model.scroll.viewport.YOffset() != before || model.scroll.following || model.scroll.unread == 0 {
		t.Fatalf("new output moved paused reader: %#v", model.scroll)
	}
	if !strings.Contains(model.View().Content, "End follow") {
		t.Fatal("missing paused-follow hint")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if !model.scroll.following || model.scroll.unread != 0 || !strings.Contains(model.View().Content, "new-entry") {
		t.Fatal("End did not resume following")
	}
}

func scrollTestForm(t *testing.T, kind InteractionKind, width, height int) (*richRootModel, *richHuhForm) {
	t.Helper()
	options := make([]InteractionOption, 100)
	for i := range options {
		options[i] = InteractionOption{Label: fmt.Sprintf("item-%03d", i), Value: fmt.Sprint(i)}
	}
	form, answer, err := newRichForm(NewInteractionHandler(InteractionOptions{}), InteractionRequest{
		Kind: kind, Message: "Choose", Options: options,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	model := newRichRootModel(width, height, false)
	model.Update(richShowFormMsg{id: 1, form: form, answer: answer, response: make(chan richAskResult, 1), ack: make(chan struct{})})
	return model, form.(*richHuhForm)
}

func TestConsoleScrollListWheelDoesNotSelectOrSubmitHiddenItem(t *testing.T) {
	for _, kind := range []InteractionKind{InteractionSelect, InteractionMultiSelect} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			model, form := scrollTestForm(t, kind, 60, 15)
			before := fmt.Sprint(form.form.GetFocusedField().GetValue())
			for range 40 {
				model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 4, Y: 4})
			}
			if !strings.Contains(model.View().Content, "item-099") {
				t.Fatalf("last item missing: %s", model.View().Content)
			}
			if got := fmt.Sprint(form.form.GetFocusedField().GetValue()); got != before {
				t.Fatalf("wheel changed selection from %s to %s", before, got)
			}
			_, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd != nil {
				t.Fatal("hidden item was submitted")
			}
			if !model.scroll.focusVisible() || !strings.Contains(model.View().Content, "item-000") {
				t.Fatal("first Enter did not reveal selection")
			}
			_, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("visible item could not be submitted")
			}
		})
	}
}

func TestConsoleScrollListNavigationAndResizeKeepsSelectionVisible(t *testing.T) {
	for _, kind := range []InteractionKind{InteractionSelect, InteractionMultiSelect} {
		model, _ := scrollTestForm(t, kind, 120, 40)
		for range 70 {
			model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		for _, size := range [][2]int{{80, 24}, {60, 15}, {10, 3}, {120, 40}} {
			model.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := model.View().Content
			if lineCount(view) > size[1] {
				t.Fatalf("height overflow %dx%d: %s", size[0], size[1], view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("width overflow %dx%d: %s", size[0], size[1], line)
				}
			}
			if !model.tooSmall() && !strings.Contains(view, "item-070") {
				t.Fatalf("selection missing after resize: %s", view)
			}
		}
	}
}

func TestScrollAnchorPreservesLogicalTextOnReflow(t *testing.T) {
	scroll := newConsoleScroll()
	scroll.following = false
	blocks := []scrollBlock{{"first", strings.Repeat("中文长路径", 120)}, {"second", "tail"}}
	scroll.setContent(blocks, 60, 8)
	scroll.viewport.SetYOffset(4)
	anchor := scroll.anchors[4]
	scroll.setContent(blocks, 30, 8)
	got := scroll.anchors[scroll.viewport.YOffset()]
	if got != anchor {
		t.Fatalf("reflow anchor = %#v, want %#v", got, anchor)
	}
}

func TestConsoleScrollLongOutcomeWaitsForReader(t *testing.T) {
	model := newRichRootModel(60, 15, false)
	model.Update(richShowOutcomeMsg{request: FinishRequest{Outcome: Succeeded, Summary: PresentationDocument{Blocks: []PresentationBlock{{Text: strings.Repeat("result\n", 50) + "final-result"}}}}, ack: make(chan struct{})})
	_, cmd := model.Update(richOutcomeElapsedMsg{})
	if cmd != nil {
		t.Fatal("long outcome vanished before it could be read")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if !strings.Contains(model.View().Content, "final-result") {
		t.Fatal("last result not reachable")
	}
	_, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not close result")
	}
}

func TestConsoleScrollCompletionPreservesPausedHistory(t *testing.T) {
	for _, summary := range []string{"done", strings.Repeat("result\n", 50)} {
		model := newRichRootModel(60, 15, false)
		model.Update(richNoticeMsg{document: PresentationDocument{Blocks: []PresentationBlock{{Text: strings.Repeat("history\n", 40)}}}, ack: make(chan struct{})})
		model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
		for range 4 {
			model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		before := model.scroll.anchors[model.scroll.viewport.YOffset()]
		model.Update(richShowOutcomeMsg{request: FinishRequest{Outcome: Succeeded, Summary: PresentationDocument{Blocks: []PresentationBlock{{Text: summary}}}}, ack: make(chan struct{})})
		if got := model.scroll.anchors[model.scroll.viewport.YOffset()]; got != before {
			t.Fatalf("completion moved history reader from %#v to %#v", before, got)
		}
		if _, cmd := model.Update(richOutcomeElapsedMsg{}); cmd != nil {
			t.Fatal("completion closed while reading history")
		}
		model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
		if _, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
			t.Fatal("reader could not dismiss completed outcome")
		}
	}
}

func TestConsoleScrollMultiSelectPreservesSelectAll(t *testing.T) {
	model, form := scrollTestForm(t, InteractionMultiSelect, 80, 24)
	model.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if got := len(form.selectionOrder.answer().Values); got != 100 {
		t.Fatalf("selected %d, want 100", got)
	}
}

func TestConsoleScrollInputAndValidationRemainVisible(t *testing.T) {
	form, answer, err := newRichForm(NewInteractionHandler(InteractionOptions{}), InteractionRequest{
		Kind: InteractionText, Message: "Project path", Description: strings.Repeat("中文说明和长路径/", 80),
		Validate: func(InteractionAnswer) error { return fmt.Errorf("invalid path\nchoose an existing directory") },
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	model := newRichRootModel(60, 15, false)
	model.Update(richShowFormMsg{id: 1, form: form, answer: answer, response: make(chan richAskResult, 1), ack: make(chan struct{})})
	for range 40 {
		model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp, X: 4, Y: 4})
	}
	model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if answer().Value != "x" || !model.scroll.focusVisible() {
		t.Fatal("typing did not restore input focus")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := model.View().Content
	if !strings.Contains(view, "invalid path") || !strings.Contains(view, "existing directory") || !model.scroll.focusVisible() {
		t.Fatalf("input/error hidden: %s", view)
	}
	t.Logf("60x15 input with validation:\n%s", view)
}

func TestConsoleScrollMouseModeCanBeReleasedForCopy(t *testing.T) {
	model := newRichRootModel(60, 15, false)
	if model.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("wheel reporting disabled")
	}
	model.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if model.View().MouseMode != tea.MouseModeNone {
		t.Fatal("copy mode still captures mouse")
	}
	if !strings.Contains(model.View().Content, "ctrl+g mouse") {
		t.Fatal("missing return-to-mouse hint")
	}
	model.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if model.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse reporting not restored")
	}
}

func TestConsoleScrollFramesFitAtBoundarySizes(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 15}, {30, 10}} {
		for _, kind := range []InteractionKind{InteractionText, InteractionConfirm, InteractionSelect, InteractionMultiSelect} {
			model, _ := scrollTestForm(t, kind, size[0], size[1])
			view := model.View().Content
			if lineCount(view) != size[1] {
				t.Fatalf("height %d, want %d: %s", lineCount(view), size[1], view)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > size[0] {
					t.Fatalf("line exceeds width %d: %s", size[0], line)
				}
			}
		}
	}
}

func TestConsoleScrollOutcomePausesForCopyAndSmallWindow(t *testing.T) {
	for _, message := range []tea.Msg{tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}, tea.WindowSizeMsg{Width: 10, Height: 3}} {
		model := newRichRootModel(80, 24, false)
		model.Update(richShowOutcomeMsg{request: FinishRequest{Outcome: Succeeded}, ack: make(chan struct{})})
		model.Update(message)
		_, cmd := model.Update(richOutcomeElapsedMsg{})
		if cmd != nil {
			t.Fatalf("outcome closed while user was reading after %T", message)
		}
	}
}

func TestConsoleScrollDisarmsPendingEscapeCancellation(t *testing.T) {
	model := newRichRootModel(60, 15, false)
	cancelled := false
	model.Update(richStartTrackMsg{label: "Work", requestCancel: func() error { cancelled = true; return nil }, ack: make(chan struct{})})
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cancelled {
		t.Fatal("scrolling left an old Escape armed")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !cancelled {
		t.Fatal("consecutive Escape presses no longer cancel")
	}
}
