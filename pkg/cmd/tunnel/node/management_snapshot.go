package node

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func (handler *managementHandler) applySnapshot(session *managementSession, message secureMessage, response *secureMessage) (string, bool) {
	var frame snapshotFrame
	if err := json.Unmarshal(message.Body, &frame); err != nil {
		return "BAD_FRAME", false
	}
	switch frame.Phase {
	case "begin":
		if session.transfer != nil || frame.NodeID != handler.state.nodeID || frame.Revision < 1 || frame.TotalBytes < 1 || frame.TotalBytes > maximumSnapshotBytes || frame.FormatVersion != nodeSnapshotFormatVersion || frame.FRPVersion != tunnelruntime.FRPVersion || len(frame.SHA256) != 64 {
			return "NODE_SNAPSHOT_INVALID", false
		}
		if _, err := hex.DecodeString(frame.SHA256); err != nil {
			return "NODE_SNAPSHOT_INVALID", false
		}
		file, err := os.CreateTemp(handler.state.directory, "snapshot-*")
		if err != nil {
			return "UNAVAILABLE", false
		}
		session.transfer = &snapshotTransfer{requestID: message.RequestID, revision: frame.Revision, total: frame.TotalBytes, digest: frame.SHA256, file: file}
		return "", true
	case "chunk":
		transfer := session.transfer
		if transfer == nil || transfer.requestID != message.RequestID || frame.Offset != transfer.received || frame.Bytes == "" || frame.Revision != 0 || frame.SHA256 != "" {
			return "BAD_FRAME", false
		}
		chunk, err := base64.RawURLEncoding.DecodeString(frame.Bytes)
		if err != nil || len(chunk) == 0 || len(chunk) > maximumSnapshotChunkBytes || transfer.received+int64(len(chunk)) > transfer.total {
			return "NODE_SNAPSHOT_TOO_LARGE", false
		}
		if _, err := transfer.file.Write(chunk); err != nil {
			return "UNAVAILABLE", false
		}
		transfer.received += int64(len(chunk))
		return "", true
	case "commit":
		transfer := session.transfer
		if transfer == nil || transfer.requestID != message.RequestID || frame.Revision != transfer.revision || frame.SHA256 != transfer.digest || transfer.received != transfer.total {
			return "NODE_SNAPSHOT_INVALID", false
		}
		if err := transfer.file.Sync(); err != nil {
			return "UNAVAILABLE", false
		}
		if _, err := transfer.file.Seek(0, io.SeekStart); err != nil {
			return "UNAVAILABLE", false
		}
		contents, err := io.ReadAll(transfer.file)
		if err != nil {
			return "UNAVAILABLE", false
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != transfer.digest {
			return "NODE_SNAPSHOT_INVALID", false
		}
		snapshot, err := decodeDesiredSnapshot(contents, handler.state.nodeID)
		if err != nil || snapshot.Revision != transfer.revision {
			return "NODE_SNAPSHOT_INVALID", false
		}
		record, err := handler.state.acceptCandidate(context.Background(), contents)
		if errors.Is(err, errSnapshotStale) {
			return "NODE_REVISION_STALE", false
		}
		if errors.Is(err, errSnapshotConflict) {
			return "NODE_REVISION_CONFLICT", false
		}
		if err != nil {
			return "UNAVAILABLE", false
		}
		resultCode := ""
		if handler.runtime != nil {
			record, resultCode = handler.runtime.applyAccepted(context.Background())
		}
		failedRevision := int64(0)
		if record.Phase == "failed" {
			failedRevision = record.HighestRevision
		}
		response.Body, _ = json.Marshal(map[string]any{"highestAcceptedRevision": record.HighestRevision, "sha256": record.HighestDigest, "appliedRevision": record.AppliedRevision, "failedRevision": failedRevision, "phase": record.Phase, "failureCode": record.FailureCode})
		return resultCode, false
	default:
		return "BAD_FRAME", false
	}
}
