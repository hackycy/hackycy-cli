package updater

import "github.com/hackycy/hackycy-cli/internal/processprobe"

func processAlive(pid int) bool {
	return processAliveWithProbe(pid, processprobe.Alive)
}

func processAliveWithProbe(pid int, probe func(int) (bool, error)) bool {
	alive, err := probe(pid)
	if err != nil {
		return false
	}
	return alive
}
