package terminal

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

type consoleModeAPI interface {
	get(windows.Handle, *uint32) error
	set(windows.Handle, uint32) error
}

type nativeConsoleModes struct{}

func (nativeConsoleModes) get(handle windows.Handle, mode *uint32) error {
	return windows.GetConsoleMode(handle, mode)
}

func (nativeConsoleModes) set(handle windows.Handle, mode uint32) error {
	return windows.SetConsoleMode(handle, mode)
}

func prepareOutputs(output, diagnostics *os.File) (bool, bool, func() error, error) {
	var handles [2]windows.Handle
	for index, file := range []*os.File{output, diagnostics} {
		if file != nil {
			handles[index] = windows.Handle(file.Fd())
		}
	}
	controls, restore, err := prepareConsoleOutputs(handles, nativeConsoleModes{})
	return controls[0], controls[1], restore, err
}

func prepareConsoleOutputs(handles [2]windows.Handle, api consoleModeAPI) ([2]bool, func() error, error) {
	var original [2]uint32
	var console, changed, controls [2]bool
	// Snapshot both before either mutation: distinct handles may share a buffer.
	for index, handle := range handles {
		if handle == 0 {
			continue
		}
		err := api.get(handle, &original[index])
		if err == nil {
			console[index] = true
		} else if !unsupportedTerminalError(err) {
			return controls, nil, fmt.Errorf("read console output mode: %w", err)
		}
	}
	restore := func() error {
		var result error
		for index := len(handles) - 1; index >= 0; index-- {
			if changed[index] {
				result = errors.Join(result, api.set(handles[index], original[index]))
			}
		}
		return result
	}
	for index, handle := range handles {
		if !console[index] {
			continue
		}
		mode := original[index] | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
		if mode == original[index] {
			controls[index] = true
			continue
		}
		if err := api.set(handle, mode); err != nil {
			if unsupportedTerminalError(err) {
				continue
			}
			return controls, nil, errors.Join(fmt.Errorf("enable console VT output: %w", err), restore())
		}
		changed[index], controls[index] = true, true
	}
	return controls, restore, nil
}

func unsupportedTerminalError(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_HANDLE) || errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)
}

func environmentKey(key string) string { return strings.ToUpper(key) }

func withDefaultColorMetadata(environment []string) []string { return environment }
