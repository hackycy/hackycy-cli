package server

import (
	"context"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"sync"
	"testing"
)

func TestServerAgentGatewayRequiresControlPlaneAndFRPSState(t *testing.T) {
	for _, options := range []ServerAgentGatewayOptions{
		{},
		{ControlPlane: openServerControlPlane(t, openServerDomainState(t))},
		{FRPS: &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning}},
	} {
		if _, err := NewServerAgentGateway(options); !errors.Is(err, ErrServerAgentGatewayConfiguration) {
			t.Fatalf("NewServerAgentGateway(%#v) error = %v", options, err)
		}
	}
}

func TestServerAgentGatewayAuthorizesBearerTokensAndReleasesPendingSlots(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	availability := &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessStopped}
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{ControlPlane: plane, FRPS: availability})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(context.Background(), "environment-admin", "agent gateway")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	_, err = gateway.Authorize(context.Background(), "Bearer "+client.Token)
	assertServerDomainCode(t, err, "FRPS_UNAVAILABLE")
	availability.set(tunnelruntime.FRPProcessRunning)
	for _, authorization := range []string{"", "Basic " + client.Token, "Bearer", "Bearer    ", "Bearer invalid-token"} {
		_, err := gateway.Authorize(context.Background(), authorization)
		assertServerDomainCode(t, err, "AUTHENTICATION_FAILED")
	}

	reservation, err := gateway.Authorize(context.Background(), "bEaReR "+client.Token+"  ")
	if err != nil {
		t.Fatalf("Authorize(valid token) error = %v", err)
	}
	if reservation.ClientID() != client.ID {
		t.Fatalf("reservation ClientID() = %q, want %q", reservation.ClientID(), client.ID)
	}
	_, err = gateway.Authorize(context.Background(), "Bearer "+client.Token)
	assertServerDomainCode(t, err, "CLIENT_CONNECTED")
	reservation.Release()
	reservation.Release()

	released, err := gateway.Authorize(context.Background(), "Bearer "+client.Token)
	if err != nil {
		t.Fatalf("Authorize(after release) error = %v", err)
	}
	released.Release()
}

func TestServerAgentGatewayReservesAtMostOnePendingSlotPerClient(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(context.Background(), "environment-admin", "concurrent agent")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	const attempts = 16
	start := make(chan struct{})
	var wait sync.WaitGroup
	var mu sync.Mutex
	var reservation *ServerAgentReservation
	var failures []error
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			candidate, authorizeErr := gateway.Authorize(context.Background(), "Bearer "+client.Token)
			mu.Lock()
			defer mu.Unlock()
			if authorizeErr != nil {
				failures = append(failures, authorizeErr)
				return
			}
			if reservation != nil {
				t.Error("Authorize() created more than one pending reservation")
				candidate.Release()
				return
			}
			reservation = candidate
		}()
	}
	close(start)
	wait.Wait()
	if reservation == nil {
		t.Fatal("Authorize() created no pending reservation")
	}
	if len(failures) != attempts-1 {
		t.Fatalf("authorization failures = %d, want %d", len(failures), attempts-1)
	}
	for _, failure := range failures {
		assertServerDomainCode(t, failure, "CLIENT_CONNECTED")
	}
	reservation.Release()
}

func TestServerAgentReservationTransfersItsSlotToAnActiveConnection(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(context.Background(), "environment-admin", "active agent")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	reservation, err := gateway.Authorize(context.Background(), "Bearer "+client.Token)
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	connection := reservation.Activate()
	if connection == nil || connection.ClientID() != client.ID {
		t.Fatalf("Activate() = %#v, want active connection for %q", connection, client.ID)
	}
	reservation.Release()
	if _, err := gateway.Authorize(context.Background(), "Bearer "+client.Token); err == nil {
		t.Fatal("Authorize() succeeded while the active connection owned the slot")
	} else {
		assertServerDomainCode(t, err, "CLIENT_CONNECTED")
	}

	connection.Close()
	connection.Close()
	released, err := gateway.Authorize(context.Background(), "Bearer "+client.Token)
	if err != nil {
		t.Fatalf("Authorize(after active connection close) error = %v", err)
	}
	released.Release()
}

