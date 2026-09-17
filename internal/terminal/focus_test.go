package terminal

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestFocusTrailFollowsTheRequestedFormAndHidesTerminalOutcome(t *testing.T) {
	model := newRichRootModelWithConsole(120, 40, false, ConsoleDescriptor{
		Command: "YCY / config",
		FormCatalog: []ConsoleFormStep{
			{ID: "workspace", Name: "Workspace"},
			{ID: "credential", Name: "Credential"},
			{ID: "provider", Name: "Provider"},
			{ID: "capabilities", Name: "Capabilities"},
			{ID: "confirm", Name: "Confirm"},
		},
	})
	if got, want := model.focusTrail(116), "◆ Workspace  ─  ○ Credential  ─  ○ Provider"; got != want {
		t.Fatalf("initial trail = %q, want %q", got, want)
	}
	model.mode = richFormMode
	model.formID = 7
	model.formRows[2].id = 7
	model.formRows[1].state = PhaseCompleted
	model.formRows[2].state = PhaseActive
	if got, want := model.focusTrail(116), "✓ Credential  ─  ◆ Provider  ─  ○ Capabilities"; got != want {
		t.Fatalf("requested field trail = %q, want %q", got, want)
	}
	if got := model.focusTrail(12); got != "◆ Provider" {
		t.Fatalf("narrow trail did not prioritize current field: %q", got)
	}
	model.mode = richOutcomeMode
	if got := model.focusTrail(116); got != "" {
		t.Fatalf("outcome retained progress trail: %q", got)
	}
}

func TestFocusTrailUsesOnlyTheActiveWorkCatalog(t *testing.T) {
	model := newRichRootModel(80, 24, false)
	if got := model.focusTrail(76); got != "" {
		t.Fatalf("catalog-free console invented a trail: %q", got)
	}
	model.mode = richTrackMode
	model.track = &trackedState{phases: []OperationPhase{
		{ID: "read", Name: "Read", State: PhaseCompleted},
		{ID: "write", Name: "Write", State: PhaseActive},
		{ID: "finish", Name: "Finish", State: PhasePending},
	}}
	if got := model.focusTrail(76); got != "✓ Read  ─  ◆ Write  ─  ○ Finish" {
		t.Fatalf("work trail mixed catalogs: %q", got)
	}
	model.track.phases = nil
	if got := model.focusTrail(76); got != "" {
		t.Fatalf("catalog-free work reused old trail: %q", got)
	}
}

func TestFocusApplicationShellSeparatesTitleProgressAndBody(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, color := range []bool{false, true} {
			model := newRichRootModelWithConsole(size[0], size[1], color, ConsoleDescriptor{
				Command:     "YCY / configure",
				Target:      "terminal session",
				FormCatalog: []ConsoleFormStep{{ID: "first", Name: "First step"}},
			})
			model.mode = richFormMode
			rendered := model.focusShell()
			lines := strings.Split(ansi.Strip(rendered), "\n")
			if len(lines) != 3 || !strings.HasPrefix(lines[0], "YCY / configure  terminal session") || !strings.HasSuffix(lines[0], "ACTIVE") {
				t.Fatalf("%dx%d color=%t title bar = %q", size[0], size[1], color, rendered)
			}
			if ansi.StringWidth(lines[0]) != size[0] || lines[1] != strings.Repeat("─", size[0]) {
				t.Fatalf("%dx%d color=%t shell width = %q", size[0], size[1], color, rendered)
			}
			if lines[2] != "◆ First step" || model.focusBodyTop() != 4 {
				t.Fatalf("%dx%d color=%t progress band = %q", size[0], size[1], color, rendered)
			}
			viewLines := strings.Split(ansi.Strip(model.View().Content), "\n")
			if viewLines[3] != "" {
				t.Fatalf("%dx%d color=%t body gap is not fixed: %q", size[0], size[1], color, model.View().Content)
			}
			if !color && strings.Contains(rendered, "\x1b[") {
				t.Fatalf("no-color shell depends on ANSI styling: %q", rendered)
			}
			if color {
				title := richStyles(true)[VisualRoleTitle].Render("YCY / configure")
				divider := focusThemeStyle(true, focusDivider).Render(strings.Repeat("─", size[0]))
				active := focusThemeEmphasis(focusThemeStyle(true, focusAccent), true).Render("◆ First step")
				if !strings.Contains(rendered, title) || !strings.Contains(rendered, divider) || !strings.Contains(rendered, active) {
					t.Fatalf("colored shell does not use distinct roles: %q", rendered)
				}
			}
		}
	}
}

