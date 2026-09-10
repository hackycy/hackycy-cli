package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hackycy/hackycy-cli/internal/logging"
)

const (
	clientDefaultYCYVersion              = "0.0.0-dev"
	frpcConfigurationVerificationTimeout = 10 * time.Second
)

var (
	ErrFRPCConfigurationVerificationTimeout = errors.New("FRPC configuration verification timed out")
	errClientControlRevoked                 = errors.New("Tunnel client was revoked")
	errClientControlFatal                   = errors.New("Tunnel client could not process a control message")
)

// ClientRunOptions supplies the private dependencies for an unregistered
// foreground client. The CLI binding and terminal selection remain later work.
type ClientRunOptions struct {
	InstanceIdentity ClientInstanceIdentity
	StateRoot        string
	Logger           logging.Logger
	LogLevel         string
	YCYVersion       string
	HTTPClient       *http.Client
	WebSocketDialer  *websocket.Dialer
	OnAuthenticated  func() error
	Backoff          []time.Duration

	newRuntime func(context.Context, logging.Logger) (ClientFRPRuntime, error)
}

type clientFRPRuntimeStateObserver interface {
	State() tunnelruntime.FRPSupervisorState
	Observe(func(tunnelruntime.FRPSupervisorState)) func()
}

type clientFRPRuntimeEnsurer func(context.Context, string, tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error)

type managedClientFRPRuntimeOptions struct {
	Logger              logging.Logger
	frpArtifact         *tunnelruntime.FRPArtifact
	frpRuntimeDirectory string
	ensureFRPRuntime    clientFRPRuntimeEnsurer
}

// managedClientFRPRuntime binds the reconciler's narrow runtime surface to
// one manifest-pinned, owner-local frpc supervisor.
type managedClientFRPRuntime struct {
	binaryPath string
	supervisor *tunnelruntime.FRPSupervisor
}

func newManagedClientFRPRuntime(ctx context.Context, options managedClientFRPRuntimeOptions) (*managedClientFRPRuntime, error) {
	artifact := tunnelruntime.FRPArtifact{}
	if options.frpArtifact != nil {
		artifact = *options.frpArtifact
	} else {
		resolved, err := tunnelruntime.CurrentFRPArtifact()
		if err != nil {
			return nil, err
		}
		artifact = resolved
	}
	directory := options.frpRuntimeDirectory
	if directory == "" {
		resolved, err := tunnelruntime.DefaultFRPRuntimeDirectory()
		if err != nil {
			return nil, err
		}
		directory = resolved
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve managed FRP runtime directory: %w", err)
	}
	ensure := options.ensureFRPRuntime
	if ensure == nil {
		ensure = tunnelruntime.EnsureFRPRuntimeAt
	}
	paths, err := ensure(ctx, directory, artifact)
	if err != nil {
		return nil, err
	}
	expected := tunnelruntime.FRPRuntimePathsFor(directory, artifact.Target)
	if paths != expected {
		return nil, errors.New("managed FRP preparation returned paths outside the pinned runtime directory")
	}
	supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{
		BinaryPath: paths.FRPC,
		Role:       tunnelruntime.FRPRoleClient,
		Logger:     options.Logger,
	})
	if err != nil {
		return nil, err
	}
	return &managedClientFRPRuntime{binaryPath: paths.FRPC, supervisor: supervisor}, nil
}

func defaultClientFRPRuntime(ctx context.Context, logger logging.Logger) (ClientFRPRuntime, error) {
	return newManagedClientFRPRuntime(ctx, managedClientFRPRuntimeOptions{Logger: logger})
}

func (runtime *managedClientFRPRuntime) Verify(ctx context.Context, configurationPath string) error {
	if runtime == nil || strings.TrimSpace(runtime.binaryPath) == "" {
		return errors.New("Tunnel client FRP runtime is unavailable")
	}
	return verifyFRPCConfiguration(ctx, runtime.binaryPath, configurationPath, frpcConfigurationVerificationTimeout)
}

func (runtime *managedClientFRPRuntime) Start(configurationPath string) error {
	if runtime == nil || runtime.supervisor == nil {
		return errors.New("Tunnel client FRP runtime is unavailable")
	}
	return runtime.supervisor.Start(configurationPath)
}

func (runtime *managedClientFRPRuntime) Restart() error {
	if runtime == nil || runtime.supervisor == nil {
		return errors.New("Tunnel client FRP runtime is unavailable")
	}
	return runtime.supervisor.Restart()
}

