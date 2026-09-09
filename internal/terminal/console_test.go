package terminal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestOpenConsoleNormalizesSafeBoundedDescriptorBeforeRichUse(t *testing.T) {
	runtime := NewExperience(ExperienceOptions{Capabilities: Capabilities{Interaction: RichInteractive}})
	run, err := runtime.OpenConsole(context.Background(), ConsoleDescriptor{
		Command:  "  YCY\x1b[31m CONFIG  ",
		Target:   "  profile\x01  ",
		Metadata: []ConsoleMetadata{{Label: " workspace ", Value: " repo\x1b[0m "}},
	})
	if err != nil {
		t.Fatalf("OpenConsole() error = %v", err)
	}

	concrete, ok := run.(*runtimeRun)
	if !ok {
		t.Fatalf("OpenConsole() = %T, want *runtimeRun", run)
	}
	if concrete.console.Command != "YCY CONFIG" || concrete.console.Target != "profile�" || concrete.console.Status != "READY" || len(concrete.console.Metadata) != 1 || concrete.console.Metadata[0] != (ConsoleMetadata{Label: "workspace", Value: "repo"}) {
		t.Fatalf("console descriptor = %#v", concrete.console)
	}
}

func TestOpenConsoleRichPreflightFailureFallsBackToPlain(t *testing.T) {
	var diagnostics bytes.Buffer
	runtime := NewExperience(ExperienceOptions{
		Capabilities: Capabilities{Interaction: RichInteractive},
		Input:        strings.NewReader("project\n"),
		Diagnostics:  &diagnostics,
	})
	run, err := runtime.OpenConsole(context.Background(), ConsoleDescriptor{
		Command: "YCY / config",
		FormCatalog: []ConsoleFormStep{
			{ID: "workspace", Name: "Workspace"},
		},
	})
	if err != nil {
		t.Fatalf("OpenConsole() error = %v", err)
	}
	concrete := run.(*runtimeRun)
	if concrete.controller != nil || !concrete.richDisabled {
		t.Fatalf("preflight fallback state = controller:%v disabled:%v", concrete.controller, concrete.richDisabled)
	}
	answer, err := run.Ask(InteractionRequest{Kind: InteractionText, Message: "Workspace", ConsoleStepID: "workspace"})
	if err != nil || answer.Value != "project" {
		t.Fatalf("Plain fallback Ask() = (%#v, %v)", answer, err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenConsoleRejectsInvalidDescriptorWithoutOpeningRun(t *testing.T) {
	runtime := NewExperience(ExperienceOptions{})
	_, err := runtime.OpenConsole(context.Background(), ConsoleDescriptor{Command: " "})
	if !errors.Is(err, ErrInvalidConsoleDescriptor) {
		t.Fatalf("OpenConsole() error = %v, want ErrInvalidConsoleDescriptor", err)
	}

	_, err = runtime.OpenConsole(context.Background(), ConsoleDescriptor{
		Command: "YCY",
		Metadata: []ConsoleMetadata{
			{Label: "one", Value: "1"}, {Label: "two", Value: "2"},
			{Label: "three", Value: "3"}, {Label: "four", Value: "4"},
			{Label: "five", Value: "5"},
		},
	})
	if !errors.Is(err, ErrInvalidConsoleDescriptor) {
		t.Fatalf("OpenConsole() metadata error = %v, want ErrInvalidConsoleDescriptor", err)
	}

	_, err = runtime.OpenConsole(context.Background(), ConsoleDescriptor{Command: strings.Repeat("x", maxConsoleField+1)})
	if !errors.Is(err, ErrInvalidConsoleDescriptor) {
		t.Fatalf("OpenConsole() size error = %v, want ErrInvalidConsoleDescriptor", err)
	}
}

func TestOpenUsesTheBCompatibleDefaultConsoleDescriptor(t *testing.T) {
	run := NewExperience(ExperienceOptions{}).Open(context.Background()).(*runtimeRun)
	if run.console.Command != "YCY" || run.console.Target != "terminal session" || run.console.Status != "READY" || len(run.console.Metadata) != 1 {
		t.Fatalf("default descriptor = %#v", run.console)
	}
}

func TestConsoleWideViewKeepsStableShellRegions(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command: "YCY CONFIG",
		Target:  "profile demo",
		Status:  "READY",
		Metadata: []ConsoleMetadata{
			{Label: "workspace", Value: "repo"},
			{Label: "provider", Value: "github"},
		},
	})
	model.mode = richTrackMode
	model.track = &trackedState{label: "sync", phases: []OperationPhase{
		{ID: "scan", Name: "Scan", State: PhaseCompleted, Detail: "repo"},
		{ID: "write", Name: "Write", State: PhaseActive, Detail: "pending"},
	}}
	view := model.View()
	if !view.AltScreen || !view.DisableBracketedPasteMode {
		t.Fatalf("wide view terminal flags = %#v", view)
	}
	for _, needle := range []string{"YCY CONFIG", "profile demo", "workspace repo", "provider github", "STATE", "PHASE", "DETAIL", "✓ DONE", "◆ ACTIVE", "Scan", "Write", "pending"} {
		if !strings.Contains(view.Content, needle) {
			t.Fatalf("wide view missing %q: %q", needle, view.Content)
		}
	}
	if strings.Contains(view.Content, "[done]") || strings.Contains(view.Content, "[active]") {
		t.Fatalf("wide view retained generic phase prefixes: %q", view.Content)
	}
}

func TestConsoleCompactSurfaceUsesTheBStatusHeading(t *testing.T) {
	model := newRichRootModelWithConsole(69, 30, false, defaultConsoleDescriptor())
	model.mode = richTrackMode
	model.track = &trackedState{label: "work", phases: []OperationPhase{{Name: "Phase", State: PhaseActive}}}
	view := model.View().Content
	if !strings.Contains(view, "STATE / PHASE / DETAIL") || !strings.Contains(view, "◆ ACTIVE · Phase") {
		t.Fatalf("compact surface omitted B status structure: %q", view)
	}
}

func TestConsoleCompactViewRetainsOrderedRowsAndActiveRegion(t *testing.T) {
	model := newRichRootModelWithConsole(48, 16, false, ConsoleDescriptor{
		Command:  "YCY GIT",
		Target:   "pulse",
		Metadata: []ConsoleMetadata{{Label: "scope", Value: "workspace"}},
	})
	model.mode = richTrackMode
	model.track = &trackedState{label: "Git Pulse", phases: []OperationPhase{
		{ID: "scan", Name: "Scan", State: PhaseCompleted, Detail: "2 repos"},
		{ID: "fetch", Name: "Fetch", State: PhaseActive, Detail: "commits"},
	}}
	view := model.View().Content
	for _, needle := range []string{"YCY GIT", "scope workspace", "STATE / PHASE / DETAIL", "✓ DONE · Scan · 2 repos", "◆ ACTIVE · Fetch · commits", "Fetch", "commits"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("compact view missing %q: %q", needle, view)
		}
	}
}