func TestServerAgentGatewayProjectsOnlyActiveConnectionsAsConnectedRuntime(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(context.Background(), "environment-admin", "runtime agent")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	wantDisconnected := ServerClientRuntimeState{ConnectionState: ServerClientDisconnected, ProcessState: tunnelruntime.FRPProcessStopped}
	if got := gateway.State(client.ID); got != wantDisconnected {
		t.Fatalf("State(before authorization) = %#v, want %#v", got, wantDisconnected)
	}

	reservation, err := gateway.Authorize(context.Background(), "Bearer "+client.Token)
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if got := gateway.State(client.ID); got != wantDisconnected {
		t.Fatalf("State(pending authorization) = %#v, want %#v", got, wantDisconnected)
	}
	connection := reservation.Activate()
	if connection == nil {
		t.Fatal("Activate() = nil")
	}
	wantConnected := ServerClientRuntimeState{ConnectionState: ServerClientConnected, ProcessState: tunnelruntime.FRPProcessStopped}
	if got := gateway.State(client.ID); got != wantConnected {
		t.Fatalf("State(active connection) = %#v, want %#v", got, wantConnected)
	}

	connection.Close()
	if got := gateway.State(client.ID); got != wantDisconnected {
		t.Fatalf("State(closed connection) = %#v, want %#v", got, wantDisconnected)
	}
}

func TestServerAgentGatewayRestartsOnlyPresentedActiveConnections(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(context.Background(), "environment-admin", "restart agent")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	before, err := plane.GetClient(context.Background(), client.ID)
	if err != nil {
		t.Fatalf("GetClient(before restart) error = %v", err)
	}
	if gateway.RestartFRPC(client.ID) {
		t.Fatal("RestartFRPC() succeeded without an active connection")
	}

	reservation, err := gateway.Authorize(context.Background(), "Bearer "+client.Token)
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	connection := reservation.Activate()
	if connection == nil {
		t.Fatal("Activate() = nil")
	}
	t.Cleanup(connection.Close)
	if gateway.RestartFRPC(client.ID) {
		t.Fatal("RestartFRPC() succeeded before welcome presentation")
	}

	var frames []any
	connection.presentationActive = true
	connection.writeFrame = func(frame any) error {
		frames = append(frames, frame)
		return nil
	}
	if !gateway.RestartFRPC(client.ID) {
		t.Fatal("RestartFRPC() = false for a presented active connection")
	}
	if len(frames) != 1 {
		t.Fatalf("restart frames = %#v", frames)
	}
	restart, ok := frames[0].(tunnelruntime.RestartFRPC)
	if !ok || restart.Type != "restart_frpc" || restart.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion {
		t.Fatalf("restart frame = %#v", frames[0])
	}
	after, err := plane.GetClient(context.Background(), client.ID)
	if err != nil {
		t.Fatalf("GetClient(after restart) error = %v", err)
	}
	if after.Remark != before.Remark || after.Token != before.Token || after.DesiredRevision != before.DesiredRevision || after.LastAppliedRevision != before.LastAppliedRevision || after.RevocationPending != before.RevocationPending {
		t.Fatalf("restart changed durable client state: before=%#v after=%#v", before, after)
	}
}

