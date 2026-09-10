package server

import (
	"context"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"testing"
)

func TestServerAgentConnectionAcceptsOnlyOneValidInitialHello(t *testing.T) {
	validHello := []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0,"futureField":true}`)
	for _, test := range []struct {
		name      string
		message   []byte
		closeCode int
	}{
		{name: "invalid JSON", message: []byte(`{"type":`), closeCode: serverAgentCloseInvalidMessage},
		{name: "wrong message type", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "non-integer protocol", message: []byte(`{"type":"hello","tunnelProtocolVersion":3.5,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "null required field", message: []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":null,"platform":"linux","architecture":"x64","lastAppliedRevision":0}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "negative revision", message: []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":-1}`), closeCode: serverAgentCloseInvalidMessage},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, _ := openServerAgentProtocolConnection(t)
			err := connection.AcceptHello(context.Background(), test.message)
			if err == nil || err.CloseCode != test.closeCode {
				t.Fatalf("AcceptHello(%s) = %v, want close code %d", test.message, err, test.closeCode)
			}
		})
	}

	connection, _ := openServerAgentProtocolConnection(t)
	if err := connection.AcceptHello(context.Background(), validHello); err != nil {
		t.Fatalf("AcceptHello(valid) error = %v", err)
	}
	if err := connection.AcceptHello(context.Background(), validHello); err == nil || err.CloseCode != serverAgentCloseInvalidMessage {
		t.Fatalf("second AcceptHello(valid) = %v, want invalid-message close", err)
	}
}

func TestServerAgentConnectionRejectsIncompatibleHello(t *testing.T) {
	validHello := []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)
	for _, test := range []struct {
		name      string
		message   []byte
		configure func(*serverAgentTestFRPSAvailability)
		closeCode int
	}{
		{name: "protocol version", message: []byte(`{"type":"hello","tunnelProtocolVersion":4,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`), closeCode: serverAgentCloseIncompatible},
		{name: "unsupported platform", message: []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"freebsd","architecture":"x64","lastAppliedRevision":0}`), closeCode: serverAgentCloseIncompatible},
		{name: "future applied revision", message: []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":1}`), closeCode: serverAgentCloseIncompatible},
		{name: "frps stopped after authorization", message: validHello, configure: func(availability *serverAgentTestFRPSAvailability) { availability.set(tunnelruntime.FRPProcessStopped) }, closeCode: serverAgentCloseFRPSUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, availability := openServerAgentProtocolConnection(t)
			if test.configure != nil {
				test.configure(availability)
			}
			err := connection.AcceptHello(context.Background(), test.message)
			if err == nil || err.CloseCode != test.closeCode {
				t.Fatalf("AcceptHello(%s) = %v, want close code %d", test.message, err, test.closeCode)
			}
		})
	}
}