func TestConsoleInitialFormCatalogIsCompleteAndOrdered(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command: "YCY / config",
		Target:  "profile setup",
		FormCatalog: []ConsoleFormStep{
			{ID: "workspace", Name: "Workspace", Detail: "choose project"},
			{ID: "token", Name: "Access token", Detail: "credential", Sensitive: true},
			{ID: "confirm", Name: "Confirm", Detail: "apply changes"},
		},
	})

	if len(model.formRows) != 3 || len(model.statusRows) != 3 {
		t.Fatalf("initial catalog rows = (%d form, %d status), want three each", len(model.formRows), len(model.statusRows))
	}
	if model.formRows[0].state != PhaseActive || model.formRows[1].state != PhasePending || model.formRows[2].state != PhasePending {
		t.Fatalf("initial catalog states = %#v, want active then pending", model.formRows)
	}
	if model.formRows[1].detail != "[redacted]" {
		t.Fatalf("sensitive catalog detail = %q, want redacted", model.formRows[1].detail)
	}

	view := model.View().Content
	for _, needle := range []string{"Workspace", "choose project", "Access token", "[redacted]", "Confirm", "apply changes", "◆ ACTIVE", "○ PENDING"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("initial catalog view missing %q: %q", needle, view)
		}
	}
	workspace := strings.Index(view, "Workspace")
	token := strings.Index(view, "Access token")
	confirm := strings.Index(view, "Confirm")
	if workspace < 0 || token < workspace || confirm < token {
		t.Fatalf("initial catalog order = %q", view)
	}
}

