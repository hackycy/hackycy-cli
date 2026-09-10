//go:build windows

package gitprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func configureChild(*exec.Cmd) {}

func defaultTerminationSignal() os.Signal {
	return os.Interrupt
}

func stopChild(process *os.Process, _ os.Signal) error {
	return ignoreCompletedProcess(process.Kill())
}

func killChild(process *os.Process) error {
	return ignoreCompletedProcess(process.Kill())
}

func reapChild(*os.Process) error {
	return nil
}

func ignoreCompletedProcess(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func signalExitCode(ctx context.Context) (int, bool) {
	if _, ok := context.Cause(ctx).(signalCause); ok {
		return 130, true
	}
	return 0, false
}