func TestServerAgentConnectionBuildsWelcomeAfterHello(t *testing.T) {
	connection, _ := openServerAgentProtocolConnection(t)
	connection.gateway.welcomeSource = serverAgentTestWelcomeSource{settings: ServerAgentWelcomeSettings{
		AdvertisedFRPHost: "frp.example.test",
		AdvertisedFRPPort: 7001,
		InternalFRPToken:  "agent-only-token",
	}}
	validHello := []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0,"futureField":true}`)
	if err := connection.AcceptHello(context.Background(), validHello); err != nil {
		t.Fatalf("AcceptHello(valid) error = %v", err)
	}
	welcome, err := connection.BuildWelcome(context.Background(), "request.example.test")
	if err != nil {
		t.Fatalf("BuildWelcome() error = %v", err)
	}
	artifact, resolveErr := tunnelruntime.ResolveFRPArtifact(tunnelruntime.WireTarget{Platform: tunnelruntime.WirePlatformLinux, Architecture: tunnelruntime.WireArchitectureX64})
	if resolveErr != nil {
		t.Fatalf("ResolveFRPArtifact() error = %v", resolveErr)
	}
	if welcome.Type != "welcome" || welcome.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion || welcome.RequiredFRPVersion != tunnelruntime.FRPVersion || welcome.Artifact != artifact.Description || welcome.AdvertisedFRPHost != "frp.example.test" || welcome.AdvertisedFRPPort != 7001 || welcome.InternalFRPToken != "agent-only-token" || welcome.Snapshot.ClientKey != connection.ClientID() || welcome.Snapshot.Revision != 0 || len(welcome.Snapshot.Tunnels) != 0 {
		t.Fatalf("BuildWelcome() = %#v", welcome)
	}

	unaccepted, _ := openServerAgentProtocolConnection(t)
	if _, err := unaccepted.BuildWelcome(context.Background(), "request.example.test"); err == nil || err.CloseCode != serverAgentCloseInvalidMessage {
		t.Fatalf("BuildWelcome(before hello) error = %v, want invalid-message close", err)
	}
}

func TestServerAgentConnectionPresentsDesiredStateOnlyAfterWelcome(t *testing.T) {
	connection, _ := openServerAgentProtocolConnection(t)
	connection.gateway.welcomeSource = serverAgentTestWelcomeSource{settings: ServerAgentWelcomeSettings{
		AdvertisedFRPHost: "frp.example.test",
		AdvertisedFRPPort: 7001,
		InternalFRPToken:  "agent-only-token",
	}}
	if err := connection.AcceptHello(context.Background(), []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)); err != nil {
		t.Fatalf("AcceptHello() error = %v", err)
	}
	if _, err := connection.gateway.controlPlane.CreateTunnel(context.Background(), connection.ClientID(), TunnelMutationInput{
		Protocol:  tunnelruntime.TunnelProtocolTCP,
		LocalPort: 3000,
	}); err != nil {
		t.Fatalf("CreateTunnel(before welcome) error = %v", err)
	}

	var frames []any
	if err := connection.PresentWelcome(context.Background(), "request.example.test", func(frame any) error {
		frames = append(frames, frame)
		return nil
	}); err != nil {
		t.Fatalf("PresentWelcome() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frames after welcome = %#v, want only welcome", frames)
	}
	welcome, ok := frames[0].(tunnelruntime.AgentWelcome)
	if !ok || welcome.Snapshot.Revision != 1 || len(welcome.Snapshot.Tunnels) != 1 {
		t.Fatalf("welcome = %#v", frames[0])
	}

	if _, err := connection.gateway.controlPlane.CreateTunnel(context.Background(), connection.ClientID(), TunnelMutationInput{
		Protocol:  tunnelruntime.TunnelProtocolTCP,
		LocalPort: 3001,
	}); err != nil {
		t.Fatalf("CreateTunnel(after welcome) error = %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames after desired-state change = %#v", frames)
	}
	desired, ok := frames[1].(tunnelruntime.DesiredState)
	if !ok || desired.Type != "desired_state" || desired.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion || desired.Snapshot.Revision != 2 || len(desired.Snapshot.Tunnels) != 2 {
		t.Fatalf("desired state = %#v", frames[1])
	}
}

func TestServerAgentConnectionProjectsApplyResultRuntimeErrors(t *testing.T) {
	ctx := context.Background()
	connection, _ := openServerAgentProtocolConnection(t)
	if err := connection.AcceptHello(ctx, []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)); err != nil {
		t.Fatalf("AcceptHello() error = %v", err)
	}
	if _, err := connection.gateway.controlPlane.CreateTunnel(ctx, connection.ClientID(), TunnelMutationInput{
		Protocol:  tunnelruntime.TunnelProtocolTCP,
		LocalPort: 3000,
	}); err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	if err := connection.AcceptApplyResult(ctx, []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":1,"success":true,"futureField":true}`)); err != nil {
		t.Fatalf("AcceptApplyResult(success) error = %v", err)
	}
	client, err := connection.gateway.controlPlane.GetClient(ctx, connection.ClientID())
	if err != nil || client.DesiredRevision != 1 || client.LastAppliedRevision != 1 {
		t.Fatalf("client after successful apply result = (%#v, %v)", client, err)
	}
	if err := connection.AcceptApplyResult(ctx, []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":1,"success":false,"error":{"code":"ACTIVATION_FAILED","message":"candidate exited","revision":1}}`)); err != nil {
		t.Fatalf("AcceptApplyResult(failure) error = %v", err)
	}
	if got := connection.gateway.State(connection.ClientID()); got.ProcessState != tunnelruntime.FRPProcessStopped || got.LastError == nil || got.LastError.Code != "ACTIVATION_FAILED" || got.LastError.Message != "candidate exited" || got.LastError.Revision == nil || *got.LastError.Revision != 1 {
		t.Fatalf("runtime after failed apply result = %#v", got)
	}
	if err := connection.AcceptApplyResult(ctx, []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":0,"success":true}`)); err != nil {
		t.Fatalf("AcceptApplyResult(stale success) error = %v", err)
	}
	client, err = connection.gateway.controlPlane.GetClient(ctx, connection.ClientID())
	if err != nil || client.LastAppliedRevision != 1 {
		t.Fatalf("client after failed or stale apply result = (%#v, %v)", client, err)
	}
	if got := connection.gateway.State(connection.ClientID()); got.LastError != nil {
		t.Fatalf("runtime after successful apply result = %#v", got)
	}
}

