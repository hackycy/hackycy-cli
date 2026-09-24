package node

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
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
	OwnerStarted     string
	OwnerBinary      string
	OwnerConfig      string
}

func (state *State) readRuntime(ctx context.Context) (runtimeRecord, error) {
	var result runtimeRecord
	err := state.db.QueryRowContext(ctx, `SELECT highest_revision, highest_digest, candidate, phase, applied_revision, last_good, boot_disabled, disabled_complete, failure_code, owner_pid, owner_create_time, owner_started, owner_binary, owner_config FROM node_runtime WHERE id=1`).Scan(
		&result.HighestRevision, &result.HighestDigest, &result.Candidate, &result.Phase, &result.AppliedRevision, &result.LastGood, &result.BootDisabled, &result.DisabledComplete, &result.FailureCode, &result.OwnerPID, &result.OwnerCreateTime, &result.OwnerStarted, &result.OwnerBinary, &result.OwnerConfig)
	return result, err
}

// acceptCandidate changes the high water mark only after the entire snapshot is valid.
func (state *State) acceptCandidate(ctx context.Context, contents []byte) (runtimeRecord, error) {
	snapshot, err := decodeDesiredSnapshot(contents, state.nodeID)
	if err != nil {
		return runtimeRecord{}, fmt.Errorf("NODE_SNAPSHOT_INVALID: %w", err)
	}
	digestBytes := sha256.Sum256(contents)
	digest := hex.EncodeToString(digestBytes[:])
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return runtimeRecord{}, err
	}
	defer tx.Rollback()
	var result runtimeRecord
	err = tx.QueryRowContext(ctx, `SELECT highest_revision, highest_digest, candidate, phase, applied_revision, last_good, boot_disabled, disabled_complete, failure_code, owner_pid, owner_create_time, owner_started, owner_binary, owner_config FROM node_runtime WHERE id=1`).Scan(
		&result.HighestRevision, &result.HighestDigest, &result.Candidate, &result.Phase, &result.AppliedRevision, &result.LastGood, &result.BootDisabled, &result.DisabledComplete, &result.FailureCode, &result.OwnerPID, &result.OwnerCreateTime, &result.OwnerStarted, &result.OwnerBinary, &result.OwnerConfig)
	if err != nil {
		return runtimeRecord{}, err
	}
	if snapshot.Revision < result.HighestRevision {
		return result, errSnapshotStale
	}
	if snapshot.Revision == result.HighestRevision {
		if digest != result.HighestDigest {
			return result, errSnapshotConflict
		}
		return result, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE node_runtime SET highest_revision=?, highest_digest=?, candidate=?, phase='accepted', failure_code='', disabled_complete=0 WHERE id=1`, snapshot.Revision, digest, contents); err != nil {
		return runtimeRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return runtimeRecord{}, err
	}
	result.HighestRevision, result.HighestDigest, result.Candidate, result.Phase, result.FailureCode, result.DisabledComplete = snapshot.Revision, digest, append([]byte(nil), contents...), "accepted", "", false
	return result, nil
}

func (state *State) updateRuntime(ctx context.Context, update func(*sql.Tx) error) error {
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := update(tx); err != nil {
		return err
	}
	return tx.Commit()
}