func TestFocusTitleBarPreservesCommandAndRightAlignsStatus(t *testing.T) {
	model := newRichRootModelWithConsole(80, 24, false, ConsoleDescriptor{
		Command: "YCY / configure",
		Target:  strings.Repeat("long-target/", 20),
	})
	model.mode = richTrackMode
	titleBar := strings.Split(model.focusShell(), "\n")[0]
	if ansi.StringWidth(titleBar) != 80 || !strings.HasPrefix(titleBar, "YCY / configure") || !strings.HasSuffix(titleBar, "ACTIVE") {
		t.Fatalf("title bar did not preserve its fixed edges: %q", titleBar)
	}
	if strings.Contains(titleBar, strings.Repeat("long-target/", 20)) {
		t.Fatalf("long target was not truncated before the command or status: %q", titleBar)
	}
}

func TestFocusHeaderStaysFixedWhileNoticeHistoryScrolls(t *testing.T) {
	model := newRichRootModel(80, 24, false)
	model.mode = richTrackMode
	model.track = &trackedState{label: "Configure", phases: make([]OperationPhase, 15)}
	for i := range model.track.phases {
		model.track.phases[i] = OperationPhase{ID: fmt.Sprint(i), Name: fmt.Sprintf("Step-%02d", i), Detail: fmt.Sprintf("Detail-%02d", i), State: PhaseCompleted}
	}
	model.track.phases[7].State = PhaseActive
	model.notices = []PresentationDocument{{Blocks: []PresentationBlock{{Text: strings.Repeat("history\n", 40)}}}}
	model.View()
	header := model.focusShell()
	model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	for i := 0; i < len(model.scroll.anchors); i++ {
		view := model.View().Content
		if model.focusShell() != header || !strings.Contains(view, "✓ Step-06  ─  ◆ Step-07  ─  ✓ Step-08") {
			t.Fatalf("scroll changed fixed trail: %s", view)
		}
		model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	view := model.View().Content
	for _, text := range []string{"STEPS", "Step-00", "Detail-00", "WORK IN PROGRESS"} {
		if strings.Contains(view, text) {
			t.Fatalf("active-only body retained %q: %s", text, view)
		}
	}
}

func TestFocusWheelOnlyScrollsInsideTheContentRegion(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 15}} {
		model := newRichRootModelWithConsole(size[0], size[1], false, ConsoleDescriptor{
			Command: "YCY", FormCatalog: []ConsoleFormStep{{ID: "one", Name: "First step"}},
		})
		model.notices = []PresentationDocument{{Blocks: []PresentationBlock{{Text: strings.Repeat("history\n", 60)}}}}
		model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
		top := model.focusBodyTop()
		for _, point := range [][2]int{{size[0], top}, {0, 0}, {0, top - 1}, {0, top + model.scroll.viewport.Height()}} {
			model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: point[0], Y: point[1]})
			if model.scroll.viewport.YOffset() != 0 {
				t.Fatalf("wheel outside content scrolled at %v for %v", point, size)
			}
		}
		model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 0, Y: top})
		if model.scroll.viewport.YOffset() != 3 {
			t.Fatal("wheel on first content cell did not scroll three lines")
		}
	}
}

func TestFocusFormsFitWithTrailAndUseTheAvailableWidth(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {70, 20}, {69, 19}, {60, 15}, {30, 10}} {
		for _, kind := range []InteractionKind{InteractionText, InteractionSecret, InteractionSelect, InteractionMultiSelect, InteractionConfirm} {
			t.Run(fmt.Sprintf("%dx%d/kind-%d", size[0], size[1], kind), func(t *testing.T) {
				model, form := scrollTestForm(t, kind, size[0], size[1])
				model.console.Command = "YCY / configure"
				model.console.Target = strings.Repeat("中文长路径/", 12)
				model.scroll.reveal = true
				view := model.View().Content
				if lineCount(view) != size[1] || model.scroll.viewport.Width() != size[0] || form.width != size[0] {
					t.Fatalf("incorrect frame dimensions: %s", view)
				}
				for _, line := range strings.Split(ansi.Strip(view), "\n") {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("line exceeds the terminal width: %q", line)
					}
				}
				if formView := ansi.Strip(form.View().Content); !strings.HasPrefix(formView, "Choose") {
					t.Fatalf("form title is not flush left: %q", formView)
				}
				plainForm := ansi.Strip(form.View().Content)
				controlPrefix := map[InteractionKind]string{
					InteractionText:        "> ",
					InteractionSecret:      "> ",
					InteractionSelect:      "◆ ",
					InteractionMultiSelect: "◆ ",
					InteractionConfirm:     "Yes",
				}[kind]
				if !hasLinePrefix(plainForm, controlPrefix) {
					t.Fatalf("kind %d control is not flush left with prefix %q: %q", kind, controlPrefix, plainForm)
				}
				if !model.scroll.focusVisible() {
					t.Fatalf("trail displaced focused control: %s", view)
				}
				for _, forbidden := range []string{"STEPS", "STATE / PHASE / DETAIL", "F2"} {
					if strings.Contains(view, forbidden) {
						t.Fatalf("obsolete UI %q leaked into Focus", forbidden)
					}
				}
			})
		}
	}
}

