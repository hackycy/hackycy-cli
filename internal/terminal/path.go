package terminal

import (
	"path"
	"strings"
)

// PathLabel hides absolute parent directories in both Unix and Windows syntax,
// including paths recorded on a different operating system.
func PathLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	value = strings.ReplaceAll(value, "\\", "/")
	drivePath := len(value) >= 3 && value[1] == ':' && value[2] == '/'
	if path.IsAbs(value) || drivePath {
		value = path.Base(path.Clean(value))
		if value == "/" || value == "." || (drivePath && len(value) == 2 && value[1] == ':') {
			return fallback
		}
	}
	return value
}
