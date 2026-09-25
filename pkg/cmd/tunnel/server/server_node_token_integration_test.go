package server

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestServerNodeTokenRotationRecoversAfterApplicationFailureAndServerRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
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
	service := newServerNodeService(registry, observations, coordinator)
	address, stopNode := startNodeForMetadataTest(t, filepath.Join(t.TempDir(), "node"))
	defer stopNode()
	preview, err := service.preview(ctx, "admin-session", address)
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.register(ctx, "admin-session", preview.ID, "Remote", preview.Fingerprint, "claim")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		disabled, err := json.Marshal(map[string]any{"formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion, "nodeId": record.ID, "revision": 100, "state": "disabled"})
		if err == nil {
			_, _ = coordinator.wire.applySnapshot(context.Background(), address, coordinator.privateKey, record.PublicKey, record.ID, 100, disabled)
		}
	}()
	ports := reserveGoToGoFRPPorts(t)
	settings := serverNodeSettings{BindAddress: "127.0.0.1", BindPort: int64(ports.frp), VhostHTTPPort: int64(ports.http), PortRangeStart: int64(ports.proxy), PortRangeEnd: int64(ports.proxy)}
	if _, err := service.patch(ctx, record.ID, serverNodeMetadataPatch{AdvertisedFRPAddress: &serverNodeEndpoint{Host: "127.0.0.1", Port: settings.BindPort}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.saveDesired(ctx, record.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	ports.Close()
	record, err = registry.get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(ctx, record)
	_, status, err := coordinator.wire.status(ctx, address, coordinator.privateKey, record.PublicKey)
	if err != nil || status.AppliedRevision != 1 || status.Phase != "applied" {
		t.Fatalf("initial Node application = (%+v, %v)", status, err)
	}
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(ctx, environmentAdministratorID, "rotation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plane.AssignClientNode(ctx, client.ID, record.ID, true, true); err != nil {
		t.Fatal(err)
	}
	before, err := plane.BuildClientRuntime(ctx, client.ID, "local.example.test", 7000, "local-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.stageTokenRotation(ctx, record.ID, 1); err != nil {
		t.Fatal(err)
	}
	var occupied net.Listener
	for {
		occupied, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := int64(occupied.Addr().(*net.TCPAddr).Port)
		if port != settings.PortRangeStart {
			settings.BindPort = port
			break
		}
		_ = occupied.Close()
	}
	defer occupied.Close()
	if revision, err := service.saveDesired(ctx, record.ID, 2, settings); err != nil || revision != 3 {
		t.Fatalf("save staged snapshot with occupied bind port = (%d, %v)", revision, err)
	}
	record, err = registry.get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(ctx, record)
	_, failed, err := coordinator.wire.status(ctx, address, coordinator.privateKey, record.PublicKey)
	if err != nil || failed.HighestAcceptedRevision != 3 || failed.AppliedRevision != 1 || failed.FailedRevision != 3 || failed.Phase != "failed" || failed.FRPSProcess != "running" {
		t.Fatalf("failed staged Node application = (%+v, %v)", failed, err)
	}
	view, err := service.managementView(ctx, record.ID)
	if err != nil || view.TokenRotation.State != "apply_failed" {
		t.Fatalf("failed Token rotation view = (%+v, %v)", view.TokenRotation, err)
	}
	stillOld, err := plane.BuildClientRuntime(ctx, client.ID, "local.example.test", 7000, "local-token")
	if err != nil || stillOld.FRPToken != before.FRPToken || stillOld.Revision != before.Revision {
		t.Fatal("failed Node application published the staged Client credential")
	}
	settings.BindPort = int64(ports.frp)
	if revision, err := service.saveDesired(ctx, record.ID, 3, settings); err != nil || revision != 4 {
		t.Fatalf("repair staged Node snapshot = (%d, %v)", revision, err)
	}
	record, err = registry.get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.wire.applySnapshot(ctx, address, coordinator.privateKey, record.PublicKey, record.ID, 4, []byte(record.DesiredSnapshot.String))
	if err != nil || result.AppliedRevision != 4 || result.Phase != "applied" {
		t.Fatalf("Node confirmation before simulated crash = (%+v, %v)", result, err)
	}
	stillOld, err = plane.BuildClientRuntime(ctx, client.ID, "local.example.test", 7000, "local-token")
	if err != nil || stillOld.FRPToken != before.FRPToken || stillOld.Revision != before.Revision {
		t.Fatal("Client credential was published before Server reconciliation")
	}
	restarted, err := newServerNodeCoordinator(state.sessions.Directory(), registry, observations)
	if err != nil {
		t.Fatal(err)
	}
	restarted.controlPlane = plane
	events := make(chan ServerControlPlaneEvent, 1)
	unsubscribe := plane.Subscribe(func(event ServerControlPlaneEvent) { events <- event })
	defer unsubscribe()
	restarted.reconcileNode(ctx, record)
	after, err := plane.BuildClientRuntime(ctx, client.ID, "local.example.test", 7000, "local-token")
	if err != nil || after.FRPToken == before.FRPToken || after.Revision != before.Revision+1 {
		t.Fatalf("restarted Server did not promote confirmed Token = (%+v, %v)", after.Reference(), err)
	}
	select {
	case event := <-events:
		if event.ClientID != client.ID || event.Type != serverDesiredState {
			t.Fatalf("promotion event = %+v", event)
		}
	default:
		t.Fatal("promotion did not notify the assigned Client")
	}
}
