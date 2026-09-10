package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

type demoOutcome uint8

const (
	outcomeSuccess demoOutcome = iota
	outcomeFailure
	outcomeCancelled
)

func parseOutcome(value string) (demoOutcome, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success", "s":
		return outcomeSuccess, nil
	case "failure", "fail", "f":
		return outcomeFailure, nil
	case "cancel", "cancelled", "c":
		return outcomeCancelled, nil
	default:
		return outcomeSuccess, fmt.Errorf("unknown outcome %q (expected success, failure, or cancel)", value)
	}
}

func (value demoOutcome) name() string {
	switch value {
	case outcomeFailure:
		return "FAILURE"
	case outcomeCancelled:
		return "CANCELLED"
	default:
		return "SUCCESS"
	}
}

func (value demoOutcome) next() demoOutcome {
	return demoOutcome((int(value) + 1) % 3)
}

type screen uint8

const (
	screenForm screen = iota
	screenWork
	screenOutcome
)

type status uint8

const (
	statusPending status = iota
	statusActive
	statusSuccess
	statusWarning
	statusError
	statusCancelled
)

type workPhase struct {
	name   string
	detail string
	state  status
}

type formValues struct {
	workspace string
	token     string
	provider  string
	targets   []string
	confirm   bool
}

type model struct {
	variant  variant
	scenario demoOutcome
	dark     bool
	width    int
	height   int
	screen   screen
	form     *huh.Form
	values   formValues
	spin     spinner.Model
	phases   []workPhase
	active   int
	complete int
	abortAt  string
	final    status
	detail   string
}

type formCompleteMsg struct{}
type formCancelledMsg struct{}
type phaseAdvanceMsg struct{}
type exitMsg struct{}

func newModel(selected variant, scenario demoOutcome) *model {
	m := &model{
		variant:  selected,
		scenario: scenario,
		dark:     true,
		width:    96,
		height:   30,
		screen:   screenForm,
		active:   -1,
		values: formValues{
			workspace: "~/Workspace/hackycy-cli",
			token:     "prototype-token",
			provider:  "github",
			targets:   []string{"config", "git"},
			confirm:   true,
		},
	}
	m.form = m.newForm()
	m.applyVariant()
	return m
}

func (m *model) newForm() *huh.Form {
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key("workspace").
				Title("Workspace").
				Description("Repository used to resolve the profile context.").
				Placeholder("~/Workspace/project").
				Value(&m.values.workspace).
				Validate(func(value string) error {
					if strings.TrimSpace(value) == "" {
						return fmt.Errorf("workspace is required")
					}
					return nil
				}),
		),
		huh.NewGroup(
			huh.NewInput().
				Key("token").
				Title("API token").
				Description("Used for this profile and never written to the transcript.").
				EchoMode(huh.EchoModePassword).
				Value(&m.values.token).
				Validate(func(value string) error {
					if len(value) < 8 {
						return fmt.Errorf("token must contain at least 8 characters")
					}
					return nil
				}),
		),
		huh.NewGroup(
			huh.NewSelect[string]().
				Key("provider").
				Title("Provider").
				Description("Choose where repository metadata will be resolved.").
				Options(
					huh.NewOption("GitHub  ·  Hosted repositories and organizations", "github"),
					huh.NewOption("GitLab  ·  Groups, projects, and self-managed hosts", "gitlab"),
					huh.NewOption("Local   ·  Filesystem-only repository metadata", "local"),
				).
				Value(&m.values.provider).
				Height(5),
		),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Key("targets").
				Title("Capabilities").
				Description("Select the command areas that may use this profile.").
				Options(
					huh.NewOption("Config  ·  Read and update profile settings", "config"),
					huh.NewOption("Git     ·  Resolve repository and commit context", "git"),
					huh.NewOption("Tunnel  ·  Authenticate managed connections", "tunnel"),
				).
				Value(&m.values.targets).
				Filterable(true).
				Height(5).
				Validate(func(values []string) error {
					if len(values) == 0 {
						return fmt.Errorf("select at least one capability")
					}
					return nil
				}),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Key("confirm").
				Title("Apply profile?").
				Description("The prototype keeps this change in memory only.").
				Affirmative("Apply").
				Negative("Cancel").
				Value(&m.values.confirm),
		),
	).
		WithShowHelp(true).
		WithShowErrors(true).
		WithTheme(huhTheme(m.variant))
	form.SubmitCmd = func() tea.Msg { return formCompleteMsg{} }
	form.CancelCmd = func() tea.Msg { return formCancelledMsg{} }
	return form
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.form.Init(), tea.RequestWindowSize)
}

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch value := message.(type) {
	case tea.BackgroundColorMsg:
		m.dark = value.IsDark()
	case tea.WindowSizeMsg:
		m.width = max(value.Width, 1)
		m.height = max(value.Height, 1)
		m.configureForm()
	case tea.KeyPressMsg:
		switch value.Key().Code {
		case tea.KeyF2:
			m.variant = m.variant.next(-1)
			m.applyVariant()
			if m.screen == screenWork {
				return m, m.spin.Tick
			}
			return m, nil
		case tea.KeyF3:
			m.variant = m.variant.next(1)
			m.applyVariant()
			if m.screen == screenWork {
				return m, m.spin.Tick
			}
			return m, nil
		case tea.KeyF4:
			m.scenario = m.scenario.next()
			return m, nil
		case tea.KeyF5:
			reset := newModel(m.variant, m.scenario)
			reset.width, reset.height, reset.dark = m.width, m.height, m.dark
			reset.applyVariant()
			reset.configureForm()
			return reset, reset.Init()
		}
		if m.screen == screenWork && value.Keystroke() == "ctrl+c" {
			m.cancelWork("Interrupted while " + m.phases[m.active].name)
			return m, exitAfter(700 * time.Millisecond)
		}
	case formCompleteMsg:
		m.complete = 5
		if !m.values.confirm {
			m.abortAt = "Apply profile"
			m.final = statusCancelled
			m.detail = "No changes were applied."
			m.screen = screenOutcome
			return m, exitAfter(700 * time.Millisecond)
		}
		return m, m.startWork()
	case formCancelledMsg:
		m.abortAt = m.currentStepName()
		m.final = statusCancelled
		m.detail = "Interaction cancelled before work started."
		m.screen = screenOutcome
		return m, exitAfter(700 * time.Millisecond)
	case spinner.TickMsg:
		if m.screen == screenWork {
			var command tea.Cmd
			m.spin, command = m.spin.Update(value)
			return m, command
		}
	case phaseAdvanceMsg:
		return m, m.advanceWork()
	case exitMsg:
		return m, tea.Quit
	}

	if m.screen == screenForm {
		before := m.currentStepIndex()
		updated, command := m.form.Update(message)
		m.form = updated.(*huh.Form)
		after := m.currentStepIndex()
		if after > before {
			m.complete = max(m.complete, after)
		}
		return m, command
	}
	return m, nil
}

