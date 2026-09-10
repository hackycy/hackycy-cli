package tunnelruntime

import "github.com/hackycy/hackycy-cli/internal/processprobe"

func nativeStateLockProcessAlive(pid int) (bool, error) {
	return nativeStateLockProcessAliveWithProbe(pid, processprobe.Alive)
}

func nativeStateLockProcessAliveWithProbe(pid int, probe func(int) (bool, error)) (bool, error) {
	alive, err := probe(pid)
	if err != nil {
		return true, nil
	}
	return alive, nil
}
