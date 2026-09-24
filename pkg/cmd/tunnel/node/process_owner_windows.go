//go:build windows

package node

// The supervisor's kill-on-close Job Object removes its child when Node exits.
type processIdentity struct {
	PID     int
	Started string
	PGID    int
	Status  string
	Command string
}

func inspectFRPSProcess(int) (*processIdentity, error)        { return nil, nil }
func findNodeFRPSProcesses(string) ([]processIdentity, error) { return nil, nil }
func terminateOwnedFRPS(processIdentity) error                { return nil }
