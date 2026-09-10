package terminal

import (
	"bytes"
	"testing"
)

func TestRendererTerminalWriterSuppressesCapabilityQueriesAcrossWrites(t *testing.T) {
	var destination bytes.Buffer
	writer := &rendererTerminalWriter{writer: &destination}

	if _, err := writer.Write([]byte("before\x1b[?2026")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if got, want := destination.String(), "before"; got != want {
		t.Fatalf("after partial probe = %q, want %q", got, want)
	}
	if _, err := writer.Write([]byte("$pvisible\x1b[?2027$pafter")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if got, want := destination.String(), "beforevisibleafter"; got != want {
		t.Fatalf("filtered output = %q, want %q", got, want)
	}
}

func TestRendererTerminalWriterFlushPreservesIncompleteNonProbe(t *testing.T) {
	var destination bytes.Buffer
	writer := &rendererTerminalWriter{writer: &destination}

	if _, err := writer.Write([]byte("text\x1b[?")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if got, want := destination.String(), "text\x1b[?"; got != want {
		t.Fatalf("flushed incomplete sequence = %q, want %q", got, want)
	}
}
