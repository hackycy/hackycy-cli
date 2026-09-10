package filesession

import (
	"errors"
	"testing"
)

func TestNativeProcessAliveTreatsProbeFailureAsAlive(t *testing.T) {
	want := errors.New("inspection denied")
	alive, err := nativeProcessAliveWithProbe(42, func(pid int) (bool, error) {
		if pid != 42 {
			t.Fatalf("probe PID = %d, want 42", pid)
		}
		return false, want
	})
	if err != nil || !alive {
		t.Fatalf("nativeProcessAliveWithProbe() = (%t, %v), want (true, nil)", alive, err)
	}
}
