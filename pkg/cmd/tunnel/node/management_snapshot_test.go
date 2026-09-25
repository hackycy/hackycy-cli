package node

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestNodeSnapshotTransferRequiresCompleteOrderedBytesBeforeAcceptance(t *testing.T) {
	state, err := OpenState(filepath.Join(t.TempDir(), "node"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := newManagementHandler(state)
	server := httptest.NewServer(handler)
	defer server.Close()
	controller := newTestController(t, server.URL)
	open := func() {
		t.Helper()
		id, _ := controller.start(t)
		controller.finish(t, id)
	}
	open()
	if result, _ := controller.message(t, "claim", struct{}{}); result.Error != "" {
		t.Fatalf("claim: %s", result.Error)
	}
	contents, err := json.Marshal(desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: state.nodeID, Revision: 5, State: "running", BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 7001, PortRangeStart: 8000, PortRangeEnd: 8100, Token: "secret", Custom404Page: strings.Repeat("x", 70000)})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	begin := snapshotFrame{Phase: "begin", Revision: 5, NodeID: state.nodeID, TotalBytes: int64(len(contents)), SHA256: hex.EncodeToString(digest[:]), FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion}
	open()
	if result, _ := controller.message(t, "applySnapshot", begin); result.Error != "" {
		t.Fatalf("begin: %s", result.Error)
	}
	if result, _ := controller.message(t, "applySnapshot", snapshotFrame{Phase: "chunk", Offset: 1, Bytes: base64.RawURLEncoding.EncodeToString(contents[:10])}); result.Error != "BAD_FRAME" {
		t.Fatalf("out-of-order chunk: %s", result.Error)
	}
	record, err := state.readRuntime(context.Background())
	if err != nil || record.HighestRevision != 0 {
		t.Fatalf("partial transfer mutated state: %+v, %v", record, err)
	}
	open()
	if result, _ := controller.message(t, "applySnapshot", begin); result.Error != "" {
		t.Fatalf("retry begin: %s", result.Error)
	}
	for offset := 0; offset < len(contents); {
		end := min(offset+maximumSnapshotChunkBytes, len(contents))
		if result, _ := controller.message(t, "applySnapshot", snapshotFrame{Phase: "chunk", Offset: int64(offset), Bytes: base64.RawURLEncoding.EncodeToString(contents[offset:end])}); result.Error != "" {
			t.Fatalf("chunk %d: %s", offset, result.Error)
		}
		offset = end
	}
	if result, _ := controller.message(t, "applySnapshot", snapshotFrame{Phase: "commit", Revision: 5, SHA256: begin.SHA256}); result.Error != "" {
		t.Fatalf("commit: %s", result.Error)
	}
	record, err = state.readRuntime(context.Background())
	if err != nil || record.HighestRevision != 5 || record.HighestDigest != begin.SHA256 || string(record.Candidate) != string(contents) {
		t.Fatalf("committed record: %+v, %v", record, err)
	}
	open()
	if result, _ := controller.message(t, "status", struct{}{}); result.Error != "" || !strings.Contains(string(result.Body), `"highestAcceptedRevision":5`) || strings.Contains(string(result.Body), "secret") {
		t.Fatalf("status: %+v", result)
	}
	open()
	if result, _ := controller.message(t, "applySnapshot", begin); result.Error != "" {
		t.Fatalf("abandoned begin: %s", result.Error)
	}
	handler.now = func() time.Time { return time.Now().Add(sessionIdleLifetime + time.Second) }
	handler.mu.Lock()
	handler.expire()
	handler.mu.Unlock()
	files, err := filepath.Glob(filepath.Join(state.directory, "snapshot-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary snapshots left after expiry: %v, %v", files, err)
	}
}