func TestConsoleCatalogAskReusesExistingRows(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command: "YCY / config",
		FormCatalog: []ConsoleFormStep{
			{ID: "workspace", Name: "Workspace", Detail: "choose project"},
			{ID: "token", Name: "Access token", Detail: "credential", Sensitive: true},
		},
	})
	response := make(chan richAskResult, 1)
	_, _ = model.Update(richShowFormMsg{
		id:       1,
		form:     consoleTestForm{},
		answer:   func() InteractionAnswer { return InteractionAnswer{Value: "project"} },
		step:     consoleFormStep{catalogID: "workspace", id: 1, name: "Workspace", detail: "text input", state: PhaseActive},
		response: response,
		ack:      make(chan struct{}),
	})
	if len(model.formRows) != 2 || len(model.statusRows) != 2 {
		t.Fatalf("catalog rows after Ask = (%d form, %d status), want two each", len(model.formRows), len(model.statusRows))
	}
	if model.statusRows[0].state != PhaseActive || model.statusRows[1].state != PhasePending {
		t.Fatalf("catalog states after Ask = %#v", model.statusRows)
	}
	_, _ = model.Update(richFormSubmittedMsg{id: 1})
	<-response
	if model.statusRows[0].state != PhaseCompleted || !strings.Contains(model.View().Content, "Access token") {
		t.Fatalf("catalog row was not retained after completion: %#v\n%s", model.statusRows, model.View().Content)
	}
}

func TestConsoleTrackReplacesFormCatalogWithWorkCatalog(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command: "YCY / config",
		FormCatalog: []ConsoleFormStep{
			{ID: "workspace", Name: "Workspace", Detail: "choose project"},
			{ID: "confirm", Name: "Confirm", Detail: "apply changes"},
		},
	})

	_, _ = model.Update(richStartTrackMsg{
		label: "Apply profile",
		phases: []OperationPhase{
			{ID: "validate", Name: "Validate", State: PhasePending},
			{ID: "write", Name: "Write", State: PhasePending},
		},
		requestCancel: func() error { return nil },
		ack:           make(chan struct{}),
	})

	if len(model.formRows) != 0 || len(model.statusRows) != 2 {
		t.Fatalf("work replacement rows = (%d form, %d status), want no form and two work rows", len(model.formRows), len(model.statusRows))
	}
	if model.statusRows[0].phase != "Validate" || model.statusRows[1].phase != "Write" || model.statusRows[0].state != PhasePending || model.statusRows[1].state != PhasePending {
		t.Fatalf("work catalog = %#v", model.statusRows)
	}
	view := model.View().Content
	if strings.Contains(view, "Workspace") || strings.Contains(view, "Confirm") || !strings.Contains(view, "Validate") || !strings.Contains(view, "Write") {
		t.Fatalf("form/work rows mixed in view: %q", view)
	}
}

