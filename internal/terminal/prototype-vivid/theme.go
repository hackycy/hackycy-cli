package main

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

type variant uint8

const (
	variantSignal variant = iota
	variantConsole
	variantFocus
)

func parseVariant(value string) (variant, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "signal", "a":
		return variantSignal, nil
	case "console", "b":
		return variantConsole, nil
	case "focus", "c":
		return variantFocus, nil
	default:
		return variantSignal, fmt.Errorf("unknown variant %q (expected signal, console, or focus)", value)
	}
}

func (value variant) name() string {
	switch value {
	case variantConsole:
		return "OPS CONSOLE"
	case variantFocus:
		return "FOCUS FLOW"
	default:
		return "SIGNAL RAIL"
	}
}

func (value variant) key() string {
	switch value {
	case variantConsole:
		return "B"
	case variantFocus:
		return "C"
	default:
		return "A"
	}
}

func (value variant) next(delta int) variant {
	return variant((int(value) + delta + 3) % 3)
}

type palette struct {
	primary   color.Color
	accent    color.Color
	success   color.Color
	warning   color.Color
	error     color.Color
	text      color.Color
	muted     color.Color
	dim       color.Color
	separator color.Color
}

func paletteFor(value variant, dark bool) palette {
	if !dark {
		switch value {
		case variantConsole:
			return newPalette("#8A4B00", "#005F9E", "#087D49", "#8A5A00", "#C21F39", "#17202A", "#5E6877", "#7D8794", "#AAB2BF")
		case variantFocus:
			return newPalette("#006B54", "#6A35A8", "#087D49", "#8A5A00", "#C21F39", "#17202A", "#5E6877", "#7D8794", "#AAB2BF")
		default:
			return newPalette("#006F8A", "#A4007F", "#087D49", "#8A5A00", "#C21F39", "#17202A", "#5E6877", "#7D8794", "#AAB2BF")
		}
	}

	switch value {
	case variantConsole:
		return newPalette("#FFB454", "#4CC9F0", "#5AF78E", "#FFD75F", "#FF5F6D", "#F5F7FA", "#9AA4B5", "#70798A", "#3A4558")
	case variantFocus:
		return newPalette("#4FE3B1", "#C792EA", "#5AF78E", "#FFD75F", "#FF5F6D", "#F5F7FA", "#9AA4B5", "#70798A", "#3A4558")
	default:
		return newPalette("#00D7FF", "#FF5FD7", "#5AF78E", "#FFD75F", "#FF5F6D", "#F5F7FA", "#9AA4B5", "#70798A", "#3A4558")
	}
}

func newPalette(primary, accent, success, warning, failure, text, muted, dim, separator string) palette {
	return palette{
		primary:   lipgloss.Color(primary),
		accent:    lipgloss.Color(accent),
		success:   lipgloss.Color(success),
		warning:   lipgloss.Color(warning),
		error:     lipgloss.Color(failure),
		text:      lipgloss.Color(text),
		muted:     lipgloss.Color(muted),
		dim:       lipgloss.Color(dim),
		separator: lipgloss.Color(separator),
	}
}

type visualStyles struct {
	eyebrow  lipgloss.Style
	title    lipgloss.Style
	subtitle lipgloss.Style
	text     lipgloss.Style
	muted    lipgloss.Style
	dim      lipgloss.Style
	primary  lipgloss.Style
	accent   lipgloss.Style
	success  lipgloss.Style
	warning  lipgloss.Style
	error    lipgloss.Style
	divider  lipgloss.Style
	key      lipgloss.Style
	switcher lipgloss.Style
}

