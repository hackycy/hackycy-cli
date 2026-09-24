//go:build windows

package tunnelruntime

import (
	"errors"
	"os"
)

// A persisted Windows owner no longer has the original Job Object handle.
// After identity verification, terminate only the recorded FRPS PID.
func terminateOwnedFRPS(owner ProcessOwner) error {
	if _, err := VerifyFRPSOwner(nil, owner); err != nil {
		if errors.Is(err, ErrProcessNotFound) {
			return nil
		}
		return err
	}
	process, err := os.FindProcess(owner.PID)
	if err != nil {
		return err
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
