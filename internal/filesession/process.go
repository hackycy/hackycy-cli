package filesession

import "github.com/hackycy/hackycy-cli/internal/processprobe"

func nativeProcessAlive(pid int) (bool, error) {
	return nativeProcessAliveWithProbe(pid, processprobe.Alive)
}

func nativeProcessAliveWithProbe(pid int, probe func(int) (bool, error)) (bool, error) {
	alive, err := probe(pid)
	if err != nil {
		return true, nil
	}
	return alive, nil
}
