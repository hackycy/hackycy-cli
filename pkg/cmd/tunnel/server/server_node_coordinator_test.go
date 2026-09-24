package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"github.com/hackycy/hackycy-cli/pkg/cmd/tunnel/node"
)

func TestServerNodeCoordinatorAppliesDesiredAndKeepsHistoryWhenOffline(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := newServerNodeObservations(state.database)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := newServerNodeCoordinator(state.sessions.Directory(), registry, observations)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	nodeDirectory := filepath.Join(t.TempDir(), "node")
	go func() {
		finished <- node.Run(ctx, node.Config{DataDir: nodeDirectory, ManagementBindAddress: "127.0.0.1", ManagementPort: port}, nil)
	}()
	address := "http://127.0.0.1:" + strconv.Itoa(port)
	var peer nodeManagementPeer
	deadline := time.Now().Add(5 * time.Second)
	for {
		peer, err = coordinator.wire.preview(context.Background(), address)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-finished
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	nodeID, err := coordinator.wire.claim(context.Background(), address, coordinator.privateKey, peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.register(context.Background(), nodeID, "Remote", address, peer.PublicKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(map[string]any{"formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion, "nodeId": nodeID, "revision": 1, "state": "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(snapshot)
	record.DesiredSnapshot.String = string(snapshot)
	record.DesiredSnapshot.Valid = true
	record.DesiredHash.String = hex.EncodeToString(digest[:])
	record.DesiredHash.Valid = true
	record.DesiredRevision = 1
	if _, err := state.database.Exec(`UPDATE remote_nodes SET desired_revision=?,desired_hash=?,desired_snapshot=? WHERE node_id=?`, 1, record.DesiredHash.String, snapshot, nodeID); err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(context.Background(), record)
	view, err := observations.read(context.Background(), nodeID)
	if err != nil || view.ManagementState != "reachable" || view.Status == nil || view.Status.AppliedRevision != 1 || !view.Status.DisabledComplete {
		t.Fatalf("reconciliation = (%+v, %v)", view, err)
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(context.Background(), record)
	view, err = observations.read(context.Background(), nodeID)
	if err != nil || view.ManagementState != "unreachable" || view.FRPSState != "unknown" || view.LastKnown == nil || view.LastKnown.AppliedRevision != 1 {
		t.Fatalf("offline projection = (%+v, %v)", view, err)
	}
}
