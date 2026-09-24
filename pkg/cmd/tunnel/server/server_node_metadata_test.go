package server

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/pkg/cmd/tunnel/node"
)

func startNodeForMetadataTest(t *testing.T, directory string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- node.Run(ctx, node.Config{DataDir: directory, ManagementBindAddress: "127.0.0.1", ManagementPort: port}, nil)
	}()
	address := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := newNodeManagementWire().preview(context.Background(), address)
		if err == nil && len(response.PublicKey) == 32 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-finished
			t.Fatalf("Node did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return address, func() {
		cancel()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}
}

func TestServerNodeManagementAddressRequiresOriginalIdentity(t *testing.T) {
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
	nodeDirectory := filepath.Join(t.TempDir(), "original")
	address, stopOriginal := startNodeForMetadataTest(t, nodeDirectory)
	peer, err := coordinator.wire.preview(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := coordinator.wire.claim(context.Background(), address, coordinator.privateKey, peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.register(context.Background(), nodeID, "Original", address, peer.PublicKey, 0); err != nil {
		t.Fatal(err)
	}
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), "environment-admin", "remote")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.Exec(`UPDATE clients SET node_id=? WHERE internal_id=?`, nodeID, client.ID); err != nil {
		t.Fatal(err)
	}
	var previousRevision int64
	if err := state.database.QueryRow(`SELECT desired_revision FROM clients WHERE internal_id=?`, client.ID).Scan(&previousRevision); err != nil {
		t.Fatal(err)
	}
	otherAddress, stopOther := startNodeForMetadataTest(t, filepath.Join(t.TempDir(), "other"))
	name := "Renamed"
	if err := registry.patchMetadata(context.Background(), nodeID, serverNodeMetadataPatch{Name: &name, ManagementAddress: &otherAddress, AdvertisedFRPAddress: &serverNodeEndpoint{Host: "edge.example.com", Port: 7000}}); err != nil {
		t.Fatal(err)
	}
	record, err := registry.get(context.Background(), nodeID)
	if err != nil || record.ManagementAddress != address || record.PendingManagementAddress != otherAddress || record.Name != name {
		t.Fatalf("unverified address replaced active address: (%+v, %v)", record, err)
	}
	var revision int64
	if err := state.database.QueryRow(`SELECT desired_revision FROM clients WHERE internal_id=?`, client.ID).Scan(&revision); err != nil || revision != previousRevision+1 {
		t.Fatalf("advertised endpoint did not advance assigned Client: (%d, %v)", revision, err)
	}
	if coordinator.verifyCandidate(context.Background(), record) {
		t.Fatal("different Node identity activated")
	}
	record, err = registry.get(context.Background(), nodeID)
	if err != nil || record.ManagementAddress != address || record.CandidateError != "NODE_IDENTITY_MISMATCH" {
		t.Fatalf("wrong identity changed active address: (%+v, %v)", record, err)
	}
	stopOther()
	stopOriginal()
	newAddress, stopReopened := startNodeForMetadataTest(t, nodeDirectory)
	defer stopReopened()
	if err := registry.patchMetadata(context.Background(), nodeID, serverNodeMetadataPatch{ManagementAddress: &newAddress}); err != nil {
		t.Fatal(err)
	}
	record, err = registry.get(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if !coordinator.verifyCandidate(context.Background(), record) {
		t.Fatal("original Node identity did not activate at new address")
	}
	record, err = registry.get(context.Background(), nodeID)
	if err != nil || record.ManagementAddress != newAddress || record.PendingManagementAddress != "" {
		t.Fatalf("verified management address not activated: (%+v, %v)", record, err)
	}
}
