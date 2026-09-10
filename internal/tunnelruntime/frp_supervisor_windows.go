//go:build windows

package tunnelruntime

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsFRPChild struct {
	process *os.Process
	job     windows.Handle
	pid     int
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	done    chan struct{}

	mu       sync.RWMutex
	waitErr  error
	released bool
}

func startFRPChild(binaryPath, configPath string) (frpChild, error) {
	command := exec.Command(binaryPath, "-c", configPath)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
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
	job, err := newWindowsFRPJob()
	if err != nil {
		stopWindowsFRPCommand(command, 0)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		stopWindowsFRPCommand(command, job)
		return nil, err
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if assignErr != nil {
		stopWindowsFRPCommand(command, job)
		return nil, assignErr
	}
	child := &windowsFRPChild{
		process: command.Process,
		job:     job,
		pid:     command.Process.Pid,
		stdout:  stdout,
		stderr:  stderr,
		done:    make(chan struct{}),
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

func newWindowsFRPJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func stopWindowsFRPCommand(command *exec.Cmd, job windows.Handle) {
	if job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
	if command.Process != nil {
		_ = command.Process.Kill()
	}
	_ = command.Wait()
}

func (child *windowsFRPChild) PID() int              { return child.pid }
func (child *windowsFRPChild) Stdout() io.Reader     { return child.stdout }
func (child *windowsFRPChild) Stderr() io.Reader     { return child.stderr }
func (child *windowsFRPChild) Done() <-chan struct{} { return child.done }

func (child *windowsFRPChild) WaitError() error {
	child.mu.RLock()
	defer child.mu.RUnlock()
	return child.waitErr
}

func (child *windowsFRPChild) Terminate() error {
	// A detached process group may have no console. In that case the bounded
	// Job Object termination in Kill still owns the complete child tree.
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(child.pid))
	return nil
}

func (child *windowsFRPChild) Kill() error {
	child.mu.RLock()
	job := child.job
	released := child.released
	child.mu.RUnlock()
	if released || job == 0 {
		return nil
	}
	return windows.TerminateJobObject(job, 1)
}

func (child *windowsFRPChild) Release() error {
	child.mu.Lock()
	defer child.mu.Unlock()
	if child.released || child.job == 0 {
		return nil
	}
	child.released = true
	err := windows.CloseHandle(child.job)
	child.job = 0
	return err
}