func hasLinePrefix(value, prefix string) bool {
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func TestFocusConfirmationButtonsAlignWithTheField(t *testing.T) {
	model, form := scrollTestForm(t, InteractionConfirm, 120, 40)
	form.request.Description = "A longer description that must not center the buttons"
	field := form.form.GetFocusedField()
	field.(*huh.Confirm).Description(form.request.Description)
	model.configureForm()
	lines := strings.Split(form.View().Content, "\n")
	buttons := lines[form.focusLine()]
	if !strings.HasPrefix(buttons, "Yes") {
		t.Fatalf("confirmation button moved from the left field edge: %q", buttons)
	}
}

func TestFocusConfirmationShowsNoColorFocusForBothChoices(t *testing.T) {
	model, form := scrollTestForm(t, InteractionConfirm, 80, 24)
	view := form.View().Content
	if strings.Contains(view, "\x1b[") || !strings.Contains(view, "[No]") {
		t.Fatalf("initial no-color confirmation focus = %q", view)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	view = form.View().Content
	if strings.Contains(view, "\x1b[") || !strings.Contains(view, "[Yes]") {
		t.Fatalf("moved no-color confirmation focus = %q", view)
	}
}

func TestFocusShellKeepsTheSameRhythmAcrossLayoutThresholds(t *testing.T) {
	for _, size := range [][2]int{{69, 19}, {70, 20}, {120, 40}} {
		model := newRichRootModelWithConsole(size[0], size[1], false, ConsoleDescriptor{
			Command: "YCY", FormCatalog: []ConsoleFormStep{{ID: "one", Name: "First"}},
		})
		if got := lineCount(model.focusShell()); got != 3 || model.focusBodyTop() != 4 {
			t.Fatalf("%dx%d shell height = %d/%d, want 3/4", size[0], size[1], got, model.focusBodyTop())
		}
	}
}

func TestFocusBodyGapIsNotScrollableContent(t *testing.T) {
	model, _ := scrollTestForm(t, InteractionText, 80, 24)
	blocks := model.scrollBlocks(model.formWidth())
	if len(blocks) == 0 || blocks[len(blocks)-1].id != "form" {
		t.Fatalf("active form is not the final scroll block: %#v", blocks)
	}
	for _, block := range blocks {
		if block.id == "separator" {
			t.Fatalf("fixed body gap remained in scroll blocks: %#v", blocks)
		}
	}
	model.View()
	for _, anchor := range model.scroll.anchors {
		if anchor.block == "separator" {
			t.Fatalf("fixed body gap entered scroll anchors: %#v", model.scroll.anchors)
		}
	}
}

func TestFocusOutcomeUsesTheHeaderAsItsOnlyStatusLabel(t *testing.T) {
	model := newRichRootModelWithConsole(80, 24, false, ConsoleDescriptor{
		Command: "YCY", FormCatalog: []ConsoleFormStep{{ID: "one", Name: "First"}},
	})
	model.Update(richShowOutcomeMsg{request: FinishRequest{
		Outcome:  Succeeded,
		Location: "write result",
		Summary:  PresentationDocument{Blocks: []PresentationBlock{{Text: "saved"}}},
	}, ack: make(chan struct{})})
	view := model.View().Content
	if strings.Count(view, "SUCCEEDED") != 1 || strings.Contains(view, "First") {
		t.Fatalf("outcome repeated status or trail: %q", view)
	}
	plainLines := strings.Split(ansi.Strip(view), "\n")
	if plainLines[1] != strings.Repeat("─", 80) || plainLines[2] != "" || model.focusBodyTop() != 3 {
		t.Fatalf("outcome did not retain shell separation: %q", view)
	}
	for _, text := range []string{"write result", "saved"} {
		if !strings.Contains(view, text) {
			t.Fatalf("outcome omitted %q: %q", text, view)
		}
	}
}