func TestServerAgentConnectionProjectsDefaultApplyFailure(t *testing.T) {
	ctx := context.Background()
	connection, _ := openServerAgentProtocolConnection(t)
	if err := connection.AcceptHello(ctx, []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)); err != nil {
		t.Fatalf("AcceptHello() error = %v", err)
	}
	if _, err := connection.gateway.controlPlane.CreateTunnel(ctx, connection.ClientID(), TunnelMutationInput{
		Protocol:  tunnelruntime.TunnelProtocolTCP,
		LocalPort: 3000,
	}); err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	if err := connection.AcceptApplyResult(ctx, []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":1,"success":false}`)); err != nil {
		t.Fatalf("AcceptApplyResult(default failure) error = %v", err)
	}
	if got := connection.gateway.State(connection.ClientID()); got.LastError == nil || got.LastError.Code != "APPLY_FAILED" || got.LastError.Message != "Client could not apply Desired Revision" || got.LastError.Revision == nil || *got.LastError.Revision != 1 {
		t.Fatalf("runtime after default apply failure = %#v", got)
	}
	client, err := connection.gateway.controlPlane.GetClient(ctx, connection.ClientID())
	if err != nil || client.LastAppliedRevision != 0 {
		t.Fatalf("client after failed apply result = (%#v, %v)", client, err)
	}
	if err := connection.AcceptProcessState(ctx, []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"running"}`)); err != nil {
		t.Fatalf("AcceptProcessState() error = %v", err)
	}
	if got := connection.gateway.State(connection.ClientID()); got.ProcessState != tunnelruntime.FRPProcessRunning || got.LastError == nil || got.LastError.Revision == nil || *got.LastError.Revision != 1 {
		t.Fatalf("runtime after process-state report = %#v", got)
	}
}

func TestServerAgentConnectionRejectsInvalidApplyResults(t *testing.T) {
	validHello := []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)
	connection, _ := openServerAgentProtocolConnection(t)
	if err := connection.AcceptApplyResult(context.Background(), []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":0,"success":true}`)); err == nil || err.CloseCode != serverAgentCloseInvalidMessage {
		t.Fatalf("AcceptApplyResult(before hello) = %v, want invalid-message close", err)
	}

	for _, test := range []struct {
		name      string
		message   []byte
		closeCode int
	}{
		{name: "invalid JSON", message: []byte(`{"type":`), closeCode: serverAgentCloseInvalidMessage},
		{name: "unsupported protocol", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":4,"revision":0,"success":true}`), closeCode: serverAgentCloseIncompatible},
		{name: "non-integer protocol", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3.5,"revision":0,"success":true}`), closeCode: serverAgentCloseIncompatible},
		{name: "unexpected process state", message: []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"running"}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "negative revision", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":-1,"success":true}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "unsafe revision", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":9007199254740992,"success":true}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "missing success", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":0}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "invalid error", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":0,"success":false,"error":{"code":true}}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "negative error revision", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":0,"success":false,"error":{"code":"FAIL","message":"failed","revision":-1}}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "revision ahead of desired state", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":1,"success":true}`), closeCode: serverAgentCloseInvalidMessage},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, _ := openServerAgentProtocolConnection(t)
			if err := connection.AcceptHello(context.Background(), validHello); err != nil {
				t.Fatalf("AcceptHello() error = %v", err)
			}
			err := connection.AcceptApplyResult(context.Background(), test.message)
			if err == nil || err.CloseCode != test.closeCode {
				t.Fatalf("AcceptApplyResult(%s) = %v, want close code %d", test.message, err, test.closeCode)
			}
		})
	}
}

