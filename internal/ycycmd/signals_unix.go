//go:build !windows

package ycycmd

import (
	"os"
	"syscall"
)

func handledYcySignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
