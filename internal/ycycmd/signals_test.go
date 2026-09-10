package ycycmd

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewSignalContextFollowsParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, stop := NewSignalContext(parent)
	defer stop()

	cancelParent()
	select {
	case <-ctx.Done():
		if !errors.Is(context.Cause(ctx), context.Canceled) {
			t.Fatalf("signal context cause = %v, want parent cancellation", context.Cause(ctx))
		}
	case <-time.After(time.Second):
		t.Fatal("signal context did not observe parent cancellation")
	}
}

func TestNewSignalContextStopCancelsContext(t *testing.T) {
	ctx, stop := NewSignalContext(context.Background())
	stop()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("stopped signal context is still active")
	}
}
