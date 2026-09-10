//go:build windows

// Package processprobe owns the small native capability used to determine
// whether a process ID is currently alive.
package processprobe

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

var tasklistOutput = func(pid int) ([]byte, error) {
	return exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
}

// Alive reports whether pid identifies a live process.
//
// Windows has no portable equivalent of Unix kill(pid, 0), so the query uses
// the same tasklist contract as the pre-refactor owner implementations.
// Command failures are returned so each owner can retain its established
// fallback policy.
func Alive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	output, err := tasklistOutput(pid)
	if err != nil {
		return false, err
	}
	return outputContainsPID(string(output), pid), nil
}

func outputContainsPID(output string, pid int) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		foundPID, parseErr := strconv.Atoi(fields[1])
		if parseErr == nil && foundPID == pid {
			return true
		}
	}
	return false
}
