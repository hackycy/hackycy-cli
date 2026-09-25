package server

import (
	"errors"
	"sync"
	"testing"
	"time"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

type serverAgentFrameWriterStub struct {
	mu          sync.Mutex
	writes      []any
	started     chan struct{}
	release     chan struct{}
	startOne    sync.Once
	err         error
	deadlineErr error
}

func (writer *serverAgentFrameWriterStub) SetWriteDeadline(time.Time) error {
	return writer.deadlineErr
}

func (writer *serverAgentFrameWriterStub) WriteJSON(value any) error {
	writer.mu.Lock()
	writer.writes = append(writer.writes, value)
	started := writer.started
	release := writer.release
	err := writer.err
	writer.mu.Unlock()
	if started != nil {
		writer.startOne.Do(func() { close(started) })
	}
	if release != nil {
		<-release
	}
	return err
}

func (writer *serverAgentFrameWriterStub) values() []any {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return append([]any(nil), writer.writes...)
}

func TestServerAgentOutboundCoalescesDesiredSnapshotsAndPrioritizesRevoke(t *testing.T) {
	writer := &serverAgentFrameWriterStub{started: make(chan struct{}), release: make(chan struct{})}
	outbound := newServerAgentOutbound(writer, nil)
	t.Cleanup(outbound.Close)
	first := tunnelruntime.DesiredState{Runtime: tunnelruntime.ClientRuntime{Revision: 1}, DesiredRestartGeneration: 1}
	outbound.Write(first)
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("first desired write did not start")
	}
	outbound.Write(tunnelruntime.DesiredState{Runtime: tunnelruntime.ClientRuntime{Revision: 2}, DesiredRestartGeneration: 1})
	outbound.Write(tunnelruntime.DesiredState{Runtime: tunnelruntime.ClientRuntime{Revision: 2}, DesiredRestartGeneration: 3})
	revokeDone := make(chan error, 1)
	go func() {
		revokeDone <- outbound.Write(tunnelruntime.Revoke{Reason: "rotated"})
	}()
	deadline := time.Now().Add(time.Second)
	for len(outbound.priority) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(outbound.priority) == 0 {
		t.Fatal("revoke was not queued")
	}
	close(writer.release)
	select {
	case err := <-revokeDone:
		if err != nil {
			t.Fatalf("revoke write error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("revoke write did not finish")
	}
	outbound.Close()
	values := writer.values()
	if len(values) < 2 {
		t.Fatalf("writes = %#v", values)
	}
	if desired, ok := values[0].(tunnelruntime.DesiredState); !ok || desired.Runtime.Revision != 1 {
		t.Fatalf("first write = %#v", values[0])
	}
	if revoke, ok := values[1].(tunnelruntime.Revoke); !ok || revoke.Reason != "rotated" {
		t.Fatalf("priority write = %#v", values[1])
	}
	if len(values) > 2 {
		desired, ok := values[2].(tunnelruntime.DesiredState)
		if !ok || desired.Runtime.Revision != 2 || desired.DesiredRestartGeneration != 3 {
			t.Fatalf("coalesced desired write = %#v", values[2])
		}
	}
}

func TestServerAgentOutboundClosesAfterWriteFailureAndWakesWaiters(t *testing.T) {
	wantErr := errors.New("slow connection failed")
	writer := &serverAgentFrameWriterStub{err: wantErr}
	failed := make(chan error, 1)
	outbound := newServerAgentOutbound(writer, func(err error) { failed <- err })
	t.Cleanup(outbound.Close)
	if err := outbound.Write(tunnelruntime.Revoke{Reason: "deleted"}); !errors.Is(err, wantErr) {
		t.Fatalf("failed write error = %v, want %v", err, wantErr)
	}
	select {
	case err := <-failed:
		if !errors.Is(err, wantErr) {
			t.Fatalf("failure callback error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("failure callback was not called")
	}
	if err := outbound.Write(tunnelruntime.Revoke{Reason: "again"}); err == nil {
		t.Fatal("write after failure error = nil")
	}
}

func TestServerAgentOutboundFailsWhenWriteDeadlineCannotBeSet(t *testing.T) {
	wantErr := errors.New("deadline failed")
	writer := &serverAgentFrameWriterStub{deadlineErr: wantErr}
	outbound := newServerAgentOutbound(writer, nil)
	t.Cleanup(outbound.Close)
	if err := outbound.Write(tunnelruntime.Revoke{Reason: "deleted"}); !errors.Is(err, wantErr) {
		t.Fatalf("deadline error = %v, want %v", err, wantErr)
	}
	if values := writer.values(); len(values) != 0 {
		t.Fatalf("writes after deadline failure = %#v", values)
	}
}
