package terminal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
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

func TestNormalizeFinishRequestPreservesSafeInlineSummarySpans(t *testing.T) {
	request, err := normalizeFinishRequest(FinishRequest{
		Outcome: Succeeded,
		Summary: PresentationDocument{Blocks: []PresentationBlock{{
			Role: VisualRoleMuted,
			Text: " time\n",
			Spans: []PresentationSpan{
				{Role: VisualRoleActive, Text: " author\x1b[31m "},
				{Role: VisualRolePlain, Text: " secret ", Sensitive: true},
			},
		}}},
	})
	if err != nil {
		t.Fatalf("normalizeFinishRequest() error = %v", err)
	}
	block := request.Summary.Blocks[0]
	if block.Text != "time" || len(block.Spans) != 2 || block.Spans[0].Text != "author" || block.Spans[0].Role != VisualRoleActive || block.Spans[1].Text != "secret" || !block.Spans[1].Sensitive {
		t.Fatalf("normalized inline summary = %#v", block)
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

func TestOpenUsesTheCompatibleDefaultConsoleDescriptor(t *testing.T) {
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
	for _, needle := range []string{"YCY CONFIG", "profile demo", "workspace: repo · provider: github", "✓ Scan  ─  ◆ Write", "Write", "pending"} {
		if !strings.Contains(view.Content, needle) {
			t.Fatalf("wide view missing %q: %q", needle, view.Content)
		}
	}
	if strings.Contains(view.Content, "[done]") || strings.Contains(view.Content, "[active]") {
		t.Fatalf("wide view retained generic phase prefixes: %q", view.Content)
	}
}

func TestConsoleCompactSurfaceUsesFocusTrail(t *testing.T) {
	model := newRichRootModelWithConsole(69, 30, false, defaultConsoleDescriptor())
	model.mode = richTrackMode
	model.track = &trackedState{label: "work", phases: []OperationPhase{{Name: "Phase", State: PhaseActive}}}
	view := model.View().Content
	if strings.Count(view, "Phase") != 2 || !strings.Contains(view, "◆ Phase") {
		t.Fatalf("compact surface omitted Focus trail or detail: %q", view)
	}
}

func TestConsoleCompactViewRetainsTrailAndActiveRegion(t *testing.T) {
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
	for _, needle := range []string{"YCY GIT", "scope: workspace", "✓ Scan  ─  ◆ Fetch", "Fetch", "commits"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("compact view missing %q: %q", needle, view)
		}
	}
}

func TestConsoleTrackStartsAndAdvancesPulse(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, defaultConsoleDescriptor())
	ack := make(chan struct{})
	_, start := model.Update(richStartTrackMsg{
		label:         "Work",
		phases:        []OperationPhase{{ID: "phase", Name: "Phase", State: PhaseActive}},
		requestCancel: func() error { return nil },
		ack:           ack,
	})
	if start == nil {
		t.Fatal("starting a Rich Track returned no spinner command")
	}
	if model.spin.Spinner.FPS != spinner.Pulse.FPS || len(model.spin.Spinner.Frames) != len(spinner.Pulse.Frames) {
		t.Fatalf("spinner = %#v, want Bubbles Pulse", model.spin.Spinner)
	}
	for index, frame := range spinner.Pulse.Frames {
		if model.spin.Spinner.Frames[index] != frame {
			t.Fatalf("spinner frame %d = %q, want Pulse frame %q", index, model.spin.Spinner.Frames[index], frame)
		}
	}
	first := model.spin.View()
	if first != spinner.Pulse.Frames[0] || !strings.Contains(model.View().Content, first+" Phase") {
		t.Fatalf("initial Pulse frame = %q, view = %q", first, model.View().Content)
	}

	message, ok := start().(spinner.TickMsg)
	if !ok {
		t.Fatalf("start command returned %T, want spinner.TickMsg", start())
	}
	_, next := model.Update(message)
	if next == nil {
		t.Fatal("spinner tick returned no follow-up command")
	}
	if got := model.spin.View(); got == first {
		t.Fatalf("spinner frame did not advance: still %q", got)
	}
}

