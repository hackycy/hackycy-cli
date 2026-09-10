package appconfig

import "github.com/hackycy/hackycy-cli/internal/processprobe"

func nativeProcessAlive(pid int) (bool, error) {
	return nativeProcessAliveWithProbe(pid, processprobe.Alive)
}

func nativeProcessAliveWithProbe(pid int, probe func(int) (bool, error)) (bool, error) {
	alive, err := probe(pid)
	if err != nil {
		// Preserve the historical denied-inspection behavior: an unknown owner
		// is treated as alive so another writer cannot take over prematurely.
		return true, nil
	}
	return alive, nil
}