func TestConsoleControlledWorkAlternatesDeclaredCatalogsWithoutMixingRows(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command: "YCY / git pulse",
		FormCatalog: []ConsoleFormStep{
			{ID: "date-range", Name: "Select date range", Detail: "choose calendar range"},
			{ID: "author-filter", Name: "Filter by authors", Detail: "choose authors"},
		},
	})
	work := []OperationPhase{
		{ID: "prepare", Name: "Prepare workspace", State: PhasePending},
		{ID: "scan", Name: "Scan repositories", State: PhasePending},
		{ID: "fetch", Name: "Fetch commits", State: PhasePending},
		{ID: "build", Name: "Build commit tree", State: PhasePending},
	}
	_, _ = model.Update(richStartTrackMsg{
		label:             "Git Pulse",
		phases:            work,
		requestCancel:     func() error { return nil },
		retainFormCatalog: true,
		ack:               make(chan struct{}),
	})
	if len(model.formRows) != 2 || len(model.statusRows) != 4 {
		t.Fatalf("initial controlled catalogs = (%d form, %d work rows)", len(model.formRows), len(model.statusRows))
	}
	workView := model.View().Content
	if strings.Contains(workView, "Select date range") || strings.Contains(workView, "Filter by authors") || !strings.Contains(workView, "Scan repositories") || !strings.Contains(workView, "Build commit tree") {
		t.Fatalf("initial controlled Work view mixed catalogs: %q", workView)
	}

	_, _ = model.Update(richTrackPhaseMsg{phase: OperationPhase{ID: "prepare", State: PhaseCompleted, Detail: "Workspace ready"}, ack: make(chan struct{})})
	_, _ = model.Update(richTrackPhaseMsg{phase: OperationPhase{ID: "scan", State: PhaseActive, Detail: "Found 2 repositories"}, ack: make(chan struct{})})
	_, _ = model.Update(richTrackPhaseMsg{phase: OperationPhase{ID: "scan", State: PhaseCompleted, Detail: "Found 2 repositories"}, ack: make(chan struct{})})

	dateResponse := make(chan richAskResult, 1)
	_, _ = model.Update(richShowFormMsg{
		id:       1,
		form:     consoleTestForm{},
		answer:   func() InteractionAnswer { return InteractionAnswer{Value: "1"} },
		step:     consoleFormStep{id: 1, catalogID: "date-range", name: "Select date range", detail: "single selection", state: PhaseActive},
		response: dateResponse,
		ack:      make(chan struct{}),
	})
	dateView := model.View().Content
	if !strings.Contains(dateView, "Select date range") || !strings.Contains(dateView, "Filter by authors") || strings.Contains(dateView, "Scan repositories") || strings.Contains(dateView, "Fetch commits") {
		t.Fatalf("date Form view mixed catalogs: %q", dateView)
	}
	_, _ = model.Update(richFormSubmittedMsg{id: 1})
	<-dateResponse
	if model.mode != richTrackMode || len(model.formRows) != 2 || len(model.statusRows) != 4 {
		t.Fatalf("date completion did not restore Work catalog: mode=%d form=%d work=%d", model.mode, len(model.formRows), len(model.statusRows))
	}

	_, _ = model.Update(richTrackPhaseMsg{phase: OperationPhase{ID: "fetch", State: PhaseActive, Detail: "Reading repositories"}, ack: make(chan struct{})})
	_, _ = model.Update(richTrackPhaseMsg{phase: OperationPhase{ID: "fetch", State: PhaseCompleted, Detail: "Read 2 of 2 repositories"}, ack: make(chan struct{})})
	authorResponse := make(chan richAskResult, 1)
	_, _ = model.Update(richShowFormMsg{
		id:       2,
		form:     consoleTestForm{},
		answer:   func() InteractionAnswer { return InteractionAnswer{Values: []string{"Ada"}} },
		step:     consoleFormStep{id: 2, catalogID: "author-filter", name: "Filter by authors", detail: "multiple selection", state: PhaseActive},
		response: authorResponse,
		ack:      make(chan struct{}),
	})
	authorView := model.View().Content
	if !strings.Contains(authorView, "Select date range") || !strings.Contains(authorView, "Filter by authors") || strings.Contains(authorView, "Fetch commits") || strings.Contains(authorView, "Build commit tree") {
		t.Fatalf("author Form view mixed catalogs: %q", authorView)
	}
	if model.formRows[0].state != PhaseCompleted || model.formRows[1].state != PhaseActive {
		t.Fatalf("form states after Work/Form/Work transition = %#v", model.formRows)
	}
	_, _ = model.Update(richFormSubmittedMsg{id: 2})
	<-authorResponse
	finalWorkView := model.View().Content
	if strings.Contains(finalWorkView, "Select date range") || strings.Contains(finalWorkView, "Filter by authors") || !strings.Contains(finalWorkView, "Fetch commits") || !strings.Contains(finalWorkView, "Build commit tree") {
		t.Fatalf("restored Work view mixed catalogs: %q", finalWorkView)
	}
}

