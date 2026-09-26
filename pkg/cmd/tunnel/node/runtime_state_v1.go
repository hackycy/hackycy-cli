package node

import (
	"context"

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
