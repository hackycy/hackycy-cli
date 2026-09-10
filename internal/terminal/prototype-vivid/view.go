package main

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

func (m *model) render() string {
	width := max(m.width, 1)
	height := max(m.height, 1)
	footer := m.renderSwitcher(width)
	available := max(height-lipgloss.Height(footer)-1, 1)

	var body string
	if width < 70 || height < 20 {
		body = m.renderCompact(width)
	} else {
		switch m.variant {
		case variantConsole:
			body = m.renderConsole(width)
		case variantFocus:
			body = m.renderFocus(width, available)
		default:
			body = m.renderSignal(width)
		}
	}
	body = takeLines(body, available)
	return lipgloss.Place(width, available, lipgloss.Left, lipgloss.Top, body) + "\n" + footer
}

func (m *model) renderSignal(width int) string {
	s := stylesFor(m.variant, m.dark)
	header := strings.Join([]string{
		s.eyebrow.Render("YCY  /  CONFIGURE PROFILE"),
		s.title.Render("Prepare a provider profile"),
		s.subtitle.Render("Workspace-scoped setup · secrets remain redacted"),
	}, "\n")

	railWidth := 27
	rail := m.renderRail(railWidth)
	contentWidth := max(width-railWidth-7, 36)
	content := lipgloss.NewStyle().Width(contentWidth).Render(m.renderActive(contentWidth))
	body := lipgloss.JoinHorizontal(lipgloss.Top, rail, "    ", content)
	return lipgloss.NewStyle().Padding(1, 2).Render(header + "\n\n" + body)
}

func (m *model) renderConsole(width int) string {
	s := stylesFor(m.variant, m.dark)
	inner := max(width-6, 40)
	bar := s.primary.Render("YCY CONFIGURE") + s.muted.Render("  |  ") + s.text.Render("profile "+m.profileName()) + s.muted.Render("  |  ") + outcomeStyle(s, m.scenario).Render(m.scenario.name())
	meta := s.muted.Render("workspace ") + s.text.Render(m.values.workspace) + s.muted.Render("    provider ") + s.text.Render(strings.ToUpper(m.values.provider))
	divider := s.divider.Render(strings.Repeat("─", inner))
	active := m.renderActive(min(inner, 96))
	return lipgloss.NewStyle().Padding(1, 3).Render(strings.Join([]string{
		bar,
		meta,
		divider,
		m.renderConsoleStatus(inner),
		"",
		active,
	}, "\n"))
}

func (m *model) renderFocus(width, height int) string {
	s := stylesFor(m.variant, m.dark)
	contentWidth := min(max(width-18, 40), 72)
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		s.primary.Render("◆"),
		" ",
		s.title.Render("ycy"),
		"  ",
		s.muted.Render("configure profile"),
	)
	trail := m.renderFocusTrail(contentWidth)
	active := m.renderActive(contentWidth)
	content := strings.Join([]string{header, "", trail, "", active}, "\n")
	return lipgloss.Place(width, max(height-1, 1), lipgloss.Center, lipgloss.Top, content, lipgloss.WithWhitespaceChars(" "))
}

func (m *model) renderCompact(width int) string {
	if m.variant == variantConsole {
		return m.renderCompactConsole(width)
	}

	s := stylesFor(m.variant, m.dark)
	content := strings.Join([]string{
		s.eyebrow.Render("YCY / " + m.variant.name()),
		s.title.Render("Configure profile"),
		s.muted.Render("Compact terminal presentation"),
		"",
		m.renderActive(max(width-4, 24)),
	}, "\n")
	return lipgloss.NewStyle().Padding(1, 2).Render(content)
}

func (m *model) renderCompactConsole(width int) string {
	s := stylesFor(m.variant, m.dark)
	inner := max(width-4, 12)
	bar := s.primary.Render("YCY CONFIGURE") + s.muted.Render(" · ") +
		s.text.Render(m.profileName()) + s.muted.Render(" · ") +
		outcomeStyle(s, m.scenario).Render(m.scenario.name())
	meta := s.muted.Render("workspace ") + s.text.Render(m.values.workspace) +
		s.muted.Render(" · provider ") + s.text.Render(strings.ToUpper(m.values.provider))
	status := m.renderCompactConsoleStatus(inner)
	active := m.renderActive(inner)
	return lipgloss.NewStyle().Padding(1, 2).Render(strings.Join([]string{
		bar,
		meta,
		status,
		active,
	}, "\n"))
}

