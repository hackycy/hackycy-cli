//go:build windows

package terminaltest

import (
	"errors"
	"os/exec"
)

// ErrPTYUnsupported is returned when a target cannot create a pseudoterminal.
var ErrPTYUnsupported = errors.New("controlled pseudoterminals are not supported on this target")

// StartPTY reports that the controlled Unix PTY fixture is unavailable on
// Windows. Native Windows console tests remain a separate acceptance concern.
func StartPTY(_ *exec.Cmd) (*PTYProcess, error) {
	return nil, ErrPTYUnsupported
}

// StartPTYWithSize reports that the controlled Unix PTY fixture is unavailable
// on Windows.
func StartPTYWithSize(_ *exec.Cmd, _, _ uint16) (*PTYProcess, error) {
	return nil, ErrPTYUnsupported
}

// Resize reports that the controlled Unix PTY fixture is unavailable on Windows.
func (process *PTYProcess) Resize(_, _ uint16) error {
	if process == nil {
		return ErrPTYUnsupported
	}
	return ErrPTYUnsupported
}
