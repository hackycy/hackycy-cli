package node

import (
	"context"
	"fmt"

	nodeent "github.com/hackycy/hackycy-cli/ent/node"
	"github.com/hackycy/hackycy-cli/ent/node/runtimestate"
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

func markNodeV1RunningApplied(ctx context.Context, client *nodeent.Client) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := tx.RuntimeState.Get(ctx, 1)
	if err != nil {
		return err
	}
	if row.Candidate == nil {
		return fmt.Errorf("Node v1 applied snapshot has no candidate")
	}
	if _, err := tx.RuntimeState.UpdateOneID(1).SetLastGood(*row.Candidate).SetAppliedRevision(row.HighestRevision).SetPhase(runtimestate.PhaseApplied).SetFailureCode("").SetBootDisabled(false).SetDisabledComplete(false).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func markNodeV1Disabling(ctx context.Context, client *nodeent.Client) error {
	_, err := client.RuntimeState.UpdateOneID(1).SetBootDisabled(true).SetDisabledComplete(false).SetPhase(runtimestate.PhaseDisabling).SetFailureCode("").Save(ctx)
	return err
}

func markNodeV1Disabled(ctx context.Context, client *nodeent.Client) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := tx.RuntimeState.Get(ctx, 1)
	if err != nil {
		return err
	}
	if _, err := tx.RuntimeState.UpdateOneID(1).ClearLastGood().SetAppliedRevision(row.HighestRevision).SetPhase(runtimestate.PhaseDisabled).SetDisabledComplete(true).SetFailureCode("").Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func setNodeV1Phase(ctx context.Context, client *nodeent.Client, phase, failure string) error {
	_, err := client.RuntimeState.UpdateOneID(1).SetPhase(runtimestate.Phase(phase)).SetFailureCode(failure).Save(ctx)
	return err
}

func setNodeV1Owner(ctx context.Context, client *nodeent.Client, pid int, createTime int64, binary, config string) error {
	_, err := client.RuntimeState.UpdateOneID(1).SetOwnerPid(pid).SetOwnerCreateTime(createTime).SetOwnerBinary(binary).SetOwnerConfig(config).Save(ctx)
	return err
}

func clearNodeV1Owner(ctx context.Context, client *nodeent.Client) error {
	return setNodeV1Owner(ctx, client, 0, 0, "", "")
}
