package node

import (
	"bytes"
	"testing"
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
