package terminal

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

const (
	bConsolePrimary = "#FFB454"
	bConsoleAccent  = "#4CC9F0"
	bConsoleSuccess = "#5AF78E"
	bConsoleWarning = "#FFD75F"
	bConsoleError   = "#FF5F6D"
	bConsoleText    = "#F5F7FA"
	bConsoleMuted   = "#9AA4B5"
	bConsoleDim     = "#70798A"
	bConsoleDivider = "#3A4558"
	bConsoleInk     = "#101319"
)

// bHuhTheme is the one production Huh theme. Its focused field uses a bottom
// rule, matching the Ops Console contract and avoiding a persistent rail.
func bHuhTheme(colorEnabled bool) huh.Theme {
	return huh.ThemeFunc(func(bool) *huh.Styles {
		t := huh.ThemeBase(true)
		primary := bThemeStyle(colorEnabled, bConsolePrimary)
		accent := bThemeStyle(colorEnabled, bConsoleAccent)
		success := bThemeStyle(colorEnabled, bConsoleSuccess)
		errorStyle := bThemeStyle(colorEnabled, bConsoleError)
		text := bThemeStyle(colorEnabled, bConsoleText)
		muted := bThemeStyle(colorEnabled, bConsoleMuted)
		dim := bThemeStyle(colorEnabled, bConsoleDim)
		divider := bThemeStyle(colorEnabled, bConsoleDivider)

		focusedBase := lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color(bConsoleDivider)).
			PaddingBottom(1)
		if !colorEnabled {
			focusedBase = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderBottom(true).
				PaddingBottom(1)
		}

		t.Form.Base = lipgloss.NewStyle()
		t.Group.Title = bThemeEmphasis(primary, colorEnabled)
		t.Group.Description = muted
		t.Focused.Base = focusedBase
		t.Focused.Card = focusedBase
		t.Focused.Title = bThemeEmphasis(primary, colorEnabled)
		t.Focused.Description = muted
		t.Focused.ErrorIndicator = bThemeEmphasis(errorStyle, colorEnabled).SetString("! ")
		t.Focused.ErrorMessage = bThemeEmphasis(errorStyle, colorEnabled)
		t.Focused.SelectSelector = bThemeEmphasis(accent, colorEnabled).SetString("◆ ")
		t.Focused.Option = text
		t.Focused.NextIndicator = primary.SetString("↓ more")
		t.Focused.PrevIndicator = primary.SetString("↑ more")
		t.Focused.MultiSelectSelector = bThemeEmphasis(accent, colorEnabled).SetString("◆ ")
		t.Focused.SelectedOption = bThemeEmphasis(success, colorEnabled)
		t.Focused.SelectedPrefix = bThemeEmphasis(success, colorEnabled).SetString("✓ ")
		t.Focused.UnselectedOption = text
		t.Focused.UnselectedPrefix = dim.SetString("○ ")
		t.Focused.TextInput.Cursor = accent
		t.Focused.TextInput.CursorText = text
		t.Focused.TextInput.Placeholder = dim
		t.Focused.TextInput.Prompt = bThemeEmphasis(accent, colorEnabled)
		t.Focused.TextInput.Text = text
		t.Focused.FocusedButton = bThemeEmphasis(bThemeButtonStyle(colorEnabled), colorEnabled).Padding(0, 2).MarginRight(1)
		t.Focused.BlurredButton = muted.Padding(0, 2).MarginRight(1)
		t.Focused.Next = t.Focused.FocusedButton

		// Blurred fields retain their semantic text but never create a side rail.
		t.Blurred = t.Focused
		t.Blurred.Base = lipgloss.NewStyle()
		t.Blurred.Card = t.Blurred.Base
		t.Blurred.Title = muted
		t.Blurred.Description = dim
		t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
		t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
		t.Blurred.NextIndicator = lipgloss.NewStyle()
		t.Blurred.PrevIndicator = lipgloss.NewStyle()
		t.Help.ShortKey = primary.Bold(true)
		t.Help.ShortDesc = dim
		t.Help.ShortSeparator = divider
		return t
	})
}

func bThemeStyle(colorEnabled bool, value string) lipgloss.Style {
	style := lipgloss.NewStyle()
	if colorEnabled {
		style = style.Foreground(lipgloss.Color(value))
	}
	return style
}

func bThemeEmphasis(style lipgloss.Style, colorEnabled bool) lipgloss.Style {
	if !colorEnabled {
		return style
	}
	return style.Bold(true)
}

func bThemeButtonStyle(colorEnabled bool) lipgloss.Style {
	style := bThemeStyle(colorEnabled, bConsoleInk)
	if !colorEnabled {
		return style
	}
	return style.Background(lipgloss.Color(bConsolePrimary))
}
