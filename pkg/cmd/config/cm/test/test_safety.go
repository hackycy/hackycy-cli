package test

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hackycy/hackycy-cli/internal/logging"
)

const (
	cmTestFieldLimit    = 160
	cmTestResponseLimit = 2_048
)

func safeCMTestProfile(value string) string {
	return safeCMTestField(value, "Configured profile")
}

func safeCMTestModel(value string) string {
	return safeCMTestField(value, "Configured model")
}

func safeCMTestField(value, fallback string) string {
	if !utf8.ValidString(value) {
		return fallback
	}
	value = logging.StripANSI(value)
	var output strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) {
			output.WriteByte(' ')
			continue
		}
		output.WriteRune(character)
	}
	value = strings.Join(strings.Fields(output.String()), " ")
	if value == "" {
		return fallback
	}
	return boundCMTestText(value, cmTestFieldLimit)
}

func safeCMTestURL(value string) string {
	if !utf8.ValidString(value) || containsCMTestControl(value) {
		return "Configured provider"
	}
	parsed, err := url.Parse(strings.TrimSpace(logging.StripANSI(value)))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return "Configured provider"
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "Configured provider"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	projected := parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
	if projected == "" || len([]rune(projected)) > cmTestFieldLimit {
		return "Configured provider"
	}
	return projected
}

func safeCMTestResponse(value string) string {
	if !utf8.ValidString(value) {
		return "Response unavailable"
	}
	value = logging.StripANSI(value)
	var output strings.Builder
	for _, character := range value {
		switch {
		case character == '\r':
			continue
		case character == '\n':
			output.WriteRune(character)
		case character == '\t':
			output.WriteByte(' ')
		case unicode.IsControl(character):
			output.WriteByte(' ')
		default:
			output.WriteRune(character)
		}
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(line)
	}
	value = strings.TrimSpace(strings.Join(lines, "\n"))
	if value == "" {
		return "Response unavailable"
	}
	if len([]rune(value)) <= cmTestResponseLimit {
		return value
	}
	marker := "... [truncated]"
	limit := cmTestResponseLimit - len([]rune(marker))
	if limit < 0 {
		limit = 0
	}
	return string([]rune(value)[:limit]) + marker
}

func safeCMTestStatus(value string) string {
	return safeCMTestField(value, "Provider error")
}

func boundCMTestText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func containsCMTestControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func cmTestResolverFailureKind(err error) string {
	if err == nil {
		return "store"
	}
	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "decrypt"), strings.Contains(value, "machine id"), strings.Contains(value, "cipher"):
		return "decrypt"
	case strings.Contains(value, "profile"), strings.Contains(value, "selection"), strings.Contains(value, "usable"):
		return "selection"
	default:
		return "store"
	}
}
