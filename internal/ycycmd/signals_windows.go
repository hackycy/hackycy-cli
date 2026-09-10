//go:build windows

package ycycmd

import "os"

func handledYcySignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