func (m *model) renderCompactConsoleStatus(width int) string {
	s := stylesFor(m.variant, m.dark)
	rows := []string{s.eyebrow.Render("STATE / PHASE / DETAIL")}
	appendRow := func(state status, phase, detail string) {
		glyph, label := statusLabel(state)
		stateText := statusStyle(s, state).Render(glyph + " " + label)
		phaseText := s.text.Render(phase)
		if detail == "" {
			rows = append(rows, stateText+" · "+phaseText)
			return
		}
		rows = append(rows, stateText+" · "+phaseText+" · "+s.muted.Render(detail))
	}

	if m.screen == screenForm {
		steps := []string{"Workspace", "Credential", "Provider", "Capabilities", "Confirm"}
		current := m.currentStepIndex()
		for index, step := range steps {
			state := statusPending
			if index < m.complete {
				state = statusSuccess
			} else if index == current {
				state = statusActive
			}
			appendRow(state, step, formStepDetail(index))
		}
		return lipgloss.NewStyle().Width(width).Render(strings.Join(rows, "\n"))
	}

	for _, phase := range m.phases {
		appendRow(phase.state, phase.name, phase.detail)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(rows, "\n"))
}

func (m *model) renderActive(width int) string {
	s := stylesFor(m.variant, m.dark)
	switch m.screen {
	case screenForm:
		return m.form.View()
	case screenWork:
		phase := m.phases[m.active]
		if m.variant == variantConsole {
			return s.primary.Render(m.spin.View()+" "+phase.name) + "\n" + s.muted.Render(phase.detail)
		}
		return strings.Join([]string{
			s.eyebrow.Render("WORK IN PROGRESS"),
			s.primary.Render(m.spin.View() + "  " + phase.name),
			s.muted.Render(phase.detail),
		}, "\n")
	case screenOutcome:
		return m.renderOutcome(width)
	default:
		return ""
	}
}

func (m *model) renderOutcome(width int) string {
	s := stylesFor(m.variant, m.dark)
	glyph, label := statusLabel(m.final)
	style := statusStyle(s, m.final)
	if m.variant == variantFocus {
		content := style.Render(glyph+"  "+label) + "\n" + s.muted.Render(m.detail)
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, content)
	}
	return style.Render(glyph+"  "+label) + "\n" + s.muted.Render(m.detail)
}

func (m *model) renderRail(width int) string {
	s := stylesFor(m.variant, m.dark)
	lines := []string{s.eyebrow.Render("FLOW")}
	if m.screen == screenForm {
		steps := []string{"Workspace", "Credential", "Provider", "Capabilities", "Confirm"}
		current := m.currentStepIndex()
		for index, step := range steps {
			state := statusPending
			if index < m.complete {
				state = statusSuccess
			} else if index == current {
				state = statusActive
			}
			lines = append(lines, renderStatusLine(s, state, fmt.Sprintf("%02d  %s", index+1, step)))
		}
	} else {
		for _, phase := range m.phases {
			lines = append(lines, renderStatusLine(s, phase.state, phase.name))
		}
	}
	lines = append(lines, "", s.muted.Render("Outcome  ")+outcomeStyle(s, m.scenario).Render(m.scenario.name()))
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m *model) renderConsoleStatus(width int) string {
	s := stylesFor(m.variant, m.dark)
	rows := []string{s.eyebrow.Render(pad("STATE", 12) + pad("PHASE", 26) + "DETAIL")}
	if m.screen == screenForm {
		steps := []string{"Workspace", "Credential", "Provider", "Capabilities", "Confirm"}
		current := m.currentStepIndex()
		for index, step := range steps {
			state := statusPending
			if index < m.complete {
				state = statusSuccess
			} else if index == current {
				state = statusActive
			}
			glyph, label := statusLabel(state)
			row := statusStyle(s, state).Render(pad(glyph+" "+label, 12)) + s.text.Render(pad(step, 26)) + s.muted.Render(formStepDetail(index))
			rows = append(rows, row)
		}
	} else {
		for _, phase := range m.phases {
			glyph, label := statusLabel(phase.state)
			row := statusStyle(s, phase.state).Render(pad(glyph+" "+label, 12)) + s.text.Render(pad(phase.name, 26)) + s.muted.Render(phase.detail)
			rows = append(rows, row)
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(rows, "\n"))
}

func (m *model) renderFocusTrail(width int) string {
	s := stylesFor(m.variant, m.dark)
	if m.screen != screenForm {
		if len(m.phases) == 0 {
			return ""
		}
		start := max(m.active-1, 0)
		end := min(start+3, len(m.phases))
		parts := make([]string, 0, end-start)
		for _, phase := range m.phases[start:end] {
			glyph, _ := statusLabel(phase.state)
			parts = append(parts, statusStyle(s, phase.state).Render(glyph+" "+phase.name))
		}
		return lipgloss.PlaceHorizontal(width, lipgloss.Center, strings.Join(parts, s.dim.Render("  →  ")))
	}

	steps := []string{"Workspace", "Credential", "Provider", "Capabilities", "Confirm"}
	current := m.currentStepIndex()
	start := max(current-1, 0)
	end := min(start+3, len(steps))
	parts := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		state := statusPending
		if index < m.complete {
			state = statusSuccess
		} else if index == current {
			state = statusActive
		}
		glyph, _ := statusLabel(state)
		parts = append(parts, statusStyle(s, state).Render(glyph+" "+steps[index]))
	}
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, strings.Join(parts, s.dim.Render("  →  ")))
}

