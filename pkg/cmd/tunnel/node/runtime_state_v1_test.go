package node

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestReadNodeV1RuntimePreservesRawBytes(t *testing.T) {
	db, client, err := openEmptyNodeV1Database(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initializeNodeV1Identity(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	candidate := []byte(" { \"revision\" : 2 }\n")
	lastGood := []byte("{\"revision\":1}")
	if _, err := client.RuntimeState.UpdateOneID(1).SetHighestRevision(2).SetHighestDigest("digest").SetCandidate(candidate).SetLastGood(lastGood).SetAppliedRevision(1).SetPhase("accepted").Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	record, err := readNodeV1Runtime(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	if record.HighestRevision != 2 || record.AppliedRevision != 1 || record.Phase != "accepted" || !bytes.Equal(record.Candidate, candidate) || !bytes.Equal(record.LastGood, lastGood) {
		t.Fatalf("mapped runtime = %+v", record)
	}
}

func TestAcceptNodeV1CandidateRevisionAndRawDigest(t *testing.T) {
	db, client, err := openEmptyNodeV1Database(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initializeNodeV1Identity(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	identity, err := client.Identity.Get(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(revision int64, token string) []byte {
		t.Helper()
		contents, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: identity.NodeID, Revision: revision, State: "running", BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 7001, PortRangeStart: 8000, PortRangeEnd: 8100, Token: token})
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	first := encode(3, "first")
	if record, err := acceptNodeV1Candidate(t.Context(), client, identity.NodeID, first); err != nil || record.HighestDigest != snapshotDigest(first) || !bytes.Equal(record.Candidate, first) {
		t.Fatalf("first accept = %+v, %v", record, err)
	}
	if _, err := acceptNodeV1Candidate(t.Context(), client, identity.NodeID, first); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if _, err := acceptNodeV1Candidate(t.Context(), client, identity.NodeID, encode(2, "old")); !errors.Is(err, errSnapshotStale) {
		t.Fatalf("old revision: %v", err)
	}
	if _, err := acceptNodeV1Candidate(t.Context(), client, identity.NodeID, encode(3, "other")); !errors.Is(err, errSnapshotConflict) {
		t.Fatalf("conflicting digest: %v", err)
	}
	if _, err := acceptNodeV1Candidate(t.Context(), client, identity.NodeID, []byte(`{"invalid":true}`)); err == nil {
		t.Fatal("invalid snapshot accepted")
	}
	record, err := readNodeV1Runtime(t.Context(), client)
	if err != nil || record.HighestRevision != 3 || !bytes.Equal(record.Candidate, first) || record.Phase != "accepted" {
		t.Fatalf("state after rejected snapshots = %+v, %v", record, err)
	}
}
