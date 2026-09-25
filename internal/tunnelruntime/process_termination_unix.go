//go:build darwin || linux

package tunnelruntime

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

func terminateOwnedFRPS(owner ProcessOwner) error {
	active, err := verifyUnixProcessGroupOwner(owner)
	if err != nil || !active {
		return err
	}
	if err := syscall.Kill(-owner.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if err := waitForOwnedFRPSExit(owner, 5*time.Second); err == nil {
		return nil
	} else if !errors.Is(err, errFRPSStillRunning) {
		return err
	}
	active, err = verifyUnixProcessGroupOwner(owner)
	if err != nil || !active {
		return err
	}
	if err := syscall.Kill(-owner.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if err := waitForOwnedFRPSExit(owner, time.Second); err != nil {
		if errors.Is(err, errFRPSStillRunning) {
			return fmt.Errorf("owned FRPS did not exit")
		}
		return err
	}
	return nil
}

func verifyUnixProcessGroupOwner(owner ProcessOwner) (bool, error) {
	if _, err := VerifyFRPSOwner(nil, owner); err != nil {
		if errors.Is(err, ErrProcessNotFound) {
			return false, nil
		}
		return false, err
	}
	group, err := syscall.Getpgid(owner.PID)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if group != owner.PID {
		return false, ErrProcessIdentityUnclear
	}
	return true, nil
}

var errFRPSStillRunning = errors.New("FRPS is still running")

func waitForOwnedFRPSExit(owner ProcessOwner, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		_, err := VerifyFRPSOwner(nil, owner)
		if errors.Is(err, ErrProcessNotFound) {
			return nil
		}
		if errors.Is(err, ErrProcessIdentityUnclear) {
			return err
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return errFRPSStillRunning
		}
		time.Sleep(50 * time.Millisecond)
	}
}