func TestConsoleNormalizedProjectionKeepsMetadataSingleLineAndWithinWidth(t *testing.T) {
	longValue := strings.Repeat("workspace-value ", 8) + "\nwith another line"
	runtime := NewExperience(ExperienceOptions{})
	run, err := runtime.OpenConsole(context.Background(), ConsoleDescriptor{
		Command:  "  YCY\tCONFIG\n",
		Target:   " profile\n demo ",
		Metadata: []ConsoleMetadata{{Label: "workspace\nname", Value: longValue}},
	})
	if err != nil {
		t.Fatalf("OpenConsole() error = %v", err)
	}
	concrete := run.(*runtimeRun)
	if concrete.console.Command != "YCY CONFIG" || concrete.console.Target != "profile demo" || concrete.console.Metadata[0].Label != "workspace name" || strings.Contains(concrete.console.Metadata[0].Value, "\n") {
		t.Fatalf("normalized console fields = %#v", concrete.console)
	}

	model := newRichRootModelWithConsole(70, 20, false, concrete.console)
	model.mode = richTrackMode
	model.track = &trackedState{label: "Work", phases: []OperationPhase{{Name: "Phase", State: PhaseActive, Detail: "detail"}}}
	view := model.View().Content
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 70 {
			t.Fatalf("console line exceeds terminal width: %d > 70: %q", lipgloss.Width(line), line)
		}
	}
	metadata := model.consoleMetadataView(richStyles(false), 20)
	if strings.Contains(metadata, "\n") || lipgloss.Width(metadata) > 20 {
		t.Fatalf("metadata projection is not bounded single-line: %q (width %d)", metadata, lipgloss.Width(metadata))
	}
}

func TestConsoleModelUsesBPaletteAndNoColorRemovesSGR(t *testing.T) {
	console := ConsoleDescriptor{
		Command:  "YCY CONFIG",
		Target:   "profile demo",
		Metadata: []ConsoleMetadata{{Label: "workspace", Value: "repo"}},
	}
	colored := newRichRootModelWithConsole(96, 30, true, console)
	colored.mode = richTrackMode
	colored.track = &trackedState{label: "Work", phases: []OperationPhase{
		{Name: "Done phase", State: PhaseCompleted, Detail: "saved"},
		{Name: "Active phase", State: PhaseActive, Detail: "working"},
	}}
	coloredView := colored.View().Content
	for _, colorCode := range []string{"38;2;255;180;84", "38;2;90;247;142"} {
		if !strings.Contains(coloredView, colorCode) {
			t.Fatalf("colored B view missing palette code %q: %q", colorCode, coloredView)
		}
	}
	if strings.Contains(ansi.Strip(coloredView), "[done]") || strings.Contains(ansi.Strip(coloredView), "[active]") {
		t.Fatalf("colored B view retained generic state prefix: %q", coloredView)
	}

	plain := newRichRootModelWithConsole(96, 30, false, console)
	plain.mode = richTrackMode
	plain.track = colored.track
	plainView := plain.View().Content
	if strings.Contains(plainView, "\x1b[") {
		t.Fatalf("NO_COLOR B view contains SGR/control styling: %q", plainView)
	}
	for _, text := range []string{"STATE", "Done phase", "Active phase", "✓ DONE", "◆ ACTIVE"} {
		if !strings.Contains(plainView, text) {
			t.Fatalf("NO_COLOR B view missing %q: %q", text, plainView)
		}
	}
}

func TestConsoleNoticeStaysAsLatestBoundedActiveContextBelowTable(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, defaultConsoleDescriptor())
	model.mode = richTrackMode
	model.track = &trackedState{label: "Work", phases: []OperationPhase{{Name: "Phase", State: PhaseActive, Detail: "working"}}}
	model.notices = []PresentationDocument{
		{Blocks: []PresentationBlock{{Text: "old context"}}},
		{Blocks: []PresentationBlock{{Text: "latest context"}}},
	}
	view := model.View().Content
	if !strings.Contains(view, "latest context") || strings.Contains(view, "old context") {
		t.Fatalf("notice context = %q", view)
	}
	stateRow := strings.Index(view, "◆ ACTIVE    Phase")
	active := strings.LastIndex(view, "\n   Work")
	context := strings.Index(view, "latest context")
	if stateRow < 0 || active < 0 || context < stateRow || context > active {
		t.Fatalf("notice context displaced table or active region: %q", view)
	}
}