func TestConsoleTrackResetsPulseAndIgnoresLateTicksOutsideWork(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, defaultConsoleDescriptor())
	startTrack := func() (int, spinner.TickMsg) {
		ack := make(chan struct{})
		_, start := model.Update(richStartTrackMsg{
			label:         "Work",
			phases:        []OperationPhase{{ID: "phase", Name: "Phase", State: PhaseActive}},
			requestCancel: func() error { return nil },
			ack:           ack,
		})
		message, ok := start().(spinner.TickMsg)
		if !ok {
			t.Fatalf("start command returned %T, want spinner.TickMsg", start())
		}
		return model.spin.ID(), message
	}

	firstID, late := startTrack()
	_, _ = model.Update(late)
	if model.spin.View() == spinner.Pulse.Frames[0] {
		// The first update is expected to advance the frame; this also proves the
		// message belongs to the currently active spinner.
		t.Fatalf("active spinner did not consume its tick")
	}
	model.mode = richFormMode
	beforeFormTick := model.spin.View()
	if _, command := model.Update(model.spin.Tick()); command != nil || model.spin.View() != beforeFormTick {
		t.Fatalf("Form mode consumed a late spinner tick: view=%q command=%v", model.spin.View(), command)
	}

	secondID, _ := startTrack()
	if secondID == firstID || model.spin.View() != spinner.Pulse.Frames[0] {
		t.Fatalf("new Track did not reset Pulse: ids=(%d,%d) view=%q", firstID, secondID, model.spin.View())
	}
	beforeStaleTick := model.spin.View()
	if _, command := model.Update(late); command != nil || model.spin.View() != beforeStaleTick {
		t.Fatalf("new Track consumed a stale spinner tick: view=%q command=%v", model.spin.View(), command)
	}
	_, outcomeCommand := model.Update(richShowOutcomeMsg{
		request: FinishRequest{Outcome: Succeeded},
		ack:     make(chan struct{}),
	})
	if outcomeCommand == nil {
		t.Fatal("Outcome transition returned no dwell command")
	}
	beforeOutcomeTick := model.spin.View()
	if _, command := model.Update(model.spin.Tick()); command != nil || model.spin.View() != beforeOutcomeTick {
		t.Fatalf("Outcome mode consumed a late spinner tick: view=%q command=%v", model.spin.View(), command)
	}
}

