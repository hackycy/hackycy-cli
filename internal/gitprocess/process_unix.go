//go:build !windows

package gitprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	groupSignalAttempts   = 3
	groupSignalRetryDelay = 10 * time.Millisecond
)

func configureChild(child *exec.Cmd) {
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func defaultTerminationSignal() os.Signal {
	return syscall.SIGTERM
}

func stopChild(process *os.Process, signal os.Signal) error {
	signalValue, ok := signal.(syscall.Signal)
	if !ok {
		signalValue = syscall.SIGTERM
	}
	return signalGroup(process, signalValue)
}

func killChild(process *os.Process) error {
	return signalGroup(process, syscall.SIGKILL)
}

func reapChild(process *os.Process) error {
	return signalGroup(process, syscall.SIGKILL)
}

func signalGroup(process *os.Process, signal syscall.Signal) error {
	var err error
	for attempt := 0; attempt < groupSignalAttempts; attempt++ {
		err = syscall.Kill(-process.Pid, signal)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if !errors.Is(err, syscall.EPERM) || attempt == groupSignalAttempts-1 {
			return err
		}
		time.Sleep(groupSignalRetryDelay)
	}
	return err
}

func signalExitCode(ctx context.Context) (int, bool) {
	cause, ok := context.Cause(ctx).(signalCause)
	if !ok {
		return 0, false
	}
	signal, ok := cause.Signal().(syscall.Signal)
	if !ok {
		return 0, false
	}
	return 128 + int(signal), true
}