func (m *model) View() tea.View {
	return tea.View{Content: m.render(), AltScreen: true, ReportFocus: true}
}

func (m *model) applyVariant() {
	m.form.WithTheme(huhTheme(m.variant))
	p := paletteFor(m.variant, m.dark)
	spinnerKind := spinner.Meter
	if m.variant == variantSignal {
		spinnerKind = spinner.MiniDot
	} else if m.variant == variantFocus {
		spinnerKind = spinner.Pulse
	}
	m.spin = spinner.New(
		spinner.WithSpinner(spinnerKind),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(p.primary).Bold(true)),
	)
	m.configureForm()
}

func (m *model) configureForm() {
	if m.form == nil {
		return
	}
	if m.width < 70 || m.height < 20 {
		m.form.WithWidth(max(m.width-4, 12)).WithHeight(max(m.height-8, 4))
		return
	}
	width := max(m.width-12, 32)
	switch m.variant {
	case variantSignal:
		width = max(m.width-38, 36)
	case variantFocus:
		width = min(max(m.width-20, 36), 68)
	}
	m.form.WithWidth(width).WithHeight(max(m.height-11, 8))
}

func (m *model) startWork() tea.Cmd {
	m.screen = screenWork
	m.active = 0
	m.phases = []workPhase{
		{name: "Validate workspace", detail: "Repository and configuration are readable.", state: statusActive},
		{name: "Resolve provider", detail: "Loading provider capabilities.", state: statusPending},
		{name: "Prepare configuration", detail: "Building a redacted profile update.", state: statusPending},
		{name: "Write settings", detail: "Persisting the selected profile.", state: statusPending},
	}
	return tea.Batch(m.spin.Tick, advanceAfter(900*time.Millisecond))
}

func (m *model) advanceWork() tea.Cmd {
	if m.active < 0 || m.active >= len(m.phases) {
		return nil
	}
	if m.scenario == outcomeCancelled && m.active == 1 {
		m.cancelWork("Cancelled while resolving provider capabilities.")
		return exitAfter(800 * time.Millisecond)
	}
	if m.scenario == outcomeFailure && m.active == 2 {
		m.phases[m.active].state = statusError
		m.phases[m.active].detail = "Provider returned an invalid profile document."
		m.final = statusError
		m.detail = "Could not prepare configuration; existing settings are unchanged."
		m.screen = screenOutcome
		return exitAfter(900 * time.Millisecond)
	}

	m.phases[m.active].state = statusSuccess
	completedDetails := []string{
		"Workspace validated.",
		"Provider capabilities resolved.",
		"Redacted profile update prepared.",
		"Profile settings written.",
	}
	m.phases[m.active].detail = completedDetails[m.active]
	m.active++
	if m.active == len(m.phases) {
		m.final = statusSuccess
		m.detail = "Profile is ready for Config and Git commands."
		m.screen = screenOutcome
		return exitAfter(900 * time.Millisecond)
	}
	m.phases[m.active].state = statusActive
	return advanceAfter(900 * time.Millisecond)
}

func (m *model) cancelWork(detail string) {
	if m.active >= 0 && m.active < len(m.phases) {
		m.phases[m.active].state = statusCancelled
		m.phases[m.active].detail = detail
		m.abortAt = m.phases[m.active].name
	}
	m.final = statusCancelled
	m.detail = detail
	m.screen = screenOutcome
}

func advanceAfter(duration time.Duration) tea.Cmd {
	return tea.Tick(duration, func(time.Time) tea.Msg { return phaseAdvanceMsg{} })
}

func exitAfter(duration time.Duration) tea.Cmd {
	return tea.Tick(duration, func(time.Time) tea.Msg { return exitMsg{} })
}

func (m *model) currentStepIndex() int {
	if m.form == nil || m.form.State == huh.StateCompleted {
		return 5
	}
	field := m.form.GetFocusedField()
	if field == nil {
		return min(m.complete, 4)
	}
	switch field.GetKey() {
	case "token":
		return 1
	case "provider":
		return 2
	case "targets":
		return 3
	case "confirm":
		return 4
	default:
		return 0
	}
}

func (m *model) currentStepName() string {
	steps := []string{"Workspace", "API token", "Provider", "Capabilities", "Apply profile"}
	return steps[min(m.currentStepIndex(), len(steps)-1)]
}

func (m *model) profileName() string {
	return m.values.provider + "-work"
}

func (m *model) succeeded() bool {
	return m.screen == screenOutcome && m.final == statusSuccess
}