func (runtime *managedClientFRPRuntime) Stop() error {
	if runtime == nil || runtime.supervisor == nil {
		return nil
	}
	return runtime.supervisor.Stop()
}

func (runtime *managedClientFRPRuntime) State() tunnelruntime.FRPSupervisorState {
	if runtime == nil || runtime.supervisor == nil {
		return tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessStopped}
	}
	return runtime.supervisor.State()
}

func (runtime *managedClientFRPRuntime) Observe(listener func(tunnelruntime.FRPSupervisorState)) func() {
	if runtime == nil || runtime.supervisor == nil {
		return func() {}
	}
	return runtime.supervisor.Observe(listener)
}

func verifyFRPCConfiguration(ctx context.Context, binaryPath, configurationPath string, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	verificationContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := exec.CommandContext(verificationContext, binaryPath, "verify", "-c", configurationPath).CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(verificationContext.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: frpc did not respond within %s", ErrFRPCConfigurationVerificationTimeout, timeout)
	}
	return fmt.Errorf("frpc rejected the generated configuration: %w", err)
}

// RunClient owns one resolved client instance through authentication, v3
// reconciliation, frpc supervision, reconnect, and final ordered shutdown.
func RunClient(ctx context.Context, config ClientConfig, options ClientRunOptions) (result error) {
	if ctx == nil {
		ctx = context.Background()
	}
	lifecycle := newClientLifecycle(options.Logger)
	lifecycle.starting(config)
	defer func() {
		lifecycle.shutdown(ctx)
		if result != nil && ctx.Err() == nil {
			lifecycle.failed(result)
		}
		lifecycle.stopped(ctx, result)
	}()
	if ctx.Err() != nil {
		return nil
	}
	backoff, err := clientReconnectBackoff(options.Backoff)
	if err != nil {
		return err
	}
	instance, err := AcquireClientInstance(config, options.InstanceIdentity, ClientInstanceOptions{
		StateRoot: options.StateRoot,
		OnCleanupError: func(cleanupErr error) {
			lifecycle.event(logging.Warn, "state.cleanup_warning", "Tunnel client state cleanup warning", map[string]any{"failureClass": "cleanup"})
		},
	})
	if err != nil {
		return err
	}

	connections := &clientControlConnectionHolder{}
	reporter := &clientProcessStateReporter{
		state:     tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessStopped},
		lifecycle: lifecycle,
	}
	var runtime ClientFRPRuntime
	var reconciler *ClientReconciler
	var stopObserving func()
	defer func() {
		result = errors.Join(result, connections.Close())
		reporter.Clear()
		if stopObserving != nil {
			stopObserving()
		}
		if reconciler != nil {
			result = errors.Join(result, reconciler.Stop())
		} else if runtime != nil {
			result = errors.Join(result, runtime.Stop())
		}
		result = errors.Join(result, instance.Release())
	}()

	shutdownDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			lifecycle.shutdown(ctx)
			_ = connections.Close()
		case <-shutdownDone:
		}
	}()
	defer close(shutdownDone)

	lastAppliedRevision := int64(0)
	if applied, found := ReadClientAppliedState(instance.StateDirectory); found {
		lastAppliedRevision = applied.Revision
	}
	agent, err := NewClientAgent(ClientAgentOptions{
		Config:              config,
		YCYVersion:          clientRunYCYVersion(options.YCYVersion),
		LastAppliedRevision: lastAppliedRevision,
		HTTPClient:          options.HTTPClient,
		WebSocketDialer:     options.WebSocketDialer,
		OnAuthenticated: func() error {
			if options.OnAuthenticated == nil {
				return nil
			}
			if rememberErr := options.OnAuthenticated(); rememberErr != nil {
				lifecycle.event(logging.Warn, "connection.remember_warning", "Tunnel connection remember warning", map[string]any{"failureClass": "cleanup"})
			}
			return nil
		},
	})
	if err != nil {
		return err
	}

	lifecycle.started(lastAppliedRevision > 0)
	newRuntime := options.newRuntime
	if newRuntime == nil {
		newRuntime = defaultClientFRPRuntime
	}
	failures := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		lifecycle.event(logging.Debug, "control.attempt", "Tunnel control connection attempt", map[string]any{"attempt": failures + 1})
		connection, connectErr := agent.Connect(ctx)
		if connectErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			if clientControlFailureIsFatal(connectErr) {
				return connectErr
			}
			lifecycle.connectionFailure(connectErr)
			if !clientReconnectContext(ctx, options.Logger, failures, backoff) {
				return nil
			}
			failures++
			continue
		}
		lifecycle.authenticated()
		failures = 0
		connections.Set(connection)
		if ctx.Err() != nil {
			return nil
		}

		if runtime == nil {
			runtime, err = newRuntime(ctx, options.Logger)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				closeClientControlSocket(connection.socket, 4406, "Client failed to prepare frpc")
				return err
			}
			reconciler, err = NewClientReconciler(ClientReconcilerOptions{
				StateDirectory: instance.StateDirectory,
				Runtime:        runtime,
				LogLevel:       options.LogLevel,
			})
			if err != nil {
				closeClientControlSocket(connection.socket, 4406, "Client failed to prepare frpc")
				return err
			}
			if observer, supported := runtime.(clientFRPRuntimeStateObserver); supported {
				reporter.Report(observer.State())
				stopObserving = observer.Observe(reporter.Report)
			}
		}
		reporter.Set(connection)

		connectionErr := runClientControlConnectionWithLifecycle(ctx, agent, connection, reconciler, reporter, lifecycle)
		reporter.ClearConnection(connection)
		connections.Clear(connection)
		_ = connection.Close()
		if errors.Is(connectionErr, errClientControlRevoked) || ctx.Err() != nil {
			return nil
		}
		if clientControlFailureIsFatal(connectionErr) {
			return connectionErr
		}
		lifecycle.connectionFailure(connectionErr)
		if !clientReconnectContext(ctx, options.Logger, failures, backoff) {
			return nil
		}
		failures++
	}
}

