//go:build !windows

// Package processprobe owns the small native capability used to determine
// whether a process ID is currently alive.
package processprobe

import (
	"errors"
	"syscall"
)

// Alive reports whether pid identifies a live process.
//
// Permission-denied is treated as evidence that the process exists, while
// other unexpected probe failures are returned to the caller for its own
// compatibility policy.
func Alive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}
