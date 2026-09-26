package node

import (
	"context"
	"fmt"

	nodeent "github.com/hackycy/hackycy-cli/ent/node"
)

func readNodeV1Runtime(ctx context.Context, client *nodeent.Client) (runtimeRecord, error) {
	row, err := client.RuntimeState.Get(ctx, 1)
	if err != nil {
		return runtimeRecord{}, err
	}
	return nodeV1RuntimeRecord(row), nil
}

func nodeV1RuntimeRecord(row *nodeent.RuntimeState) runtimeRecord {
	record := runtimeRecord{
		HighestRevision: row.HighestRevision, HighestDigest: row.HighestDigest,
		Phase: string(row.Phase), AppliedRevision: row.AppliedRevision,
		BootDisabled: row.BootDisabled, DisabledComplete: row.DisabledComplete,
		FailureCode: row.FailureCode, OwnerPID: row.OwnerPid,
		OwnerCreateTime: row.OwnerCreateTime, OwnerBinary: row.OwnerBinary,
		OwnerConfig: row.OwnerConfig,
	}
	if row.Candidate != nil {
		record.Candidate = append([]byte(nil), (*row.Candidate)...)
	}
	if row.LastGood != nil {
		record.LastGood = append([]byte(nil), (*row.LastGood)...)
	}
	return record
}

func acceptNodeV1Candidate(ctx context.Context, client *nodeent.Client, nodeID string, contents []byte) (runtimeRecord, error) {
	snapshot, err := decodeDesiredSnapshot(contents, nodeID)
	if err != nil {
		return runtimeRecord{}, fmt.Errorf("NODE_SNAPSHOT_INVALID: %w", err)
	}
	digest := snapshotDigest(contents)
	tx, err := client.Tx(ctx)
	if err != nil {
		return runtimeRecord{}, err
	}
	defer tx.Rollback()
	row, err := tx.RuntimeState.Get(ctx, 1)
	if err != nil {
		return runtimeRecord{}, err
	}
	result := nodeV1RuntimeRecord(row)
	if snapshot.Revision < result.HighestRevision {
		return result, errSnapshotStale
	}
	if snapshot.Revision == result.HighestRevision {
		if digest != result.HighestDigest {
			return result, errSnapshotConflict
		}
		return result, nil
	}
	if _, err := tx.RuntimeState.UpdateOneID(1).SetHighestRevision(snapshot.Revision).SetHighestDigest(digest).SetCandidate(contents).SetPhase("accepted").SetFailureCode("").SetDisabledComplete(false).Save(ctx); err != nil {
		return runtimeRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return runtimeRecord{}, err
	}
	result.HighestRevision, result.HighestDigest = snapshot.Revision, digest
	result.Candidate, result.Phase = append([]byte(nil), contents...), "accepted"
	result.FailureCode, result.DisabledComplete = "", false
	return result, nil
}
