package updater

import (
	"errors"
	"testing"
)

func TestProcessAliveTreatsProbeFailureAsNotAlive(t *testing.T) {
	want := errors.New("inspection denied")
	alive := processAliveWithProbe(42, func(pid int) (bool, error) {
		if pid != 42 {
			t.Fatalf("probe PID = %d, want 42", pid)
		}
		return true, want
	})
	if alive {
		t.Fatal("processAliveWithProbe() = true, want false")
	}
}