func TestServerAgentGatewayRevokesActiveConnectionsAfterTokenMutation(t *testing.T) {
	for _, test := range []struct {
		name      string
		revoke    func(context.Context, *ServerControlPlane, string) error
		reason    string
		wantState ServerClientConnectionState
	}{
		{name: "rotation", revoke: func(ctx context.Context, plane *ServerControlPlane, clientID string) error {
			_, err := plane.RotateClientToken(ctx, clientID)
			return err
		}, reason: "rotated", wantState: ServerClientRevocationPending},
		{name: "deletion", revoke: func(ctx context.Context, plane *ServerControlPlane, clientID string) error {
			return plane.DeleteClient(ctx, clientID)
		}, reason: "deleted", wantState: ServerClientDisconnected},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := openServerDomainState(t)
			plane := openServerControlPlane(t, state)
			gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
				ControlPlane: plane,
				FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
			})
			if err != nil {
				t.Fatalf("NewServerAgentGateway() error = %v", err)
			}
			client, err := plane.CreateClient(context.Background(), "environment-admin", "revoked agent")
			if err != nil {
				t.Fatalf("CreateClient() error = %v", err)
			}
			reservation, err := gateway.Authorize(context.Background(), "Bearer "+client.Token)
			if err != nil {
				t.Fatalf("Authorize() error = %v", err)
			}
			connection := reservation.Activate()
			if connection == nil {
				t.Fatal("Activate() = nil")
			}
			t.Cleanup(connection.Close)
			var frames []any
			connection.presentationActive = true
			connection.writeFrame = func(frame any) error {
				frames = append(frames, frame)
				return nil
			}
			var closeError *ServerAgentProtocolError
			connection.AttachCloser(func(protocolError *ServerAgentProtocolError) {
				closeError = protocolError
			})

			if err := test.revoke(context.Background(), plane, client.ID); err != nil {
				t.Fatalf("token mutation error = %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("revoke frames = %#v", frames)
			}
			revoke, ok := frames[0].(tunnelruntime.Revoke)
			if !ok || revoke.Type != "revoke" || revoke.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion || revoke.Reason != test.reason {
				t.Fatalf("revoke frame = %#v", frames[0])
			}
			if closeError == nil || closeError.CloseCode != serverAgentCloseRevoked {
				t.Fatalf("revoke close = %#v, want %d", closeError, serverAgentCloseRevoked)
			}
			if got := gateway.State(client.ID); got.ConnectionState != test.wantState || got.ProcessState != tunnelruntime.FRPProcessStopped {
				t.Fatalf("runtime after revoke = %#v", got)
			}
		})
	}
}

func TestServerAgentConnectionAcknowledgesReplacementToken(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane: plane,
		FRPS:         &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning},
	})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(context.Background(), "environment-admin", "replacement agent")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	rotated, err := plane.RotateClientToken(context.Background(), client.ID)
	if err != nil {
		t.Fatalf("RotateClientToken() error = %v", err)
	}
	reservation, err := gateway.Authorize(context.Background(), "Bearer "+rotated.Token)
	if err != nil {
		t.Fatalf("Authorize(replacement token) error = %v", err)
	}
	connection := reservation.Activate()
	if connection == nil {
		t.Fatal("Activate() = nil")
	}
	t.Cleanup(connection.Close)
	if err := connection.AcknowledgeReplacementToken(context.Background()); err != nil {
		t.Fatalf("AcknowledgeReplacementToken() error = %v", err)
	}
	current, err := plane.GetClient(context.Background(), client.ID)
	if err != nil || current.RevocationPending {
		t.Fatalf("client after replacement acknowledgement = (%#v, %v)", current, err)
	}
}

type serverAgentTestFRPSAvailability struct {
	mu    sync.RWMutex
	state tunnelruntime.FRPProcessState
}

func (availability *serverAgentTestFRPSAvailability) FRPSState() tunnelruntime.FRPSupervisorState {
	availability.mu.RLock()
	defer availability.mu.RUnlock()
	return tunnelruntime.FRPSupervisorState{State: availability.state}
}

func (availability *serverAgentTestFRPSAvailability) set(state tunnelruntime.FRPProcessState) {
	availability.mu.Lock()
	availability.state = state
	availability.mu.Unlock()
}
