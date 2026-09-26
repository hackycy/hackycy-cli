package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/pkg/cmd/tunnel/node"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

func TestServerNodeEndpointConstraintMappingAndRollback(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	firstID := "0123456789abcdef0123456789abcdef"
	secondID := "fedcba9876543210fedcba9876543210"
	for index, id := range []string{firstID, secondID} {
		key := make([]byte, 32)
		key[0] = byte(index + 1)
		if _, err := registry.register(ctx, id, "Remote", fmt.Sprintf("http://127.0.0.1:%d", 7600+index), key, 0); err != nil {
			t.Fatal(err)
		}
	}
	endpoint := &serverNodeEndpoint{Host: "EDGE.example.test", Port: 7000}
	if err := registry.patchMetadata(ctx, firstID, serverNodeMetadataPatch{AdvertisedFRPAddress: endpoint}); err != nil {
		t.Fatal(err)
	}
	newName := "Changed"
	assertServerDomainCode(t, registry.patchMetadata(ctx, secondID, serverNodeMetadataPatch{Name: &newName, AdvertisedFRPAddress: endpoint}), "NODE_RESOURCE_CONFLICT")
	second, err := registry.get(ctx, secondID)
	if err != nil || second.Name != "Remote" || second.AdvertisedFRPHost.Valid {
		t.Fatalf("second Node after rejected patch = (%+v, %v), want unchanged", second, err)
	}

	connection, err := state.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := serverEntOnConnection(connection)
	assertServerDomainCode(t, mapNodeEndpointConstraintError(ctx, client, secondID, endpoint, sqlite3.CONSTRAINT_UNIQUE), "NODE_RESOURCE_CONFLICT")
	for _, original := range []error{
		fmt.Errorf("nodes_frp_endpoint unique: %w", sqlite3.CONSTRAINT_CHECK),
		sqlite3.CONSTRAINT_FOREIGNKEY,
		sqlite3.CONSTRAINT_PRIMARYKEY,
		sqlite3.CONSTRAINT,
	} {
		mapped := mapNodeEndpointConstraintError(ctx, client, secondID, endpoint, original)
		var domain *ServerDomainError
		if errors.As(mapped, &domain) || !errors.Is(mapped, original) {
			t.Fatalf("mapped error = %v, want internal error preserving %v", mapped, original)
		}
	}
	for _, mapped := range []error{
		mapNodeEndpointConstraintError(ctx, client, secondID, &serverNodeEndpoint{Host: "free.example.test", Port: 7000}, sqlite3.CONSTRAINT_UNIQUE),
		mapNodeEndpointConstraintError(ctx, client, secondID, nil, sqlite3.CONSTRAINT_UNIQUE),
	} {
		var domain *ServerDomainError
		if errors.As(mapped, &domain) || !errors.Is(mapped, sqlite3.CONSTRAINT_UNIQUE) {
			t.Fatalf("unattributed unique error = %v, want internal error", mapped)
		}
	}
}

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

func TestServerNodeEndpointPatchNotifiesAssignedOnlineClient(t *testing.T) {
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
	plane := openServerControlPlane(t, state)
	coordinator.controlPlane = plane
	service := newServerNodeService(registry, observations, coordinator)
	id := "fabcde0123456789abcdef0123456789"
	if _, err := registry.register(t.Context(), id, "Remote", "http://127.0.0.1:7600", make([]byte, 32), 0); err != nil {
		t.Fatal(err)
	}
	client, err := plane.CreateClient(t.Context(), environmentAdministratorID, "endpoint")
	if err != nil {
		t.Fatal(err)
	}
	client, err = plane.AssignClientNode(t.Context(), client.ID, id, true, true)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan ServerControlPlaneEvent, 2)
	unsubscribe := plane.Subscribe(func(event ServerControlPlaneEvent) { events <- event })
	defer unsubscribe()
	endpoint := &serverNodeEndpoint{Host: "remote.example.test", Port: 7000}
	if _, err := service.patch(t.Context(), id, serverNodeMetadataPatch{AdvertisedFRPAddress: endpoint}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.ClientID != client.ID || event.Type != serverDesiredState {
			t.Fatalf("endpoint event = %+v", event)
		}
	default:
		t.Fatal("endpoint update did not notify assigned Client")
	}
	if _, err := service.patch(t.Context(), id, serverNodeMetadataPatch{AdvertisedFRPAddress: endpoint}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("unchanged endpoint emitted %+v", event)
	default:
	}
	updated, err := plane.GetClient(t.Context(), client.ID)
	if err != nil || updated.DesiredRevision != client.DesiredRevision+1 {
		t.Fatalf("assigned Client revision = (%d, %v)", updated.DesiredRevision, err)
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
