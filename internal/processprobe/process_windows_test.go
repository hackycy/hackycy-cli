//go:build windows

package processprobe

import (
	"errors"
	"testing"
)

func TestAliveParsesTasklistPID(t *testing.T) {
	original := tasklistOutput
	t.Cleanup(func() { tasklistOutput = original })
	tasklistOutput = func(pid int) ([]byte, error) {
		if pid != 42 {
			t.Fatalf("tasklist PID = %d, want 42", pid)
		}
		return []byte("Image Name                     PID Session Name        Session#    Mem Usage\r\napp.exe                       42 Console                    1     1,024 K\r\n"), nil
	}
	alive, err := Alive(42)
	if err != nil || !alive {
		t.Fatalf("Alive(42) = (%t, %v), want (true, nil)", alive, err)
	}

	tasklistOutput = func(int) ([]byte, error) {
		return []byte("INFO: No tasks are running which match the specified criteria.\r\n"), nil
	}
	alive, err = Alive(42)
	if err != nil || alive {
		t.Fatalf("Alive(missing PID) = (%t, %v), want (false, nil)", alive, err)
	}
}

func TestAliveReturnsTasklistFailure(t *testing.T) {
	original := tasklistOutput
	t.Cleanup(func() { tasklistOutput = original })
	want := errors.New("tasklist unavailable")
	tasklistOutput = func(int) ([]byte, error) { return nil, want }
	alive, err := Alive(42)
	if alive || !errors.Is(err, want) {
		t.Fatalf("Alive(42) = (%t, %v), want (false, tasklist failure)", alive, err)
	}
}