func TestBThemeUsesBottomFocusAndApprovedPalette(t *testing.T) {
	theme := bHuhTheme(true).Theme(true)
	if !theme.Focused.Base.GetBorderBottom() || theme.Focused.Base.GetBorderLeft() {
		t.Fatalf("focused Huh border = bottom:%t left:%t; want bottom-only", theme.Focused.Base.GetBorderBottom(), theme.Focused.Base.GetBorderLeft())
	}
	if got := ansi.Strip(theme.Focused.SelectSelector.String()); got != "◆ " {
		t.Fatalf("select selector = %q, want paired active symbol", got)
	}
	if got := ansi.Strip(theme.Focused.SelectedPrefix.String()); got != "✓ " {
		t.Fatalf("selected prefix = %q, want paired success symbol", got)
	}
	if got := ansi.Strip(theme.Focused.UnselectedPrefix.String()); got != "○ " {
		t.Fatalf("unselected prefix = %q, want paired pending symbol", got)
	}
	if got := theme.Focused.Title.GetForeground(); got == nil {
		t.Fatal("focused title has no B primary color")
	}
}

func TestBThemeCanRenderWithoutColor(t *testing.T) {
	theme := bHuhTheme(false).Theme(true)
	for _, style := range []struct {
		name  string
		value string
	}{
		{name: "title", value: theme.Focused.Title.Render("title")},
		{name: "selected", value: theme.Focused.SelectedPrefix.Render("done")},
	} {
		if strings.Contains(style.value, "\x1b[") {
			t.Fatalf("%s style contains ANSI in no-color mode: %q", style.name, style.value)
		}
	}
}

func TestConsoleFormRowsRetainReachedOrderAndRedactedStepDetail(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command: "YCY / config",
		FormCatalog: []ConsoleFormStep{
			{ID: "workspace", Name: "Workspace", Detail: "choose project"},
			{ID: "token", Name: "Access token", Detail: "credential", Sensitive: true},
		},
	})
	response := make(chan richAskResult, 2)
	show := func(id uint64, request InteractionRequest) {
		_, _ = model.Update(richShowFormMsg{
			id:       id,
			form:     consoleTestForm{},
			answer:   func() InteractionAnswer { return InteractionAnswer{} },
			step:     newConsoleFormStep(id, request),
			response: response,
			ack:      make(chan struct{}),
		})
	}

	show(1, InteractionRequest{Kind: InteractionText, Message: "Workspace", ConsoleStepID: "workspace", TranscriptLabel: "Workspace"})
	_, _ = model.Update(richFormSubmittedMsg{id: 1})
	<-response
	show(2, InteractionRequest{Kind: InteractionSecret, Message: "Access token", ConsoleStepID: "token", TranscriptLabel: "Access token", Sensitive: true})
	if len(model.formRows) != 2 || len(model.statusRows) != 2 {
		t.Fatalf("declared form rows = (%d form, %d status), want two each", len(model.formRows), len(model.statusRows))
	}

	view := model.View().Content
	completed := strings.Index(view, "✓ DONE")
	workspace := strings.Index(view, "Workspace")
	active := strings.Index(view, "◆ ACTIVE")
	token := strings.Index(view, "Access token")
	if completed < 0 || workspace < completed || active < workspace || token < active || !strings.Contains(view, "answer captured") || !strings.Contains(view, "redacted input") {
		t.Fatalf("form rows = %q", view)
	}

	_, _ = model.Update(richFormCancelledMsg{id: 2})
	<-response
	view = model.View().Content
	if !strings.Contains(view, "⊘ CANCELLED") || !strings.Contains(view, "Access token") || !strings.Contains(view, "cancelled") {
		t.Fatalf("cancelled form row = %q", view)
	}
}

func TestConsoleDoesNotAppendUndeclaredFormRows(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command:     "YCY / config",
		FormCatalog: []ConsoleFormStep{{ID: "workspace", Name: "Workspace"}},
	})
	_, _ = model.Update(richShowFormMsg{
		id:       1,
		form:     consoleTestForm{},
		answer:   func() InteractionAnswer { return InteractionAnswer{} },
		step:     newConsoleFormStep(1, InteractionRequest{Kind: InteractionText, Message: "Unexpected", ConsoleStepID: "unexpected"}),
		response: make(chan richAskResult, 1),
		ack:      make(chan struct{}),
	})
	if len(model.formRows) != 1 || len(model.statusRows) != 1 || model.formRows[0].catalogID != "workspace" {
		t.Fatalf("undeclared form appended rows: form=%#v status=%#v", model.formRows, model.statusRows)
	}
}