func TestServerAgentConnectionProjectsProcessStateWithoutDurableMutation(t *testing.T) {
	ctx := context.Background()
	connection, _ := openServerAgentProtocolConnection(t)
	if err := connection.AcceptHello(ctx, []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)); err != nil {
		t.Fatalf("AcceptHello() error = %v", err)
	}
	before, err := connection.gateway.controlPlane.GetClient(ctx, connection.ClientID())
	if err != nil {
		t.Fatalf("GetClient(before process state) error = %v", err)
	}
	if err := connection.AcceptProcessState(ctx, []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"running"}`)); err != nil {
		t.Fatalf("AcceptProcessState(running) error = %v", err)
	}
	if got := connection.gateway.State(connection.ClientID()); got.ConnectionState != ServerClientConnected || got.ProcessState != tunnelruntime.FRPProcessRunning || got.LastError != nil {
		t.Fatalf("runtime after running process state = %#v", got)
	}
	if err := connection.AcceptProcessState(ctx, []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"configuration_failed","error":{"code":"ACTIVATION_FAILED","message":"candidate exited","revision":0}}`)); err != nil {
		t.Fatalf("AcceptProcessState(failure) error = %v", err)
	}
	if got := connection.gateway.State(connection.ClientID()); got.ConnectionState != ServerClientConnected || got.ProcessState != tunnelruntime.FRPProcessConfigurationFailed || got.LastError == nil || got.LastError.Code != "ACTIVATION_FAILED" || got.LastError.Message != "candidate exited" || got.LastError.Revision == nil || *got.LastError.Revision != 0 {
		t.Fatalf("runtime after failed process state = %#v", got)
	}
	after, err := connection.gateway.controlPlane.GetClient(ctx, connection.ClientID())
	if err != nil {
		t.Fatalf("GetClient(after process state) error = %v", err)
	}
	if after.Remark != before.Remark || after.Token != before.Token || after.DesiredRevision != before.DesiredRevision || after.LastAppliedRevision != before.LastAppliedRevision || after.RevocationPending != before.RevocationPending {
		t.Fatalf("process state changed durable client state: before=%#v after=%#v", before, after)
	}
	connection.Close()
	if got := connection.gateway.State(connection.ClientID()); got.ConnectionState != ServerClientDisconnected || got.ProcessState != tunnelruntime.FRPProcessConfigurationFailed || got.LastError == nil || got.LastError.Revision == nil || *got.LastError.Revision != 0 {
		t.Fatalf("runtime after connection close = %#v", got)
	}
}

func TestServerAgentConnectionRejectsInvalidProcessStates(t *testing.T) {
	validHello := []byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"linux","architecture":"x64","lastAppliedRevision":0}`)
	connection, _ := openServerAgentProtocolConnection(t)
	if err := connection.AcceptProcessState(context.Background(), []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"running"}`)); err == nil || err.CloseCode != serverAgentCloseInvalidMessage {
		t.Fatalf("AcceptProcessState(before hello) = %v, want invalid-message close", err)
	}

	for _, test := range []struct {
		name      string
		message   []byte
		closeCode int
	}{
		{name: "invalid JSON", message: []byte(`{"type":`), closeCode: serverAgentCloseInvalidMessage},
		{name: "unsupported protocol", message: []byte(`{"type":"process_state","tunnelProtocolVersion":4,"state":"running"}`), closeCode: serverAgentCloseIncompatible},
		{name: "unexpected apply result", message: []byte(`{"type":"apply_result","tunnelProtocolVersion":3,"revision":0,"success":true}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "invalid state", message: []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"starting"}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "invalid error", message: []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"running","error":{"code":true}}`), closeCode: serverAgentCloseInvalidMessage},
		{name: "negative error revision", message: []byte(`{"type":"process_state","tunnelProtocolVersion":3,"state":"running","error":{"code":"FAIL","message":"failed","revision":-1}}`), closeCode: serverAgentCloseInvalidMessage},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection, _ := openServerAgentProtocolConnection(t)
			if err := connection.AcceptHello(context.Background(), validHello); err != nil {
				t.Fatalf("AcceptHello() error = %v", err)
			}
			err := connection.AcceptProcessState(context.Background(), test.message)
			if err == nil || err.CloseCode != test.closeCode {
				t.Fatalf("AcceptProcessState(%s) = %v, want close code %d", test.message, err, test.closeCode)
			}
		})
	}
}

func openServerAgentProtocolConnection(t *testing.T) (*ServerAgentConnection, *serverAgentTestFRPSAvailability) {
	t.Helper()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	availability := &serverAgentTestFRPSAvailability{state: tunnelruntime.FRPProcessRunning}
	gateway, err := NewServerAgentGateway(ServerAgentGatewayOptions{ControlPlane: plane, FRPS: availability})
	if err != nil {
		t.Fatalf("NewServerAgentGateway() error = %v", err)
	}
	client, err := plane.CreateClient(context.Background(), "environment-admin", "protocol client")
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
	return connection, availability
}

type serverAgentTestWelcomeSource struct {
	settings ServerAgentWelcomeSettings
}

func (source serverAgentTestWelcomeSource) AgentWelcomeSettings(string) ServerAgentWelcomeSettings {
	return source.settings
}
