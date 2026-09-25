//go:build !darwin && !linux && !windows

package tunnelruntime

import "errors"

func terminateOwnedFRPS(ProcessOwner) error {
	return errors.New("persisted FRPS termination is unsupported on this platform")
}
