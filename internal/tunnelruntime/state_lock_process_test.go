package tunnelruntime

import (
	"errors"
	"testing"
)

func TestNativeStateLockProcessAliveTreatsProbeFailureAsAlive(t *testing.T) {
	want := errors.New("inspection denied")
	alive, err := nativeStateLockProcessAliveWithProbe(42, func(pid int) (bool, error) {
		if pid != 42 {
			t.Fatalf("probe PID = %d, want 42", pid)
		}
		return false, want
	})
	if err != nil || !alive {
		t.Fatalf("nativeStateLockProcessAliveWithProbe() = (%t, %v), want (true, nil)", alive, err)
	}
}
