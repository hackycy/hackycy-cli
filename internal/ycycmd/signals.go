package ycycmd

import (
	"context"
	"os"
	"os/signal"
)

type ycySignalCause struct {
	signal os.Signal
}

func (cause ycySignalCause) Error() string {
	return cause.signal.String()
}

func (cause ycySignalCause) Signal() os.Signal {
	return cause.signal
}

// NewSignalContext installs the process signal cancellation policy for one
// invocation. The caller owns the returned stop function.
func NewSignalContext(parent context.Context) (context.Context, func()) {
	return newYcySignalContext(parent)
}

func newYcySignalContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, handledYcySignals()...)
	go func() {
		select {
		case received := <-signals:
			cancel(ycySignalCause{signal: received})
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		signal.Stop(signals)
		cancel(nil)
	}
}