func clientRunYCYVersion(value string) string {
	if strings.TrimSpace(value) == "" {
		return clientDefaultYCYVersion
	}
	return value
}

func clientReconnectBackoff(configured []time.Duration) ([]time.Duration, error) {
	backoff := configured
	if len(backoff) == 0 {
		backoff = tunnelruntime.DefaultFRPRecoveryBackoff()
	}
	backoff = append([]time.Duration(nil), backoff...)
	for _, delay := range backoff {
		if delay < 0 {
			return nil, fmt.Errorf("Tunnel client reconnect delay must not be negative")
		}
	}
	return backoff, nil
}

func clientReconnect(logger logging.Logger, failures int, backoff []time.Duration) {
	_ = clientReconnectContext(context.Background(), logger, failures, backoff)
}

func clientReconnectContext(ctx context.Context, logger logging.Logger, failures int, backoff []time.Duration) bool {
	index := failures
	if index >= len(backoff) {
		index = len(backoff) - 1
	}
	delay := backoff[index]
	logger.Event(logging.Debug, clientEventReconnect, "Tunnel control reconnect scheduled", map[string]any{
		"delayMs":       delay.Milliseconds(),
		"attempt":       failures + 1,
		"backoffCapped": failures >= len(backoff),
	})
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func clientControlFailureIsFatal(err error) bool {
	return errors.Is(err, ErrClientAuthentication) || errors.Is(err, ErrClientIncompatible) || errors.Is(err, ErrClientProtocol) || errors.Is(err, errClientControlFatal)
}

func runClientControlConnection(ctx context.Context, agent *ClientAgent, connection *ClientControlConnection, reconciler *ClientReconciler, reporter *clientProcessStateReporter) error {
	return runClientControlConnectionWithLifecycle(ctx, agent, connection, reconciler, reporter, nil)
}

func runClientControlConnectionWithLifecycle(ctx context.Context, agent *ClientAgent, connection *ClientControlConnection, reconciler *ClientReconciler, reporter *clientProcessStateReporter, lifecycle *clientLifecycle) error {
	configuration := clientDesiredConfigurationFromWelcome(connection.Welcome)
	if err := reportClientApplyWithLifecycle(ctx, agent, connection, reconciler, reporter, configuration, lifecycle); err != nil {
		return err
	}
	for {
		source, err := connection.ReadMessage()
		if err != nil {
			return err
		}
		message, err := decodeClientControlMessage(source)
		if err != nil {
			if errors.Is(err, ErrClientIncompatible) {
				closeClientControlSocket(connection.socket, 4406, "Client failed to process control message")
			} else {
				closeClientControlSocket(connection.socket, 4400, "Invalid control message")
			}
			return err
		}
		switch message.kind {
		case "desired_state":
			configuration.Snapshot = message.desired.Snapshot
			if err := reportClientApplyWithLifecycle(ctx, agent, connection, reconciler, reporter, configuration, lifecycle); err != nil {
				return err
			}
		case "restart_frpc":
			if lifecycle != nil {
				lifecycle.event(logging.Debug, "frp.restart_requested", "FRP restart requested", nil)
			}
			if err := reconciler.Restart(); err != nil {
				closeClientControlSocket(connection.socket, 4406, "Client failed to process control message")
				return fmt.Errorf("%w: restart frpc: %v", errClientControlFatal, err)
			}
			if err := reporter.Publish(); err != nil {
				return fmt.Errorf("report Tunnel client process state: %w", err)
			}
			if lifecycle != nil {
				lifecycle.event(logging.Info, "frp.restarted", "FRP client restarted", nil)
			}
		case "revoke":
			if lifecycle != nil {
				lifecycle.revoked(message.revokeReason)
			}
			return &clientControlRevokedError{reason: message.revokeReason}
		default:
			return fmt.Errorf("%w: unexpected control message", ErrClientProtocol)
		}
	}
}

func clientDesiredConfigurationFromWelcome(welcome tunnelruntime.AgentWelcome) ClientDesiredConfiguration {
	return ClientDesiredConfiguration{
		AdvertisedFRPHost: welcome.AdvertisedFRPHost,
		AdvertisedFRPPort: welcome.AdvertisedFRPPort,
		InternalFRPToken:  welcome.InternalFRPToken,
		Snapshot:          welcome.Snapshot,
	}
}

func reportClientApply(ctx context.Context, agent *ClientAgent, connection *ClientControlConnection, reconciler *ClientReconciler, reporter *clientProcessStateReporter, desired ClientDesiredConfiguration) error {
	return reportClientApplyWithLifecycle(ctx, agent, connection, reconciler, reporter, desired, nil)
}

func reportClientApplyWithLifecycle(ctx context.Context, agent *ClientAgent, connection *ClientControlConnection, reconciler *ClientReconciler, reporter *clientProcessStateReporter, desired ClientDesiredConfiguration, lifecycle *clientLifecycle) error {
	applyResult, err := reconciler.ApplyWithResult(ctx, desired)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	revision := desired.Snapshot.Revision
	if err == nil {
		agent.RecordAppliedRevision(revision)
		if writeErr := connection.WriteJSON(tunnelruntime.ApplyResult{
			Type:                  "apply_result",
			TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
			Revision:              revision,
			Success:               true,
		}); writeErr != nil {
			return fmt.Errorf("acknowledge Tunnel desired state: %w", writeErr)
		}
	} else {
		code := clientReconciliationErrorCode(err)
		if code == "" {
			code = "APPLY_FAILED"
		}
		if writeErr := connection.WriteJSON(tunnelruntime.ApplyResult{
			Type:                  "apply_result",
			TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
			Revision:              revision,
			Success:               false,
			Error: &tunnelruntime.StructuredRuntimeError{
				Code: code, Message: err.Error(), Revision: &revision,
			},
		}); writeErr != nil {
			return fmt.Errorf("report Tunnel desired-state failure: %w", writeErr)
		}
	}
	if lifecycle != nil {
		lifecycle.apply(applyResult, err)
	}
	if writeErr := reporter.Publish(); writeErr != nil {
		return fmt.Errorf("report Tunnel client process state: %w", writeErr)
	}
	return nil
}

type clientControlMessage struct {
	kind         string
	desired      tunnelruntime.DesiredState
	revokeReason string
}

type clientControlRevokedError struct{ reason string }

func (err *clientControlRevokedError) Error() string { return errClientControlRevoked.Error() }
func (err *clientControlRevokedError) Unwrap() error { return errClientControlRevoked }

func decodeClientControlMessage(source []byte) (clientControlMessage, error) {
	var envelope struct {
		Type                  string `json:"type"`
		TunnelProtocolVersion int    `json:"tunnelProtocolVersion"`
	}
	if err := json.Unmarshal(source, &envelope); err != nil {
		return clientControlMessage{}, fmt.Errorf("%w: decode control message", ErrClientProtocol)
	}
	if envelope.Type == "incompatible" {
		var incompatible tunnelruntime.Incompatible
		if err := json.Unmarshal(source, &incompatible); err != nil || incompatible.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion || strings.TrimSpace(incompatible.Message) == "" {
			return clientControlMessage{}, fmt.Errorf("%w: invalid incompatibility message", ErrClientProtocol)
		}
		return clientControlMessage{}, fmt.Errorf("%w: %s", ErrClientIncompatible, incompatible.Message)
	}
	if envelope.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion {
		return clientControlMessage{}, fmt.Errorf("%w: Control plane uses an unsupported tunnel protocol; upgrade ycy", ErrClientIncompatible)
	}
	switch envelope.Type {
	case "desired_state":
		var desired tunnelruntime.DesiredState
		if err := json.Unmarshal(source, &desired); err != nil || desired.Snapshot.Revision < 0 || desired.Snapshot.Revision > clientMaximumSafeInteger {
			return clientControlMessage{}, fmt.Errorf("%w: invalid desired-state message", ErrClientProtocol)
		}
		return clientControlMessage{kind: envelope.Type, desired: desired}, nil
	case "restart_frpc":
		return clientControlMessage{kind: envelope.Type}, nil
	case "revoke":
		var revoke tunnelruntime.Revoke
		if err := json.Unmarshal(source, &revoke); err != nil || (revoke.Reason != "rotated" && revoke.Reason != "deleted") {
			return clientControlMessage{}, fmt.Errorf("%w: invalid revoke message", ErrClientProtocol)
		}
		return clientControlMessage{kind: envelope.Type, revokeReason: revoke.Reason}, nil
	default:
		return clientControlMessage{}, fmt.Errorf("%w: unexpected control message", ErrClientProtocol)
	}
}

type clientControlConnectionHolder struct {
	mu         sync.Mutex
	connection *ClientControlConnection
}

func (holder *clientControlConnectionHolder) Set(connection *ClientControlConnection) {
	holder.mu.Lock()
	holder.connection = connection
	holder.mu.Unlock()
}

func (holder *clientControlConnectionHolder) Clear(connection *ClientControlConnection) {
	holder.mu.Lock()
	if holder.connection == connection {
		holder.connection = nil
	}
	holder.mu.Unlock()
}

func (holder *clientControlConnectionHolder) Close() error {
	holder.mu.Lock()
	connection := holder.connection
	holder.connection = nil
	holder.mu.Unlock()
	if connection == nil {
		return nil
	}
	return connection.Close()
}

type clientProcessStateReporter struct {
	mu          sync.Mutex
	connection  *ClientControlConnection
	state       tunnelruntime.FRPSupervisorState
	previous    tunnelruntime.FRPProcessState
	initialized bool
	lifecycle   *clientLifecycle
}

func (reporter *clientProcessStateReporter) Set(connection *ClientControlConnection) {
	reporter.mu.Lock()
	reporter.connection = connection
	reporter.mu.Unlock()
	_ = reporter.Publish()
}

func (reporter *clientProcessStateReporter) Clear() {
	reporter.mu.Lock()
	reporter.connection = nil
	reporter.mu.Unlock()
}

func (reporter *clientProcessStateReporter) ClearConnection(connection *ClientControlConnection) {
	reporter.mu.Lock()
	if reporter.connection == connection {
		reporter.connection = nil
	}
	reporter.mu.Unlock()
}

func (reporter *clientProcessStateReporter) Report(state tunnelruntime.FRPSupervisorState) {
	reporter.mu.Lock()
	previous := reporter.state.State
	initialized := reporter.initialized
	reporter.state = tunnelruntime.CloneFRPSupervisorState(state)
	reporter.initialized = true
	lifecycle := reporter.lifecycle
	reporter.mu.Unlock()
	if lifecycle != nil && initialized && previous != state.State {
		switch state.State {
		case tunnelruntime.FRPProcessRunning:
			if previous == tunnelruntime.FRPProcessRecovering {
				lifecycle.event(logging.Info, "frp.recovered", "FRP client recovered", nil)
			} else {
				lifecycle.event(logging.Info, "frp.running", "FRP client running", nil)
			}
		case tunnelruntime.FRPProcessStopped:
			lifecycle.event(logging.Info, "frp.stopped", "FRP client stopped", nil)
		case tunnelruntime.FRPProcessRecovering:
			lifecycle.event(logging.Warn, "frp.recovering", "FRP client recovering", map[string]any{"failureClass": "frp-child"})
		case tunnelruntime.FRPProcessConfigurationFailed:
			lifecycle.event(logging.Error, "frp.failed", "FRP client failed", map[string]any{"failureClass": "configuration"})
		}
	}
	_ = reporter.Publish()
}

func (reporter *clientProcessStateReporter) Publish() error {
	reporter.mu.Lock()
	connection := reporter.connection
	state := tunnelruntime.CloneFRPSupervisorState(reporter.state)
	reporter.mu.Unlock()
	if connection == nil {
		return nil
	}
	return connection.WriteJSON(tunnelruntime.ProcessState{
		Type:                  "process_state",
		TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
		State:                 state.State,
		Error:                 state.Error,
	})
}
