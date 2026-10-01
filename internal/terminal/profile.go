package terminal

import (
	"bytes"
	"io"
	"strings"

	"github.com/charmbracelet/colorprofile"
)

// ColorProfile is the effective color depth of one output, independent of input
// permission and screen controls. Its zero value disables styling.
type ColorProfile uint8

const (
	NoColor ColorProfile = iota
	ANSI16
	ANSI256
	TrueColor
)

func profileForColor(color bool) ColorProfile {
	if color {
		return TrueColor
	}
	return NoColor
}

func (profile ColorProfile) libraryProfile(controls bool) colorprofile.Profile {
	switch profile {
	case ANSI16:
		return colorprofile.ANSI
	case ANSI256:
		return colorprofile.ANSI256
	case TrueColor:
		return colorprofile.TrueColor
	default:
		if controls {
			return colorprofile.ASCII
		}
		return colorprofile.NoTTY
	}
}

func fromLibraryProfile(profile colorprofile.Profile) ColorProfile {
	switch profile {
	case colorprofile.TrueColor:
		return TrueColor
	case colorprofile.ANSI256:
		return ANSI256
	case colorprofile.ANSI:
		return ANSI16
	default:
		return NoColor
	}
}

// ConvertText applies a previously selected profile to a complete styled text
// record. It is never applied to machine output or forwarded child streams.
func (profile ColorProfile) ConvertText(value string) string {
	var output bytes.Buffer
	writer := colorprofile.Writer{Forward: &output, Profile: profile.libraryProfile(false)}
	_, _ = io.WriteString(&writer, value)
	return output.String()
}

// normalizedColorEnvironment prevents library preferences from overriding the
// project's policy. TERM/COLORTERM and other depth metadata remain available.
func normalizedColorEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		key = environmentKey(key)
		switch key {
		case "NO_COLOR", "FORCE_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "TTY_FORCE":
			continue
		}
		result = append(result, key+"="+value)
	}
	return withDefaultColorMetadata(result)
}
