package terminal

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

type fakeConsoleModes struct {
	modes map[windows.Handle]*uint32
	fail  map[windows.Handle]error
}

func (api fakeConsoleModes) get(handle windows.Handle, mode *uint32) error {
	if value := api.modes[handle]; value != nil {
		*mode = *value
		return nil
	}
	return windows.ERROR_INVALID_HANDLE
}

func (api fakeConsoleModes) set(handle windows.Handle, mode uint32) error {
	if err := api.fail[handle]; err != nil {
		return err
	}
	*api.modes[handle] = mode
	return nil
}

func TestConsoleOutputRestoresAliasedBuffers(t *testing.T) {
	for _, sameHandle := range []bool{false, true} {
		mode := uint32(windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_WRAP_AT_EOL_OUTPUT)
		original := mode
		handles := [2]windows.Handle{1, 2}
		if sameHandle {
			handles[1] = handles[0]
		}
		api := fakeConsoleModes{modes: map[windows.Handle]*uint32{1: &mode, 2: &mode}}
		controls, restore, err := prepareConsoleOutputs(handles, api)
		if err != nil || controls != [2]bool{true, true} {
			t.Fatalf("prepare = %v, %v", controls, err)
		}
		if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
			t.Fatal("VT was not enabled")
		}
		invocation := &Invocation{restore: restore}
		if err := invocation.Close(); err != nil {
			t.Fatal(err)
		}
		if err := invocation.Close(); err != nil {
			t.Fatal(err)
		}
		if mode != original {
			t.Fatalf("mode = %x, want %x", mode, original)
		}
	}
}

func TestConsoleOutputPreparationFailures(t *testing.T) {
	for _, failure := range []error{windows.ERROR_INVALID_PARAMETER, windows.ERROR_ACCESS_DENIED} {
		stdout, stderr := uint32(1), uint32(1)
		api := fakeConsoleModes{modes: map[windows.Handle]*uint32{1: &stdout, 2: &stderr}, fail: map[windows.Handle]error{2: failure}}
		controls, restore, err := prepareConsoleOutputs([2]windows.Handle{1, 2}, api)
		if errors.Is(failure, windows.ERROR_ACCESS_DENIED) {
			if !errors.Is(err, failure) || stdout != 1 || stderr != 1 {
				t.Fatalf("partial startup leaked: %x %x %v", stdout, stderr, err)
			}
		} else {
			if err != nil || controls != [2]bool{true, false} {
				t.Fatalf("fallback = %v, %v", controls, err)
			}
			if err := restore(); err != nil {
				t.Fatal(err)
			}
			if stdout != 1 || stderr != 1 {
				t.Fatal("fallback did not restore modes")
			}
		}
	}
}
