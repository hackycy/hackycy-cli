package node

import (
	"context"
	"errors"
)

var errSnapshotStale = errors.New("NODE_REVISION_STALE")
var errSnapshotConflict = errors.New("NODE_REVISION_CONFLICT")

type runtimeRecord struct {
	HighestRevision  int64
	HighestDigest    string
	Candidate        []byte
	Phase            string
	AppliedRevision  int64
	LastGood         []byte
	BootDisabled     bool
	DisabledComplete bool
	FailureCode      string
	OwnerPID         int
	OwnerCreateTime  int64
	OwnerBinary      string
	OwnerConfig      string
}

func (state *State) readRuntime(ctx context.Context) (runtimeRecord, error) {
	return readNodeV1Runtime(ctx, state.client)
}

func (state *State) acceptCandidate(ctx context.Context, contents []byte) (runtimeRecord, error) {
	return acceptNodeV1Candidate(ctx, state.client, state.nodeID, contents)
}
