//go:build !windows

package run

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/term"
)

const (
	runGroupSignalAttempts   = 3
	runGroupSignalRetryDelay = 10 * time.Millisecond
)

func configureRunChild(child *exec.Cmd, input io.Reader) {
	attributes := &syscall.SysProcAttr{Setpgid: true}
	if terminal, ok := input.(*os.File); ok && term.IsTerminal(int(terminal.Fd())) {
		// The child keeps its isolated process group for cancellation, but it
		// must own the foreground terminal before it reads inherited stdin.
		attributes.Foreground = true
		attributes.Ctty = int(terminal.Fd())
	}
	child.SysProcAttr = attributes
}

func defaultRunTerminationSignal() os.Signal {
	return syscall.SIGTERM
}

func stopRunChild(process *os.Process, signal os.Signal) error {
	signalValue, ok := signal.(syscall.Signal)
	if !ok {
		signalValue = syscall.SIGTERM
	}
	return signalRunChildGroup(process, signalValue)
}

func killRunChild(process *os.Process) error {
	return signalRunChildGroup(process, syscall.SIGKILL)
}

func reapRunChild(process *os.Process) error {
	return signalRunChildGroup(process, syscall.SIGKILL)
}

func signalRunChildGroup(process *os.Process, signal syscall.Signal) error {
	var err error
	for attempt := 0; attempt < runGroupSignalAttempts; attempt++ {
		err = syscall.Kill(-process.Pid, signal)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if !errors.Is(err, syscall.EPERM) || attempt == runGroupSignalAttempts-1 {
			return err
		}
		time.Sleep(runGroupSignalRetryDelay)
	}
	return err
}

func runChildExitResult(exited *exec.ExitError) Result {
	if status, ok := exited.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return Result{ExitCode: 128 + int(status.Signal())}
	}
	return Result{ExitCode: exited.ExitCode()}
}
