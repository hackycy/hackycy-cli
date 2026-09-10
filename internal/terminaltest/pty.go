package terminaltest

import (
	"os"
	"os/exec"
)

// PTYProcess is a subprocess connected to one controlled pseudoterminal.
type PTYProcess struct {
	terminal *os.File
	command  *exec.Cmd
}

// Terminal returns the controller side of the pseudoterminal.
func (process *PTYProcess) Terminal() *os.File {
	return process.terminal
}

// Wait waits for the controlled subprocess to finish.
func (process *PTYProcess) Wait() error {
	return process.command.Wait()
}

// Close releases the controller side of the pseudoterminal.
func (process *PTYProcess) Close() error {
	if process.terminal == nil {
		return nil
	}
	return process.terminal.Close()
}
