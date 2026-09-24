package node

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestNodeCandidateHighWaterTransactionAndRestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	state, err := OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	encode := func(revision int64, token string) []byte {
		t.Helper()
		value, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: revision, State: "running", BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 7001, PortRangeStart: 8000, PortRangeEnd: 8100, Token: token})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := encode(3, "first")
	if _, err := state.acceptCandidate(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := state.acceptCandidate(ctx, first); err != nil {
		t.Fatalf("same revision retry: %v", err)
	}
	if _, err := state.acceptCandidate(ctx, encode(2, "old")); !errors.Is(err, errSnapshotStale) {
		t.Fatalf("lower revision: %v", err)
	}
	if _, err := state.acceptCandidate(ctx, encode(3, "different")); !errors.Is(err, errSnapshotConflict) {
		t.Fatalf("conflicting digest: %v", err)
	}
	if _, err := state.acceptCandidate(ctx, []byte(`{"invalid":true}`)); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	record, err := state.readRuntime(ctx)
	if err != nil || record.HighestRevision != 3 || string(record.Candidate) != string(first) || record.Phase != "accepted" || record.AppliedRevision != 0 {
		t.Fatalf("durable record = (%+v, %v)", record, err)
	}
}
