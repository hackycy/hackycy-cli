//go:build windows

package run

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

func configureRunChild(*exec.Cmd, io.Reader) {}

func defaultRunTerminationSignal() os.Signal {
	return os.Interrupt
}

func stopRunChild(process *os.Process, _ os.Signal) error {
	return ignoreCompletedRunProcess(process.Kill())
}

func killRunChild(process *os.Process) error {
	return ignoreCompletedRunProcess(process.Kill())
}

func reapRunChild(*os.Process) error {
	return nil
}

func ignoreCompletedRunProcess(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func runChildExitResult(exited *exec.ExitError) Result {
	code := exited.ExitCode()
	if code < 0 {
		code = 1
	}
	return Result{ExitCode: code}
}