func TestControlledWorkResumesPulseAfterForm(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, ConsoleDescriptor{
		Command:     "YCY",
		FormCatalog: []ConsoleFormStep{{ID: "step", Name: "Step"}},
	})
	_, start := model.Update(richStartTrackMsg{
		label:             "Work",
		phases:            []OperationPhase{{ID: "phase", Name: "Phase", State: PhaseActive}},
		requestCancel:     func() error { return nil },
		retainFormCatalog: true,
		ack:               make(chan struct{}),
	})
	if start == nil {
		t.Fatal("controlled Work returned no initial spinner command")
	}
	response := make(chan richAskResult, 1)
	_, _ = model.Update(richShowFormMsg{
		id:       1,
		form:     consoleTestForm{},
		answer:   func() InteractionAnswer { return InteractionAnswer{Value: "ok"} },
		step:     consoleFormStep{catalogID: "step", id: 1, name: "Step", state: PhaseActive},
		response: response,
		ack:      make(chan struct{}),
	})
	if model.mode != richFormMode {
		t.Fatalf("mode after form start = %v, want richFormMode", model.mode)
	}
	_, resume := model.Update(richFormSubmittedMsg{id: 1})
	<-response
	if model.mode != richTrackMode || resume == nil || model.spin.View() != spinner.Pulse.Frames[0] {
		t.Fatalf("controlled Work did not restart Pulse: mode=%v command=%v view=%q", model.mode, resume, model.spin.View())
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

	view := model.View().Content
	for _, needle := range []string{"◆ Workspace", "○ Access token", "○ Confirm"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("initial catalog view missing %q: %q", needle, view)
		}
	}
	for _, hidden := range []string{"choose project", "[redacted]", "apply changes", "STEPS", "PENDING"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("initial catalog leaked body detail %q: %q", hidden, view)
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
	if view := model.View().Content; !strings.Contains(view, "◆ Workspace  ─  ○ Access token") {
		t.Fatalf("catalog did not expose active form in trail: %q", view)
	}
	_, _ = model.Update(richFormSubmittedMsg{id: 1})
	<-response
	if view := model.View().Content; !strings.Contains(view, "✓ Workspace  ─  ○ Access token") {
		t.Fatalf("catalog did not retain completed form in trail: %q", view)
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

	view := model.View().Content
	if strings.Contains(view, "Workspace") || strings.Contains(view, "Confirm") || !strings.Contains(view, "○ Validate  ─  ○ Write") {
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
	workView := model.View().Content
	if strings.Contains(workView, "Select date range") || strings.Contains(workView, "Filter by authors") || !strings.Contains(workView, "○ Prepare workspace  ─  ○ Scan repositories  ─  ○ Fetch commits") {
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
	if model.mode != richTrackMode || !strings.Contains(model.View().Content, "✓ Scan repositories  ─  ○ Fetch commits  ─  ○ Build commit tree") {
		t.Fatalf("date completion did not restore Work trail: mode=%d view=%q", model.mode, model.View().Content)
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

func TestConsoleNormalizedProjectionKeepsMetadataWithinWidth(t *testing.T) {
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

}

func TestConsoleMetadataPacksFieldsAndWrapsOnlyAtFieldBoundaries(t *testing.T) {
	fields := []ConsoleMetadata{
		{Label: "mode", Value: "stage and commit"},
		{Label: "language", Value: "en"},
		{Label: "remote", Value: "origin"},
	}
	muted := richStyles(false)[VisualRoleMuted]
	if got, want := consoleMetadataText(fields, 120, muted), "mode: stage and commit · language: en · remote: origin"; got != want {
		t.Fatalf("wide metadata = %q, want %q", got, want)
	}
	if got, want := consoleMetadataText(fields, 30, muted), "mode: stage and commit\nlanguage: en · remote: origin"; got != want {
		t.Fatalf("compact metadata = %q, want %q", got, want)
	}
}

func TestConsoleMetadataLongFieldWrapsWithoutTruncation(t *testing.T) {
	value := "工作区/" + strings.Repeat("long-directory/", 6)
	text := consoleMetadataText([]ConsoleMetadata{{Label: "directory", Value: "\x1b[31m" + value + "\x1b[0m"}}, 24, richStyles(true)[VisualRoleMuted])
	if strings.Contains(text, "\x1b[31m") || !strings.Contains(ansi.Strip(text), value) {
		t.Fatalf("metadata was not safely preserved: %q", text)
	}

	scroll := newConsoleScroll()
	scroll.setContent([]scrollBlock{{id: "metadata", text: text}}, 24, 10)
	lines := strings.Split(scroll.viewport.View(), "\n")
	var joined strings.Builder
	for _, line := range lines {
		if ansi.StringWidth(line) > 24 {
			t.Fatalf("metadata line exceeds width: %d > 24: %q", ansi.StringWidth(line), line)
		}
		joined.WriteString(strings.TrimSpace(ansi.Strip(line)))
	}
	if !strings.Contains(joined.String(), value) {
		t.Fatalf("wrapped metadata was truncated: %q", lines)
	}
}

func TestConsoleModelUsesFocusPaletteAndNoColorRemovesSGR(t *testing.T) {
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
	for _, colorCode := range []string{"38;2;79;227;177", "38;2;90;247;142"} {
		if !strings.Contains(coloredView, colorCode) {
			t.Fatalf("colored Focus view missing palette code %q: %q", colorCode, coloredView)
		}
	}
	if strings.Contains(ansi.Strip(coloredView), "[done]") || strings.Contains(ansi.Strip(coloredView), "[active]") {
		t.Fatalf("colored Focus view retained generic state prefix: %q", coloredView)
	}

	plain := newRichRootModelWithConsole(96, 30, false, console)
	plain.mode = richTrackMode
	plain.track = colored.track
	plainView := plain.View().Content
	if strings.Contains(plainView, "\x1b[") {
		t.Fatalf("NO_COLOR Focus view contains SGR/control styling: %q", plainView)
	}
	for _, text := range []string{"✓ Done phase  ─  ◆ Active phase", "Active phase", "working"} {
		if !strings.Contains(plainView, text) {
			t.Fatalf("NO_COLOR Focus view missing %q: %q", text, plainView)
		}
	}
}

func TestConsoleNoticeHistoryRemainsAvailableBeforeActiveWork(t *testing.T) {
	model := newRichRootModelWithConsole(96, 30, false, defaultConsoleDescriptor())
	model.mode = richTrackMode
	model.track = &trackedState{label: "Work", phases: []OperationPhase{{Name: "Phase", State: PhaseActive, Detail: "working"}}}
	model.notices = []PresentationDocument{
		{Blocks: []PresentationBlock{{Text: "old context"}}},
		{Blocks: []PresentationBlock{{Text: "latest context"}}},
	}
	view := model.View().Content
	if !strings.Contains(view, "latest context") || !strings.Contains(view, "old context") {
		t.Fatalf("notice context = %q", view)
	}
	active := strings.LastIndex(view, model.spin.View()+" Phase")
	context := strings.Index(view, "latest context")
	if active < 0 || context < 0 || context > active {
		t.Fatalf("notice context displaced active region: %q", view)
	}
}

func TestFocusThemeUsesFlushAlignmentAndApprovedPalette(t *testing.T) {
	theme := focusHuhTheme(true).Theme(true)
	if theme.Focused.Base.GetBorderBottom() || theme.Focused.Base.GetBorderLeft() || theme.Focused.Base.GetPaddingLeft() != 0 {
		t.Fatalf("focused Huh field must be borderless and flush left: %#v", theme.Focused.Base)
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
	if got := theme.Focused.Title.GetForeground(); got != lipgloss.Color(focusText) {
		t.Fatalf("focused title color = %v, want body text color", got)
	}
	if got := theme.Focused.SelectSelector.GetForeground(); got != lipgloss.Color(focusAccent) {
		t.Fatalf("focused selector color = %v, want accent", got)
	}
}

func TestFocusThemeCanRenderWithoutColor(t *testing.T) {
	theme := focusHuhTheme(false).Theme(true)
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
	view := model.View().Content
	if !strings.Contains(view, "✓ Workspace  ─  ◆ Access token") || strings.Contains(view, "answer captured") || strings.Contains(view, "redacted input") {
		t.Fatalf("active form trail or body = %q", view)
	}

	_, _ = model.Update(richFormCancelledMsg{id: 2})
	<-response
	view = model.View().Content
	if !strings.Contains(view, "✓ Workspace  ─  ⊘ Access token") || strings.Contains(view, "cancelled") {
		t.Fatalf("cancelled form trail = %q", view)
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
	if len(model.formRows) != 1 || model.formRows[0].catalogID != "workspace" {
		t.Fatalf("undeclared form appended rows: form=%#v", model.formRows)
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

func TestConsoleCompletedTrackLeavesHistoryToTranscript(t *testing.T) {
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
	if !strings.Contains(view, "Follow-up context") {
		t.Fatalf("replacement notice missing: %q", view)
	}
	for _, history := range []string{"Validate source", "Write profile", "configuration validated", "profile persisted", "STEPS"} {
		if strings.Contains(view, history) {
			t.Fatalf("completed track history %q remained in live body: %q", history, view)
		}
	}
}

func TestConsoleOutcomeRendersOnlyBoundedSummary(t *testing.T) {
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
	view := model.View().Content
	for _, needle := range []string{"SUCCEEDED", "Location: profile demo", "Profile applied"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("Outcome view missing %q: %q", needle, view)
		}
	}
	for _, history := range []string{"Validate", "Write", "DONE"} {
		if strings.Contains(view, history) {
			t.Fatalf("Outcome retained Work history %q: %q", history, view)
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
		location   string
		summary    string
	}{
		{name: "failed", outcome: Failed, phaseState: PhaseFailed, location: "write profile", summary: "profile could not be saved"},
		{name: "cancelled", outcome: Cancelled, phaseState: PhaseCancelled, location: "confirm profile", summary: "profile update cancelled"},
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
			for _, needle := range []string{strings.ToUpper(testCase.outcome.String()), "Location: " + testCase.location, testCase.summary} {
				if !strings.Contains(view, needle) {
					t.Fatalf("%s Outcome view missing %q: %q", testCase.name, needle, view)
				}
			}
			if strings.Contains(view, "Write") {
				t.Fatalf("%s Outcome retained Work history: %q", testCase.name, view)
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

func (consoleTestForm) configure(int) {}

func (consoleTestForm) handlesEscape() bool { return false }
