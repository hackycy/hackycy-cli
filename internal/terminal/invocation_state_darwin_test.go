//go:build darwin

package terminal_test

import (
	"testing"

	"golang.org/x/sys/unix"
)

func captureInvocationTerminalState(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}

func invocationTerminalStatesEqual(before, after *unix.Termios) bool {
	left, right := *before, *after
	// macOS can set PENDIN when canonical input is restored. It records pending
	// input processing rather than a terminal configuration left in raw mode.
	left.Lflag &^= unix.PENDIN
	right.Lflag &^= unix.PENDIN
	return left == right
}

func TestInvocationTerminalStateComparison(t *testing.T) {
	before := unix.Termios{Lflag: unix.ECHO | unix.ICANON | unix.ISIG}
	for _, test := range []struct {
		name   string
		change func(*unix.Termios)
		equal  bool
	}{
		{name: "unchanged", change: func(*unix.Termios) {}, equal: true},
		{name: "pending input", change: func(state *unix.Termios) { state.Lflag |= unix.PENDIN }, equal: true},
		{name: "echo", change: func(state *unix.Termios) { state.Lflag &^= unix.ECHO }},
		{name: "canonical input", change: func(state *unix.Termios) { state.Lflag &^= unix.ICANON }},
		{name: "signals", change: func(state *unix.Termios) { state.Lflag &^= unix.ISIG }},
		{name: "input flags", change: func(state *unix.Termios) { state.Iflag ^= unix.ICRNL }},
		{name: "output flags", change: func(state *unix.Termios) { state.Oflag ^= unix.OPOST }},
		{name: "control flags", change: func(state *unix.Termios) { state.Cflag ^= unix.PARENB }},
		{name: "control characters", change: func(state *unix.Termios) { state.Cc[unix.VMIN]++ }},
		{name: "input speed", change: func(state *unix.Termios) { state.Ispeed++ }},
		{name: "output speed", change: func(state *unix.Termios) { state.Ospeed++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := before
			test.change(&after)
			if got := invocationTerminalStatesEqual(&before, &after); got != test.equal {
				t.Fatalf("terminal states equal = %t, want %t: before=%#v after=%#v", got, test.equal, before, after)
			}
			if got := invocationTerminalStatesEqual(&after, &before); got != test.equal {
				t.Fatalf("reversed terminal states equal = %t, want %t", got, test.equal)
			}
		})
	}
}