func TestRichConsoleRequiresDeclaredFormCatalogEntry(t *testing.T) {
	run := &runtimeRun{console: ConsoleDescriptor{FormCatalog: []ConsoleFormStep{{ID: "workspace", Name: "Workspace"}}}}
	for _, request := range []InteractionRequest{
		{Kind: InteractionText, Message: "Workspace"},
		{Kind: InteractionText, Message: "Workspace", ConsoleStepID: "unexpected"},
	} {
		if err := run.validateConsoleForm(request); !errors.Is(err, ErrUndeclaredConsoleForm) {
			t.Fatalf("validateConsoleForm(%#v) error = %v, want ErrUndeclaredConsoleForm", request, err)
		}
	}
	if err := run.validateConsoleForm(InteractionRequest{Kind: InteractionText, Message: "Workspace", ConsoleStepID: "workspace"}); err != nil {
		t.Fatalf("declared Console form error = %v", err)
	}
}

func TestConsoleTrackRowsRetainCatalogOrderAndFinalDetailsAfterActiveRegionChanges(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, defaultConsoleDescriptor())
	start := func() {
		_, _ = model.Update(richStartTrackMsg{
			label: "Profile setup",
			phases: []OperationPhase{
				{ID: "validate", Name: "Validate source", State: PhasePending},
				{ID: "write", Name: "Write profile", State: PhasePending},
			},
			requestCancel: func() error { return nil },
			ack:           make(chan struct{}),
		})
	}
	update := func(phase OperationPhase) {
		_, _ = model.Update(richTrackPhaseMsg{phase: phase, ack: make(chan struct{})})
	}

	start()
	update(OperationPhase{ID: "validate", State: PhaseActive, Detail: "reading configuration"})
	update(OperationPhase{ID: "validate", State: PhaseCompleted, Detail: "configuration validated"})
	update(OperationPhase{ID: "write", State: PhaseActive, Detail: "persisting profile"})
	update(OperationPhase{ID: "write", State: PhaseCompleted, Detail: "profile persisted"})
	_, _ = model.Update(richFinishTrackMsg{ack: make(chan struct{})})
	_, _ = model.Update(richNoticeMsg{
		document: PresentationDocument{Blocks: []PresentationBlock{{Text: "Follow-up context"}}},
		ack:      make(chan struct{}),
	})

	view := model.View().Content
	if got := strings.Count(view, "Validate source"); got != 1 {
		t.Fatalf("Validate source count = %d, want 1: %q", got, view)
	}
	if got := strings.Count(view, "Write profile"); got != 1 {
		t.Fatalf("Write profile count = %d, want 1: %q", got, view)
	}
	validate := strings.Index(view, "Validate source")
	write := strings.Index(view, "Write profile")
	if validate < 0 || write < validate || !strings.Contains(view, "configuration validated") || !strings.Contains(view, "profile persisted") {
		t.Fatalf("track catalog rows lost order or final detail: %q", view)
	}
	if !strings.Contains(view, "✓ DONE") || !strings.Contains(view, "Follow-up context") {
		t.Fatalf("track table or replacement active region missing: %q", view)
	}
}

func TestConsoleOutcomeRetainsWorkRowsAndRendersBoundedSummary(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command: "YCY CONFIG",
		Target:  "profile demo",
	})
	_, _ = model.Update(richStartTrackMsg{
		label: "Apply profile",
		phases: []OperationPhase{
			{ID: "validate", Name: "Validate", State: PhaseCompleted, Detail: "validated"},
			{ID: "write", Name: "Write", State: PhaseCompleted, Detail: "persisted"},
		},
		requestCancel: func() error { return nil },
		ack:           make(chan struct{}),
	})

	request := FinishRequest{
		Outcome:  Succeeded,
		Location: "profile demo",
		Summary:  PresentationDocument{Blocks: []PresentationBlock{{Role: VisualRolePlain, Text: "Profile applied"}}},
	}
	_ = updateRichOutcome(t, model, request)
	if model.mode != richOutcomeMode {
		t.Fatalf("outcome mode = %v, want richOutcomeMode", model.mode)
	}
	if len(model.statusRows) != 2 || model.statusRows[0].phase != "Validate" || model.statusRows[1].phase != "Write" {
		t.Fatalf("final Work rows = %#v, want both rows retained", model.statusRows)
	}
	view := model.View().Content
	for _, needle := range []string{"✓ SUCCEEDED", "Location: profile demo", "Profile applied", "Validate", "Write", "✓ DONE"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("Outcome view missing %q: %q", needle, view)
		}
	}

	lateAck := make(chan struct{})
	_, _ = model.Update(richShowFormMsg{
		id:   99,
		step: consoleFormStep{name: "Late form", detail: "should be ignored", state: PhaseActive},
		ack:  lateAck,
	})
	if model.mode != richOutcomeMode || model.outcome.Outcome != request.Outcome || model.outcome.Location != request.Location || strings.Contains(model.View().Content, "Late form") {
		t.Fatalf("late form changed terminal Outcome: mode=%v outcome=%#v view=%q", model.mode, model.outcome, model.View().Content)
	}
}

