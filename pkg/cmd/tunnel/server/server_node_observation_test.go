package server

import (
	"context"
	"crypto/rand"
	"testing"
	"time"
)

func TestServerNodeObservationSeparatesSavedHistoryFromFreshRuntime(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	id := "abcdef0123456789abcdef0123456789"
	if _, err := registry.register(context.Background(), id, "Remote", "http://127.0.0.1:7600", publicKey, 0); err != nil {
		t.Fatal(err)
	}
	observations, err := newServerNodeObservations(state.database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	observations.now = func() time.Time { return now }
	if err := observations.recordStatus(context.Background(), id, nodeStatus{Claimed: true, AppliedRevision: 2, FRPSProcess: "running", ObservedAt: now.Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	view, err := observations.read(context.Background(), id)
	if err != nil || view.ManagementState != "reachable" || view.FRPSState != "running" || view.Stale {
		t.Fatalf("fresh observation = (%+v, %v)", view, err)
	}
	restarted, err := newServerNodeObservations(state.database)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = observations.now
	view, err = restarted.read(context.Background(), id)
	if err != nil || view.ManagementState != "unknown" || view.FRPSState != "unknown" || view.LastKnown == nil || view.LastKnown.FRPSProcess != "running" {
		t.Fatalf("restart projected cached running as current: (%+v, %v)", view, err)
	}
	if err := observations.recordFailure(context.Background(), id, "NODE_PROTOCOL_INCOMPATIBLE"); err != nil {
		t.Fatal(err)
	}
	view, err = observations.read(context.Background(), id)
	if err != nil || view.ManagementState != "incompatible" || view.FRPSState != "unknown" || view.LastKnown.FRPSProcess != "running" {
		t.Fatalf("failure lost history or reported running: (%+v, %v)", view, err)
	}
	now = now.Add(nodeObservationFreshness)
	view, err = observations.read(context.Background(), id)
	if err != nil || view.ManagementState != "unknown" || view.FRPSState != "unknown" {
		t.Fatalf("expired observation = (%+v, %v)", view, err)
	}
}