func (m *model) renderSwitcher(width int) string {
	s := stylesFor(m.variant, m.dark)
	label := s.key.Render(m.variant.key() + "  " + m.variant.name())
	content := s.switcher.Render("F2  ‹") + "  " + label + "  " + s.switcher.Render("›  F3") + s.dim.Render("    F4  ") + outcomeStyle(s, m.scenario).Render(m.scenario.name()) + s.dim.Render("    F5  restart")
	if width < lipgloss.Width(content)+2 {
		content = label + "  " + outcomeStyle(s, m.scenario).Render(m.scenario.name()) + s.dim.Render("  F2/F3 · F4 · F5")
	}
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, content)
}

func (m *model) renderTranscript() string {
	s := stylesFor(m.variant, m.dark)
	var lines []string
	glyph, label := statusLabel(m.final)
	lines = append(lines,
		statusStyle(s, m.final).Render(glyph+"  ycy configure profile  /  "+label),
		s.muted.Render("Interaction transcript · secrets redacted"),
		"",
	)

	answers := m.transcriptAnswers()
	if len(answers) > 0 {
		lines = append(lines, s.eyebrow.Render("ANSWERS"))
		for _, answer := range answers {
			lines = append(lines, s.success.Render("✓")+"  "+answer)
		}
		lines = append(lines, "")
	}
	if len(m.phases) > 0 {
		lines = append(lines, s.eyebrow.Render("WORK"))
		for _, phase := range m.phases {
			if phase.state == statusPending {
				continue
			}
			lines = append(lines, renderStatusLine(s, phase.state, phase.name+"  "+s.muted.Render(phase.detail)))
		}
		lines = append(lines, "")
	}
	if m.abortAt != "" {
		lines = append(lines, s.warning.Render("AT")+"       "+m.abortAt)
	}
	lines = append(lines, statusStyle(s, m.final).Render("OUTCOME")+"  "+m.detail, "")
	return strings.Join(lines, "\n")
}

func (m *model) transcriptAnswers() []string {
	answers := []string{
		"Workspace       " + m.values.workspace,
		"API token       [redacted]",
		"Provider        " + providerLabel(m.values.provider),
		"Capabilities    " + strings.Join(targetLabels(m.values.targets), ", "),
		"Apply profile   " + yesNo(m.values.confirm),
	}
	return slices.Clone(answers[:min(m.complete, len(answers))])
}

func providerLabel(value string) string {
	switch value {
	case "gitlab":
		return "GitLab"
	case "local":
		return "Local"
	default:
		return "GitHub"
	}
}

func targetLabels(values []string) []string {
	labels := make([]string, 0, len(values))
	for _, value := range values {
		switch value {
		case "config":
			labels = append(labels, "Config")
		case "git":
			labels = append(labels, "Git")
		case "tunnel":
			labels = append(labels, "Tunnel")
		}
	}
	return labels
}

func yesNo(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func formStepDetail(index int) string {
	details := []string{"repository context", "redacted secret", "single selection", "multiple selection", "explicit approval"}
	return details[min(index, len(details)-1)]
}

func renderStatusLine(s visualStyles, state status, text string) string {
	glyph, _ := statusLabel(state)
	return statusStyle(s, state).Render(glyph) + "  " + s.text.Render(text)
}

func statusLabel(value status) (string, string) {
	switch value {
	case statusActive:
		return "◆", "ACTIVE"
	case statusSuccess:
		return "✓", "DONE"
	case statusWarning:
		return "!", "WARNING"
	case statusError:
		return "✕", "FAILED"
	case statusCancelled:
		return "⊘", "CANCELLED"
	default:
		return "○", "PENDING"
	}
}

func statusStyle(s visualStyles, value status) lipgloss.Style {
	switch value {
	case statusActive:
		return s.primary
	case statusSuccess:
		return s.success
	case statusWarning:
		return s.warning
	case statusError:
		return s.error
	case statusCancelled:
		return s.warning
	default:
		return s.dim
	}
}

func outcomeStyle(s visualStyles, value demoOutcome) lipgloss.Style {
	switch value {
	case outcomeFailure:
		return s.error
	case outcomeCancelled:
		return s.warning
	default:
		return s.success
	}
}

func pad(value string, width int) string {
	missing := width - lipgloss.Width(value)
	if missing <= 0 {
		return value
	}
	return value + strings.Repeat(" ", missing)
}

func takeLines(value string, count int) string {
	if count <= 0 {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) > count {
		lines = lines[:count]
	}
	return strings.Join(lines, "\n")
}
