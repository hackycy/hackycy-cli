//go:build darwin || linux

package node

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type processIdentity struct {
	PID     int
	Started string
	PGID    int
	Status  string
	Command string
}

func parseProcessLine(line string) (processIdentity, error) {
	fields := strings.Fields(line)
	if len(fields) < 9 {
		return processIdentity{}, fmt.Errorf("incomplete process identity")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return processIdentity{}, err
	}
	pgid, err := strconv.Atoi(fields[6])
	if err != nil {
		return processIdentity{}, err
	}
	return processIdentity{PID: pid, Started: strings.Join(fields[1:6], " "), PGID: pgid, Status: fields[7], Command: strings.Join(fields[8:], " ")}, nil
}

func inspectFRPSProcess(pid int) (*processIdentity, error) {
	if pid <= 0 {
		return nil, nil
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=,lstart=,pgid=,stat=,command=", "-ww").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	line := strings.TrimSpace(string(output))
	if line == "" {
		return nil, nil
	}
	process, err := parseProcessLine(line)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(process.Status, "Z") {
		return nil, nil
	}
	return &process, nil
}

func findNodeFRPSProcesses(directory string) ([]processIdentity, error) {
	output, err := exec.Command("ps", "-axo", "pid=,lstart=,pgid=,stat=,command=", "-ww").Output()
	if err != nil {
		return nil, err
	}
	marker := " -c " + filepath.Join(directory, "frps-")
	var found []processIdentity
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		process, err := parseProcessLine(line)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(process.Status, "Z") {
			found = append(found, process)
		}
	}
	return found, nil
}

func terminateOwnedFRPS(expected processIdentity) error {
	current, err := inspectFRPSProcess(expected.PID)
	if err != nil || current == nil {
		return err
	}
	if current.Started != expected.Started || current.Command != expected.Command || current.PGID != expected.PID {
		return fmt.Errorf("FRPS ownership is uncertain")
	}
	if err := syscall.Kill(-expected.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		current, err = inspectFRPSProcess(expected.PID)
		if err != nil || current == nil {
			return err
		}
		if current.Started != expected.Started || current.Command != expected.Command {
			return fmt.Errorf("FRPS ownership changed during termination")
		}
	}
	current, err = inspectFRPSProcess(expected.PID)
	if err != nil || current == nil {
		return err
	}
	if current.Started != expected.Started || current.Command != expected.Command || current.PGID != expected.PID {
		return fmt.Errorf("FRPS ownership changed before forced termination")
	}
	if err := syscall.Kill(-expected.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		current, err = inspectFRPSProcess(expected.PID)
		if err != nil || current == nil {
			return err
		}
	}
	return fmt.Errorf("owned FRPS did not exit")
}
