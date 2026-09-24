package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	clientcommand "github.com/hackycy/hackycy-cli/pkg/cmd/tunnel/connect"
)

func TestOfficialClientSwitchesAcrossNodesWithRealFRP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatal(err)
	}
	ports := reserveGoToGoFRPPorts(t)
	defer ports.Close()
	backendPort := startGoToGoTCPBackend(t)
	runtime, err := NewServerRuntime(ctx, ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address: "127.0.0.1", ControlPort: 0, FRPPort: ports.frp, HTTPPort: ports.http,
			PortRange:        ServerHTTPPortRange{Start: ports.proxy, End: ports.proxy},
			AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "127.0.0.1", Port: ports.frp},
			DataDir:          t.TempDir(), AdminUser: "admin",
		},
		AdminPassword: "integration-password", FRPToken: "local-integration-token", SessionIdleLifetime: time.Hour,
		frpArtifact: &artifact,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := runtime.controlPlane.CreateClient(ctx, environmentAdministratorID, "cross-node")
	if err != nil {
		_ = runtime.Close()
		t.Fatal(err)
	}
	proxyPort := int64(ports.proxy)
	loopback := "127.0.0.1"
	tunnel, err := runtime.controlPlane.CreateTunnel(ctx, client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &proxyPort, LocalHost: &loopback, LocalPort: int64(backendPort)})
	if err != nil {
		_ = runtime.Close()
		t.Fatal(err)
	}
	ports.Close()
	server, err := runtime.Start()
	if err != nil {
		_ = runtime.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	waitForGoToGoForwarding(t, "Local FRPS startup", 20*time.Second, func() error {
		if runtime.frps.FRPSState().State != tunnelruntime.FRPProcessRunning {
			return errors.New("Local FRPS is not running")
		}
		return nil
	})
	remoteA := startCrossNodeRemote(t, ctx, runtime, "Remote A", proxyPort)
	remoteB := startCrossNodeRemote(t, ctx, runtime, "Remote B", proxyPort)
	controlURL, err := url.Parse(server.URL())
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := t.TempDir()
	clientContext, cancelClient := context.WithCancel(ctx)
	clientDone := make(chan error, 1)
	startClient := func(runContext context.Context, done chan error) {
		go func() {
			done <- clientcommand.RunClient(runContext, clientcommand.ClientConfig{Server: controlURL, Token: client.Token}, clientcommand.ClientRunOptions{
				InstanceIdentity: goToGoClientIdentity{}, StateRoot: clientRoot, YCYVersion: "cross-node-integration",
			})
		}()
	}
	startClient(clientContext, clientDone)
	stopClient := func() {
		if clientDone == nil {
			return
		}
		cancelClient()
		select {
		case err := <-clientDone:
			if err != nil {
				t.Errorf("RunClient() cleanup error = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("RunClient() did not stop")
		}
		clientDone = nil
	}
	t.Cleanup(stopClient)
	waitCrossNodeClient(t, ctx, runtime, client.ID, clientRoot, "local", proxyPort)
	previousGeneration := runtime.gateway.FRPCObservation(client.ID).ProcessGeneration
	if _, err := runtime.controlPlane.AssignClientNode(ctx, client.ID, remoteA.id, true, true); err != nil {
		t.Fatal(err)
	}
	waitCrossNodeClient(t, ctx, runtime, client.ID, clientRoot, remoteA.id, proxyPort)
	previousGeneration = assertCrossNodeProcessChanged(t, runtime, client.ID, previousGeneration)
	if _, err := runtime.controlPlane.AssignClientNode(ctx, client.ID, remoteB.id, true, true); err != nil {
		t.Fatal(err)
	}
	waitCrossNodeClient(t, ctx, runtime, client.ID, clientRoot, remoteB.id, proxyPort)
	previousGeneration = assertCrossNodeProcessChanged(t, runtime, client.ID, previousGeneration)
	if _, err := runtime.controlPlane.AssignClientNode(ctx, client.ID, "local", true, true); err != nil {
		t.Fatal(err)
	}
	waitCrossNodeClient(t, ctx, runtime, client.ID, clientRoot, "local", proxyPort)
	previousGeneration = assertCrossNodeProcessChanged(t, runtime, client.ID, previousGeneration)
	if _, err := runtime.nodeService.patch(ctx, remoteA.id, serverNodeMetadataPatch{AdvertisedFRPAddress: &serverNodeEndpoint{Host: "127.0.0.2", Port: remoteA.frpPort}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.controlPlane.AssignClientNode(ctx, client.ID, remoteA.id, true, true); err != nil {
		t.Fatal(err)
	}
	waitCrossNodeApplied(t, ctx, runtime, client.ID, clientRoot, remoteA.id)
	waitForGoToGoForwarding(t, "old Local proxy release after unreachable target", 20*time.Second, func() error {
		return verifyGoToGoRemotePortReleased(int(proxyPort))
	})
	waitForGoToGoForwarding(t, "unreachable target observation", 10*time.Second, func() error {
		status := runtime.gateway.FRPCObservation(client.ID)
		if status.NodeID != remoteA.id || status.Connection == "connected" {
			return fmt.Errorf("unreachable target observation = node %s, connection %s", status.NodeID, status.Connection)
		}
		return nil
	})
	time.Sleep(2 * time.Second)
	if current, err := runtime.controlPlane.GetClient(ctx, client.ID); err != nil || current.NodeID != remoteA.id {
		t.Fatalf("unreachable target reverted assignment = (%+v, %v)", current, err)
	}
	if err := verifyGoToGoRemotePortReleased(int(proxyPort)); err != nil {
		t.Fatalf("old proxy restarted after failed target login: %v", err)
	}
	if _, err := runtime.nodeService.patch(ctx, remoteA.id, serverNodeMetadataPatch{AdvertisedFRPAddress: &serverNodeEndpoint{Host: "127.0.0.1", Port: remoteA.frpPort}}); err != nil {
		t.Fatal(err)
	}
	waitCrossNodeClient(t, ctx, runtime, client.ID, clientRoot, remoteA.id, proxyPort)
	assertCrossNodeProcessChanged(t, runtime, client.ID, previousGeneration)
	stopClient()
	if _, err := runtime.controlPlane.AssignClientNode(ctx, client.ID, remoteB.id, false, false); err != nil {
		t.Fatal(err)
	}
	pending, err := runtime.controlPlane.GetClient(ctx, client.ID)
	if err != nil || pending.NodeID != remoteA.id || pending.PendingNodeID == nil || *pending.PendingNodeID != remoteB.id {
		t.Fatalf("offline pending assignment = (%+v, %v)", pending, err)
	}
	clientContext, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	clientDone = make(chan error, 1)
	startClient(clientContext, clientDone)
	waitCrossNodeClient(t, ctx, runtime, client.ID, clientRoot, remoteB.id, proxyPort)
	committed, err := runtime.controlPlane.GetClient(ctx, client.ID)
	if err != nil || committed.PendingNodeID != nil || committed.NodeID != remoteB.id {
		t.Fatalf("pending welcome did not commit Remote B = (%+v, %v)", committed, err)
	}
	if err := runtime.controlPlane.DeleteTunnel(ctx, tunnel.ID); err != nil {
		t.Fatal(err)
	}
	waitForGoToGoForwarding(t, "empty Tunnel set application", 15*time.Second, func() error {
		current, err := runtime.controlPlane.GetClient(ctx, client.ID)
		if err != nil || current.LastAppliedRevision != current.DesiredRevision {
			return fmt.Errorf("empty runtime revision = (%+v, %v)", current, err)
		}
		status := runtime.gateway.FRPCObservation(client.ID)
		if status.NodeID != remoteB.id || status.Connection != "not_required" || status.Process != tunnelruntime.FRPProcessStopped {
			return fmt.Errorf("empty runtime observation = node %s, connection %s, process %s", status.NodeID, status.Connection, status.Process)
		}
		return nil
	})
	if _, err := runtime.controlPlane.AssignClientNode(ctx, client.ID, "local", true, true); err != nil {
		t.Fatal(err)
	}
	waitCrossNodeApplied(t, ctx, runtime, client.ID, clientRoot, "local")
	waitForGoToGoForwarding(t, "empty Local assignment observation", 10*time.Second, func() error {
		status := runtime.gateway.FRPCObservation(client.ID)
		if status.NodeID != "local" || status.Connection != "not_required" || status.Process != tunnelruntime.FRPProcessStopped {
			return fmt.Errorf("empty Local observation = node %s, connection %s, process %s", status.NodeID, status.Connection, status.Process)
		}
		return verifyGoToGoRemotePortReleased(int(proxyPort))
	})
	if state, found := clientcommand.ReadClientAppliedState(filepath.Join(clientRoot, goToGoClientInstanceID())); !found || len(state.Runtime.Tunnels) != 0 {
		t.Fatalf("empty Local accepted state = (%+v, %t)", state, found)
	}
}

func assertCrossNodeProcessChanged(t *testing.T, runtime *ServerRuntime, clientID, previous string) string {
	t.Helper()
	current := runtime.gateway.FRPCObservation(clientID).ProcessGeneration
	if previous == "" || current == "" || current == previous {
		t.Fatalf("FRPC process generation did not change across Nodes: previous %q, current %q", previous, current)
	}
	return current
}

type crossNodeRemote struct {
	id      string
	frpPort int64
}

func startCrossNodeRemote(t *testing.T, ctx context.Context, runtime *ServerRuntime, name string, proxyPort int64) crossNodeRemote {
	t.Helper()
	ports := reserveGoToGoFRPPorts(t)
	defer ports.Close()
	address, stopNode := startNodeForMetadataTest(t, filepath.Join(t.TempDir(), "node"))
	preview, err := runtime.nodeService.preview(ctx, "integration-session", address)
	if err != nil {
		stopNode()
		t.Fatal(err)
	}
	record, err := runtime.nodeService.register(ctx, "integration-session", preview.ID, name, preview.Fingerprint, "claim")
	if err != nil {
		stopNode()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		disabled, err := json.Marshal(map[string]any{"formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion, "nodeId": record.ID, "revision": 100, "state": "disabled"})
		if err == nil {
			_, _ = runtime.nodeCoordinator.wire.applySnapshot(context.Background(), address, runtime.nodeCoordinator.privateKey, record.PublicKey, record.ID, 100, disabled)
		}
		stopNode()
	})
	if _, err := runtime.nodeService.patch(ctx, record.ID, serverNodeMetadataPatch{AdvertisedFRPAddress: &serverNodeEndpoint{Host: "127.0.0.1", Port: int64(ports.frp)}}); err != nil {
		t.Fatal(err)
	}
	settings := serverNodeSettings{BindAddress: "127.0.0.1", BindPort: int64(ports.frp), VhostHTTPPort: int64(ports.http), PortRangeStart: proxyPort, PortRangeEnd: proxyPort}
	if _, err := runtime.nodeService.saveDesired(ctx, record.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	ports.Close()
	record, err = runtime.nodes.get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime.nodeCoordinator.reconcileNode(ctx, record)
	waitForGoToGoForwarding(t, name+" FRPS convergence", 20*time.Second, func() error {
		view, err := runtime.nodeService.managementView(ctx, record.ID)
		if err != nil {
			return err
		}
		if view.Observed.Configuration != "converged" || view.FRPS.State != "running" || !view.Selectability.Selectable {
			return fmt.Errorf("Node view = desired %d, applied %d, FRPS %s", view.Desired.Revision, view.Observed.AppliedRevision, view.FRPS.State)
		}
		return nil
	})
	return crossNodeRemote{id: record.ID, frpPort: settings.BindPort}
}

func waitCrossNodeClient(t *testing.T, ctx context.Context, runtime *ServerRuntime, clientID, root, nodeID string, proxyPort int64) {
	t.Helper()
	waitCrossNodeApplied(t, ctx, runtime, clientID, root, nodeID)
	waitForGoToGoForwarding(t, "FRPC registration on "+nodeID, 20*time.Second, func() error {
		status := runtime.gateway.FRPCObservation(clientID)
		if status.NodeID != nodeID || status.Connection != "connected" || len(status.Proxies) != 1 || status.Proxies[0].State != "registered" {
			return fmt.Errorf("FRPC observation = node %s, connection %s, proxies %v", status.NodeID, status.Connection, status.Proxies)
		}
		return nil
	})
	waitForGoToGoForwarding(t, "TCP forwarding on "+nodeID, 15*time.Second, func() error {
		return verifyGoToGoTCPForwarding(ctx, int(proxyPort))
	})
}

func waitCrossNodeApplied(t *testing.T, ctx context.Context, runtime *ServerRuntime, clientID, root, nodeID string) {
	t.Helper()
	waitForGoToGoForwarding(t, "Client application on "+nodeID, 30*time.Second, func() error {
		client, err := runtime.controlPlane.GetClient(ctx, clientID)
		if err != nil {
			return err
		}
		if client.NodeID != nodeID || client.LastAppliedNodeID == nil || *client.LastAppliedNodeID != nodeID || client.LastAppliedRevision != client.DesiredRevision {
			accepted, acceptedFound := clientcommand.ReadClientAcceptedState(filepath.Join(root, goToGoClientInstanceID()))
			applied, appliedFound := clientcommand.ReadClientAppliedState(filepath.Join(root, goToGoClientInstanceID()))
			acceptedReference, appliedReference := "none", "none"
			if acceptedFound {
				acceptedReference = fmt.Sprintf("%s/%d", accepted.Runtime.NodeID, accepted.Revision)
			}
			if appliedFound {
				appliedReference = fmt.Sprintf("%s/%d", applied.Runtime.NodeID, applied.Revision)
			}
			return fmt.Errorf("Client assignment = current %s, applied %v, revisions %d/%d, accepted %s, local applied %s, gateway %+v", client.NodeID, client.LastAppliedNodeID, client.LastAppliedRevision, client.DesiredRevision, acceptedReference, appliedReference, runtime.gateway.State(clientID))
		}
		state, found := clientcommand.ReadClientAppliedState(filepath.Join(root, goToGoClientInstanceID()))
		if !found || state.Runtime.NodeID != nodeID || state.Revision != client.DesiredRevision {
			return fmt.Errorf("local applied state = (%v, %t)", state, found)
		}
		return nil
	})
}
