package terminal

import (
	"errors"
	"io"
	"sync"
)

// LeaseAwareDiagnosticWriter serializes diagnostics and defers them while a
// renderer has exclusive ownership of the terminal diagnostic stream.
type LeaseAwareDiagnosticWriter struct {
	destination io.Writer

	leaseMu sync.Mutex
	mu      sync.Mutex
	leased  bool
	pending [][]byte
}

// NewLeaseAwareDiagnosticWriter creates a diagnostic writer for one stderr destination.
func NewLeaseAwareDiagnosticWriter(destination io.Writer) *LeaseAwareDiagnosticWriter {
	if destination == nil {
		destination = io.Discard
	}
	return &LeaseAwareDiagnosticWriter{destination: destination}
}

// Write writes a normal diagnostic immediately unless a renderer lease is active.
func (writer *LeaseAwareDiagnosticWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.leased {
		writer.pending = append(writer.pending, append([]byte(nil), value...))
		return len(value), nil
	}
	return writer.destination.Write(value)
}

// AcquireRendererLease obtains exclusive temporary ownership of the diagnostic stream.
func (writer *LeaseAwareDiagnosticWriter) AcquireRendererLease() *RendererLease {
	writer.leaseMu.Lock()
	writer.mu.Lock()
	writer.leased = true
	writer.mu.Unlock()
	return &RendererLease{owner: writer}
}

func (writer *LeaseAwareDiagnosticWriter) writeRenderer(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.destination.Write(value)
}

func (writer *LeaseAwareDiagnosticWriter) releaseRendererLease() error {
	writer.mu.Lock()
	writer.leased = false
	pending := writer.pending
	writer.pending = nil
	var flushErrors []error
	for _, value := range pending {
		_, err := writer.destination.Write(value)
		if err != nil {
			flushErrors = append(flushErrors, err)
		}
	}
	writer.mu.Unlock()
	writer.leaseMu.Unlock()
	return errors.Join(flushErrors...)
}

// RendererLease is exclusive temporary access to a diagnostic stream for one view.
type RendererLease struct {
	owner  *LeaseAwareDiagnosticWriter
	mu     sync.Mutex
	closed bool
}

// Writer returns the direct diagnostic stream for the active renderer.
func (lease *RendererLease) Writer() io.Writer {
	return lease
}

// Write writes renderer output without deferring it behind the active lease.
func (lease *RendererLease) Write(value []byte) (int, error) {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return 0, io.ErrClosedPipe
	}
	return lease.owner.writeRenderer(value)
}

// Close restores normal diagnostic delivery and flushes deferred records in order.
func (lease *RendererLease) Close() error {
	lease.mu.Lock()
	if lease.closed {
		lease.mu.Unlock()
		return nil
	}
	lease.closed = true
	lease.mu.Unlock()
	return lease.owner.releaseRendererLease()
}
