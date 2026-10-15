//go:build !windows && !darwin

package terminal_test

import (
	"reflect"

	"golang.org/x/term"
)

func captureInvocationTerminalState(fd int) (*term.State, error) {
	return term.GetState(fd)
}

func invocationTerminalStatesEqual(before, after *term.State) bool {
	return reflect.DeepEqual(before, after)
}
