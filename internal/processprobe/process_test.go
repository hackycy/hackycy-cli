package processprobe

import (
	"os"
	"testing"
)

func TestAliveRejectsNonPositivePID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if alive, err := Alive(pid); err != nil || alive {
			t.Fatalf("Alive(%d) = (%t, %v), want (false, nil)", pid, alive, err)
		}
	}
}

func TestAliveRecognizesCurrentProcess(t *testing.T) {
	alive, err := Alive(os.Getpid())
	if err != nil {
		t.Fatalf("Alive(current process) returned error: %v", err)
	}
	if !alive {
		t.Fatal("Alive(current process) = false, want true")
	}
}
