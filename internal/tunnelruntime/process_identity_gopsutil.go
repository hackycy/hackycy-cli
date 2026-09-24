package tunnelruntime

import (
	"fmt"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

type gopsutilProcessInspector struct{}

func (gopsutilProcessInspector) Inspect(pid int) (ProcessSnapshot, error) {
	if pid <= 0 {
		return ProcessSnapshot{}, invalidProcessPID(pid)
	}
	exists, err := process.PidExists(int32(pid))
	if err != nil {
		return ProcessSnapshot{}, fmt.Errorf("inspect process %d: %w", pid, err)
	}
	if !exists {
		return ProcessSnapshot{}, fmt.Errorf("%w: PID %d", ErrProcessNotFound, pid)
	}
	current, err := process.NewProcess(int32(pid))
	if err != nil {
		return ProcessSnapshot{}, fmt.Errorf("%w: PID %d: %v", ErrProcessNotFound, pid, err)
	}
	if status, statusErr := current.Status(); statusErr == nil && containsZombie(status) {
		return ProcessSnapshot{}, fmt.Errorf("%w: PID %d is a zombie", ErrProcessNotFound, pid)
	}
	executable, err := current.Exe()
	if err != nil {
		return ProcessSnapshot{}, fmt.Errorf("inspect executable for PID %d: %w", pid, err)
	}
	args, err := current.CmdlineSlice()
	if err != nil {
		return ProcessSnapshot{}, fmt.Errorf("inspect command line for PID %d: %w", pid, err)
	}
	createTime, err := current.CreateTime()
	if err != nil {
		createTime = 0
	}
	return ProcessSnapshot{PID: pid, CreateTimeUnixMs: createTime, Executable: executable, Args: args}, nil
}

func containsZombie(status []string) bool {
	for _, value := range status {
		if strings.EqualFold(value, process.Zombie) || strings.EqualFold(value, "zombie") {
			return true
		}
	}
	return false
}
