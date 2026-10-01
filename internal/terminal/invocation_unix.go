//go:build !windows

package terminal

import (
	"errors"
	"os"
	"syscall"
)

func prepareOutputs(output, diagnostics *os.File) (bool, bool, func() error, error) {
	return IsTerminal(output), IsTerminal(diagnostics), nil, nil
}

func unsupportedTerminalError(err error) bool {
	return errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSYS)
}

func environmentKey(key string) string { return key }

func withDefaultColorMetadata(environment []string) []string {
	value, _ := environmentLookup(environment)("TERM")
	if value == "" {
		// A real ANSI-compatible Unix TTY defaults to basic color while still
		// allowing COLORTERM to supply a stronger depth estimate.
		environment = append(environment, "TERM=ansi")
	}
	return environment
}
