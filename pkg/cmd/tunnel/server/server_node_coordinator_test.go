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

func TestServerNodeCoordinatorRemovesOnlyAfterDurableDisabledAndStoppedFRPS(t *testing.T) {
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
	if _, err := registry.register(context.Background(), nodeID, "Remote", address, peer.PublicKey, 0); err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(context.Background(), mustNodeRecord(t, registry, nodeID))
	if _, err := registry.get(context.Background(), nodeID); err != nil {
		t.Fatalf("unconfigured Node disappeared: %v", err)
	}
	revision, err := registry.requestNodeRemoval(context.Background(), nodeID)
	if err != nil || revision != 1 {
		t.Fatalf("request removal = (%d, %v)", revision, err)
	}
	record := mustNodeRecord(t, registry, nodeID)
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(context.Background(), record)
	if pending := mustNodeRecord(t, registry, nodeID); pending.Lifecycle != "removing" || pending.DesiredRevision != revision {
		t.Fatalf("offline removal changed record: %+v", pending)
	}
	ctx, cancel = context.WithCancel(context.Background())
	finished = make(chan error, 1)
	go func() {
		finished <- node.Run(ctx, node.Config{DataDir: nodeDirectory, ManagementBindAddress: "127.0.0.1", ManagementPort: port}, nil)
	}()
	deadline = time.Now().Add(5 * time.Second)
	for {
		_, _, err = coordinator.wire.status(context.Background(), address, coordinator.privateKey, peer.PublicKey)
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
	// The transfer commits, but its response is discarded. A later status
	// query must resolve the outcome without reusing the old response.
	if _, err := coordinator.wire.applySnapshot(context.Background(), address, coordinator.privateKey, peer.PublicKey, nodeID, revision, []byte(record.DesiredSnapshot.String)); err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(context.Background(), record)
	if _, err := registry.get(context.Background(), nodeID); err == nil || !hasServerDomainCode(err, "NOT_FOUND") {
		t.Fatalf("confirmed disabled Node remains: %v", err)
	}
	remoteID, status, err := coordinator.wire.status(context.Background(), address, coordinator.privateKey, peer.PublicKey)
	if err != nil || remoteID != nodeID || status.AppliedRevision != 1 || !status.BootDisabled || !status.DisabledComplete || status.FRPSProcess != "stopped" {
		t.Fatalf("Node after removal = (%s, %+v, %v)", remoteID, status, err)
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func mustNodeRecord(t *testing.T, registry *serverNodeRegistry, id string) serverNodeRecord {
	t.Helper()
	record, err := registry.get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