func TestConsoleOutcomeProjectsFailureAndCancellation(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		outcome    FinishOutcome
		phaseState PhaseState
		glyph      string
		location   string
		summary    string
	}{
		{name: "failed", outcome: Failed, phaseState: PhaseFailed, glyph: "✕ FAILED", location: "write profile", summary: "profile could not be saved"},
		{name: "cancelled", outcome: Cancelled, phaseState: PhaseCancelled, glyph: "⊘ CANCELLED", location: "confirm profile", summary: "profile update cancelled"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
				Command: "YCY CONFIG",
				Target:  "profile demo",
			})
			_, _ = model.Update(richStartTrackMsg{
				label: "Apply profile",
				phases: []OperationPhase{
					{ID: "validate", Name: "Validate", State: PhaseCompleted, Detail: "validated"},
					{ID: "write", Name: "Write", State: testCase.phaseState, Detail: testCase.summary},
				},
				requestCancel: func() error { return nil },
				ack:           make(chan struct{}),
			})
			_ = updateRichOutcome(t, model, FinishRequest{
				Outcome:  testCase.outcome,
				Location: testCase.location,
				Summary:  PresentationDocument{Blocks: []PresentationBlock{{Text: testCase.summary}}},
			})

			view := model.View().Content
			for _, needle := range []string{testCase.glyph, "Location: " + testCase.location, testCase.summary, "Write"} {
				if !strings.Contains(view, needle) {
					t.Fatalf("%s Outcome view missing %q: %q", testCase.name, needle, view)
				}
			}
			_, _ = model.Update(richTrackPhaseMsg{
				phase: OperationPhase{ID: "write", Name: "Late write", State: PhaseCompleted, Detail: "must be ignored"},
				ack:   make(chan struct{}),
			})
			if strings.Contains(model.View().Content, "Late write") {
				t.Fatalf("late Work update changed %s Outcome: %q", testCase.name, model.View().Content)
			}
		})
	}
}

func TestConsoleOutcomeElapsedQuitsAfterExactDwell(t *testing.T) {
	if outcomeDwell != 800*time.Millisecond {
		t.Fatalf("outcome dwell = %s, want 800ms", outcomeDwell)
	}
	model := newRichRootModelWithConsole(96, 30, false, defaultConsoleDescriptor())
	_ = updateRichOutcome(t, model, FinishRequest{Outcome: Succeeded})
	updated, quitCmd := model.Update(richOutcomeElapsedMsg{})
	if updated != model {
		t.Fatalf("elapsed message returned a different model: %T", updated)
	}
	if quitCmd == nil {
		t.Fatal("elapsed Outcome message returned no quit command")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatalf("elapsed Outcome command returned %T, want tea.QuitMsg", quitCmd())
	}
}

func updateRichOutcome(t *testing.T, model *richRootModel, request FinishRequest) tea.Cmd {
	t.Helper()
	ack := make(chan struct{})
	_, cmd := model.Update(richShowOutcomeMsg{request: request, ack: ack})
	return cmd
}

type consoleTestForm struct{}

func (consoleTestForm) Init() tea.Cmd { return nil }

func (form consoleTestForm) Update(tea.Msg) (tea.Model, tea.Cmd) { return form, nil }

func (consoleTestForm) View() tea.View { return tea.NewView("active input") }

func (consoleTestForm) configure(int, int, bool) {}

func (consoleTestForm) handlesEscape() bool { return false }
