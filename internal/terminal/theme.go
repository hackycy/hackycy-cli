package terminal

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

const (
	focusPrimary = "#4FE3B1"
	focusAccent  = "#C792EA"
	focusSuccess = "#5AF78E"
	focusWarning = "#FFD75F"
	focusError   = "#FF5F6D"
	focusText    = "#F5F7FA"
	focusMuted   = "#9AA4B5"
	focusDim     = "#70798A"
	focusDivider = "#3A4558"
	focusInk     = "#101319"
)

// Focus fields use color and semantic glyphs instead of structural borders.
func focusHuhTheme(colorEnabled bool) huh.Theme {
	return huh.ThemeFunc(func(bool) *huh.Styles {
		t := huh.ThemeBase(true)
		primary := focusThemeStyle(colorEnabled, focusPrimary)
		accent := focusThemeStyle(colorEnabled, focusAccent)
		success := focusThemeStyle(colorEnabled, focusSuccess)
		errorStyle := focusThemeStyle(colorEnabled, focusError)
		text := focusThemeStyle(colorEnabled, focusText)
		muted := focusThemeStyle(colorEnabled, focusMuted)
		dim := focusThemeStyle(colorEnabled, focusDim)
		divider := focusThemeStyle(colorEnabled, focusDivider)

		focusedBase := lipgloss.NewStyle()
		focusedButton := focusThemeEmphasis(focusThemeButtonStyle(colorEnabled), colorEnabled).Padding(0, 1).MarginRight(1)
		blurredButton := muted.PaddingRight(2).MarginRight(1)
		if !colorEnabled {
			focusedButton = lipgloss.NewStyle().
				BorderStyle(lipgloss.Border{Left: "[", Right: "]"}).
				BorderLeft(true).
				BorderRight(true).
				MarginRight(1)
			blurredButton = lipgloss.NewStyle().PaddingRight(2).MarginRight(1)
		}

		t.Form.Base = lipgloss.NewStyle()
		t.Group.Title = focusThemeEmphasis(primary, colorEnabled)
		t.Group.Description = muted
		t.Focused.Base = focusedBase
		t.Focused.Card = focusedBase
		t.Focused.Title = focusThemeEmphasis(text, colorEnabled)
		t.Focused.Description = muted
		t.Focused.ErrorIndicator = focusThemeEmphasis(errorStyle, colorEnabled).SetString("! ")
		t.Focused.ErrorMessage = focusThemeEmphasis(errorStyle, colorEnabled)
		t.Focused.SelectSelector = focusThemeEmphasis(accent, colorEnabled).SetString("◆ ")
		t.Focused.Option = text
		t.Focused.NextIndicator = primary.SetString("↓ more")
		t.Focused.PrevIndicator = primary.SetString("↑ more")
		t.Focused.MultiSelectSelector = focusThemeEmphasis(accent, colorEnabled).SetString("◆ ")
		t.Focused.SelectedOption = focusThemeEmphasis(success, colorEnabled)
		t.Focused.SelectedPrefix = focusThemeEmphasis(success, colorEnabled).SetString("✓ ")
		t.Focused.UnselectedOption = text
		t.Focused.UnselectedPrefix = dim.SetString("○ ")
		t.Focused.TextInput.Cursor = accent
		t.Focused.TextInput.CursorText = text
		t.Focused.TextInput.Placeholder = dim
		t.Focused.TextInput.Prompt = focusThemeEmphasis(accent, colorEnabled)
		t.Focused.TextInput.Text = text
		t.Focused.FocusedButton = focusedButton
		t.Focused.BlurredButton = blurredButton
		t.Focused.Next = t.Focused.FocusedButton

		// Blurred fields retain their semantic text but never create a side rail.
		t.Blurred = t.Focused
		t.Blurred.Base = focusedBase
		t.Blurred.Card = t.Blurred.Base
		t.Blurred.Title = muted
		t.Blurred.Description = dim
		t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
		t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
		t.Blurred.NextIndicator = lipgloss.NewStyle()
		t.Blurred.PrevIndicator = lipgloss.NewStyle()
		t.Help.ShortKey = focusThemeEmphasis(primary, colorEnabled)
		t.Help.ShortDesc = dim
		t.Help.ShortSeparator = divider
		return t
	})
}

func focusThemeStyle(colorEnabled bool, value string) lipgloss.Style {
	style := lipgloss.NewStyle()
	if colorEnabled {
		style = style.Foreground(lipgloss.Color(value))
	}
	return style
}

func focusThemeEmphasis(style lipgloss.Style, colorEnabled bool) lipgloss.Style {
	if !colorEnabled {
		return style
	}
	return style.Bold(true)
}

func focusThemeButtonStyle(colorEnabled bool) lipgloss.Style {
	style := focusThemeStyle(colorEnabled, focusInk)
	if !colorEnabled {
		return style
	}
	return style.Background(lipgloss.Color(focusPrimary))
}
