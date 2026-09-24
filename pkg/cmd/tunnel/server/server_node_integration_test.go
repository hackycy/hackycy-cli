package server

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestServerNodeRealFRPSClaimApplyAndExplicitReadd(t *testing.T) {
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
	stopped := false
	defer func() {
		if !stopped {
			stopNode()
		}
	}()
	preview, err := service.preview(ctx, "admin-session", address)
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.register(ctx, "admin-session", preview.ID, "Remote", preview.Fingerprint, "claim")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if stopped {
			return
		}
		disabled, err := json.Marshal(map[string]any{"formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion, "nodeId": record.ID, "revision": 100, "state": "disabled"})
		if err == nil {
			_, _ = coordinator.wire.applySnapshot(context.Background(), address, coordinator.privateKey, record.PublicKey, record.ID, 100, disabled)
		}
	}()
	freePort := func() int {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		return port
	}
	bind, httpPort, pool := freePort(), freePort(), freePort()
	for bind == httpPort || bind == pool || httpPort == pool {
		bind, httpPort, pool = freePort(), freePort(), freePort()
	}
	publicAddress := serverNodeEndpoint{Host: "127.0.0.1", Port: int64(bind)}
	if _, err := service.patch(ctx, record.ID, serverNodeMetadataPatch{AdvertisedFRPAddress: &publicAddress}); err != nil {
		t.Fatal(err)
	}
	settings := serverNodeSettings{BindAddress: "127.0.0.1", BindPort: int64(bind), VhostHTTPPort: int64(httpPort), PortRangeStart: int64(pool), PortRangeEnd: int64(pool)}
	if revision, err := service.saveDesired(ctx, record.ID, 0, settings); err != nil || revision != 1 {
		t.Fatalf("save running desired = (%d, %v)", revision, err)
	}
	record, err = registry.get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(ctx, record)
	view, err := service.managementView(ctx, record.ID)
	if err != nil || view.Desired.Revision != 1 || view.Observed.AppliedRevision != 1 || view.Observed.Configuration != "converged" || view.FRPS.State != "running" || !view.Selectability.Selectable {
		t.Fatalf("real FRPS application = (%+v, %v)", view, err)
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(bind)), time.Second)
	if err != nil {
		t.Fatalf("FRPS did not listen at saved bind port: %v", err)
	}
	_ = connection.Close()
	if _, err := state.database.ExecContext(ctx, `DELETE FROM nodes WHERE node_id=?`, record.ID); err != nil {
		t.Fatal(err)
	}
	preview, err = service.preview(ctx, "admin-session", address)
	if err != nil {
		t.Fatal(err)
	}
	readded, err := service.register(ctx, "admin-session", preview.ID, "Recovered", preview.Fingerprint, "readd")
	if err != nil || readded.ID != record.ID || readded.DesiredRevision != 1 || readded.DesiredSnapshot.Valid {
		t.Fatalf("explicit readd = (%+v, %v)", readded, err)
	}
	if _, err := service.patch(ctx, readded.ID, serverNodeMetadataPatch{AdvertisedFRPAddress: &publicAddress}); err != nil {
		t.Fatal(err)
	}
	if revision, err := service.saveDesired(ctx, readded.ID, 1, settings); err != nil || revision != 2 {
		t.Fatalf("readded Node revision = (%d, %v)", revision, err)
	}
	readded, err = registry.get(ctx, readded.ID)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileNode(ctx, readded)
	view, err = service.managementView(ctx, readded.ID)
	if err != nil || view.Observed.AppliedRevision != 2 || view.Observed.Configuration != "converged" || view.FRPS.State != "running" {
		t.Fatalf("readded Node did not converge: (%+v, %v)", view, err)
	}
	disabled, err := json.Marshal(map[string]any{"formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion, "nodeId": readded.ID, "revision": 3, "state": "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.wire.applySnapshot(ctx, address, coordinator.privateKey, readded.PublicKey, readded.ID, 3, disabled)
	if err != nil || result.AppliedRevision != 3 || result.Phase != "disabled" {
		t.Fatalf("cleanup disabled = (%+v, %v)", result, err)
	}
	stopNode()
	stopped = true
}
