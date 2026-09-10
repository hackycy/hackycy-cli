//go:build darwin || linux

package tunnelruntime

import (
	"errors"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	unixFRPPermissionRetries = 10
	unixFRPPermissionWait    = 10 * time.Millisecond
)

type unixFRPChild struct {
	pid    int
	stdout io.ReadCloser
	stderr io.ReadCloser
	done   chan struct{}

	mu      sync.RWMutex
	waitErr error
}

func startFRPChild(binaryPath, configPath string) (frpChild, error) {
	command := exec.Command(binaryPath, "-c", configPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}

	child := &unixFRPChild{
		pid:    command.Process.Pid,
		stdout: stdout,
		stderr: stderr,
		done:   make(chan struct{}),
	}
	go func() {
		waitErr := command.Wait()
		child.mu.Lock()
		child.waitErr = waitErr
		child.mu.Unlock()
		close(child.done)
	}()
	return child, nil
}

func (child *unixFRPChild) PID() int              { return child.pid }
func (child *unixFRPChild) Stdout() io.Reader     { return child.stdout }
func (child *unixFRPChild) Stderr() io.Reader     { return child.stderr }
func (child *unixFRPChild) Done() <-chan struct{} { return child.done }

func (child *unixFRPChild) WaitError() error {
	child.mu.RLock()
	defer child.mu.RUnlock()
	return child.waitErr
}

func (child *unixFRPChild) Terminate() error {
	return signalUnixFRPProcessGroup(child.pid, syscall.SIGTERM)
}

func (child *unixFRPChild) Kill() error {
	return signalUnixFRPProcessGroup(child.pid, syscall.SIGKILL)
}

// Release is a no-op because exec.Cmd.Wait releases Unix process resources.
func (child *unixFRPChild) Release() error { return nil }

func signalUnixFRPProcessGroup(pid int, signal syscall.Signal) error {
	for attempt := 0; attempt < unixFRPPermissionRetries; attempt++ {
		err := syscall.Kill(-pid, signal)
		if err == nil || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if !errors.Is(err, syscall.EPERM) || attempt == unixFRPPermissionRetries-1 {
			return err
		}
		time.Sleep(unixFRPPermissionWait)
	}
	return nil
}