func stylesFor(value variant, dark bool) visualStyles {
	p := paletteFor(value, dark)
	return visualStyles{
		eyebrow:  lipgloss.NewStyle().Foreground(p.primary).Bold(true),
		title:    lipgloss.NewStyle().Foreground(p.text).Bold(true),
		subtitle: lipgloss.NewStyle().Foreground(p.muted),
		text:     lipgloss.NewStyle().Foreground(p.text),
		muted:    lipgloss.NewStyle().Foreground(p.muted),
		dim:      lipgloss.NewStyle().Foreground(p.dim),
		primary:  lipgloss.NewStyle().Foreground(p.primary).Bold(true),
		accent:   lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		success:  lipgloss.NewStyle().Foreground(p.success).Bold(true),
		warning:  lipgloss.NewStyle().Foreground(p.warning).Bold(true),
		error:    lipgloss.NewStyle().Foreground(p.error).Bold(true),
		divider:  lipgloss.NewStyle().Foreground(p.separator),
		key:      lipgloss.NewStyle().Foreground(p.text).Background(p.separator).Bold(true).Padding(0, 1),
		switcher: lipgloss.NewStyle().Foreground(p.muted),
	}
}

func huhTheme(value variant) huh.Theme {
	return huh.ThemeFunc(func(dark bool) *huh.Styles {
		p := paletteFor(value, dark)
		t := huh.ThemeBase(dark)

		base := lipgloss.NewStyle()
		switch value {
		case variantSignal:
			base = base.BorderStyle(lipgloss.ThickBorder()).BorderLeft(true).BorderForeground(p.primary).PaddingLeft(1)
		case variantConsole:
			base = base.BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).BorderForeground(p.separator).PaddingBottom(1)
		case variantFocus:
			base = base.PaddingLeft(2)
		}

		t.Form.Base = lipgloss.NewStyle()
		t.Group.Title = lipgloss.NewStyle().Foreground(p.primary).Bold(true)
		t.Group.Description = lipgloss.NewStyle().Foreground(p.muted)
		t.Focused.Base = base
		t.Focused.Card = base
		t.Focused.Title = lipgloss.NewStyle().Bold(true)
		t.Focused.Description = lipgloss.NewStyle().Foreground(p.muted)
		t.Focused.ErrorIndicator = lipgloss.NewStyle().Foreground(p.error).Bold(true).SetString("! ")
		t.Focused.ErrorMessage = lipgloss.NewStyle().Foreground(p.error).Bold(true)
		t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(p.accent).Bold(true).SetString("◆ ")
		t.Focused.Option = lipgloss.NewStyle()
		t.Focused.NextIndicator = lipgloss.NewStyle().Foreground(p.primary).SetString("↓ more")
		t.Focused.PrevIndicator = lipgloss.NewStyle().Foreground(p.primary).SetString("↑ more")
		t.Focused.MultiSelectSelector = lipgloss.NewStyle().Foreground(p.accent).Bold(true).SetString("◆ ")
		t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(p.success).Bold(true)
		t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(p.success).Bold(true).SetString("✓ ")
		t.Focused.UnselectedOption = lipgloss.NewStyle()
		t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(p.dim).SetString("○ ")
		t.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(p.accent)
		t.Focused.TextInput.CursorText = lipgloss.NewStyle()
		t.Focused.TextInput.Placeholder = lipgloss.NewStyle().Foreground(p.dim)
		t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
		t.Focused.TextInput.Text = lipgloss.NewStyle()
		t.Focused.FocusedButton = lipgloss.NewStyle().Foreground(lipgloss.Color("#101319")).Background(p.primary).Bold(true).Padding(0, 2).MarginRight(1)
		t.Focused.BlurredButton = lipgloss.NewStyle().Foreground(p.muted).Padding(0, 2).MarginRight(1)
		t.Focused.Next = t.Focused.FocusedButton

		t.Blurred = t.Focused
		t.Blurred.Base = lipgloss.NewStyle().PaddingLeft(2)
		t.Blurred.Card = t.Blurred.Base
		t.Blurred.Title = lipgloss.NewStyle().Foreground(p.muted)
		t.Blurred.Description = lipgloss.NewStyle().Foreground(p.dim)
		t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
		t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
		t.Blurred.NextIndicator = lipgloss.NewStyle()
		t.Blurred.PrevIndicator = lipgloss.NewStyle()
		t.Help.ShortKey = lipgloss.NewStyle().Foreground(p.primary).Bold(true)
		t.Help.ShortDesc = lipgloss.NewStyle().Foreground(p.dim)
		t.Help.ShortSeparator = lipgloss.NewStyle().Foreground(p.separator)
		return t
	})
}
