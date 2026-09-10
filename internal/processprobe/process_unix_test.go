//go:build !windows

package processprobe

import "testing"

func TestAliveRecognizesMissingProcess(t *testing.T) {
	// This is above the PID limit of every supported Unix release target while
	// remaining representable by the native signed pid_t.
	const missingPID = 1 << 30
	alive, err := Alive(missingPID)
	if err != nil {
		t.Fatalf("Alive(missing process) returned error: %v", err)
	}
	if alive {
		t.Fatal("Alive(missing process) = true, want false")
	}
}
