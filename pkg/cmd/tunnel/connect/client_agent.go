package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

var (
	ErrClientAuthentication = errors.New("Tunnel client authentication failed")
	ErrClientIncompatible   = errors.New("Tunnel client is incompatible with the control plane")
	ErrClientProtocol       = errors.New("Tunnel client received an invalid control message")
)

// ClientAgentOptions defines one v4 control-link handshake. Reconciliation,
// supervision, and reconnect ownership are added by later client slices.
type ClientAgentOptions struct {
	Config              ClientConfig
	YCYVersion          string
	LastAppliedRevision int64
	HTTPClient          *http.Client
	WebSocketDialer     *websocket.Dialer
	OnAuthenticated     func() error
	LastRestartResult   *tunnelruntime.RestartResult

	expectedArtifact *tunnelruntime.FRPArtifact
	wireTarget       *tunnelruntime.WireTarget
}

// ClientAgent owns the authentication probe and first v4 WebSocket exchange.
type ClientAgent struct {
	config              ClientConfig
	ycyVersion          string
	lastAppliedRevision int64
	lastRestartResult   *tunnelruntime.RestartResult
	httpClient          *http.Client
	dialer              *websocket.Dialer
	expectedArtifact    tunnelruntime.FRPArtifact
	wireTarget          tunnelruntime.WireTarget
	onAuthenticated     func() error

	mu                     sync.Mutex
	authenticationReported bool
}

// ClientControlConnection is one authenticated v4 socket after its welcome
// has passed the compiled protocol and FRP-artifact checks.
type ClientControlConnection struct {
	Welcome tunnelruntime.AgentWelcome

	socket      *websocket.Conn
	closeOnce   sync.Once
	closeErr    error
	outbound    chan clientControlWrite
	done        chan struct{}
	doneOnce    sync.Once
	writerMu    sync.Mutex
	writerErr   error
	readTimeout time.Duration
}

type clientControlWrite struct {
	value  any
	result chan error
}

var (
	clientControlAttemptTimeout = 30 * time.Second
	clientControlPingInterval   = 30 * time.Second
	clientControlReadTimeout    = 75 * time.Second
	clientControlWriteTimeout   = 10 * time.Second
)

// NewClientAgent validates process-local handshake dependencies without
// connecting to the control plane.
func NewClientAgent(options ClientAgentOptions) (*ClientAgent, error) {
	if options.Config.Server == nil || strings.TrimSpace(options.Config.Token) == "" {
		return nil, fmt.Errorf("Tunnel client server and token are required")
	}
	if options.LastAppliedRevision < 0 || options.LastAppliedRevision > clientMaximumSafeInteger {
		return nil, fmt.Errorf("Tunnel client applied revision is invalid")
	}
	if options.LastRestartResult != nil && !validClientRestartResult(*options.LastRestartResult) {
		return nil, fmt.Errorf("Tunnel client restart result is invalid")
	}
	artifact := tunnelruntime.FRPArtifact{}
	if options.expectedArtifact != nil {
		artifact = *options.expectedArtifact
	} else {
		resolved, err := tunnelruntime.CurrentFRPArtifact()
		if err != nil {
			return nil, err
		}
		artifact = resolved
	}
	target := tunnelruntime.WireTarget{}
	if options.wireTarget != nil {
		target = *options.wireTarget
	} else {
		resolved, err := tunnelruntime.CurrentWireTarget()
		if err != nil {
			return nil, err
		}
		target = resolved
	}
	if _, _, err := target.GoTarget(); err != nil {
		return nil, err
	}
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.WebSocketDialer == nil {
		options.WebSocketDialer = websocket.DefaultDialer
	}
	return &ClientAgent{
		config:              options.Config,
		ycyVersion:          options.YCYVersion,
		lastAppliedRevision: options.LastAppliedRevision,
		httpClient:          options.HTTPClient,
		dialer:              options.WebSocketDialer,
		expectedArtifact:    artifact,
		wireTarget:          target,
		onAuthenticated:     options.OnAuthenticated,
		lastRestartResult:   cloneClientRestartResult(options.LastRestartResult),
	}, nil
}

// Probe verifies that the control plane has not rejected the current Bearer
// token before an instance can activate any cached or fresh FRPC state.
func (agent *ClientAgent) Probe(ctx context.Context) error {
	if agent == nil {
		return fmt.Errorf("Tunnel client agent is unavailable")
	}
	endpoint := clientAgentEndpoint(agent.config.Server, false)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create Tunnel control authentication probe: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+agent.config.Token)
	response, err := agent.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("probe Tunnel control plane authentication: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: Client Token was rejected by the Tunnel Control Plane", ErrClientAuthentication)
	}
	return nil
}

// Connect probes first, completes one hello/welcome exchange, and exposes the
// authenticated socket to the later reconciler and lifecycle owner.
func (agent *ClientAgent) Connect(ctx context.Context) (_ *ClientControlConnection, result error) {
	attemptContext, cancel := context.WithTimeout(ctx, clientControlAttemptTimeout)
	defer cancel()
	if err := agent.Probe(attemptContext); err != nil {
		return nil, err
	}
	endpoint := clientAgentEndpoint(agent.config.Server, true)
	headers := http.Header{"Authorization": []string{"Bearer " + agent.config.Token}}
	socket, response, err := agent.dialer.DialContext(attemptContext, endpoint.String(), headers)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("%w: Client Token was rejected by the Tunnel Control Plane", ErrClientAuthentication)
		}
		return nil, fmt.Errorf("connect Tunnel control plane: %w", err)
	}
	defer func() {
		if result != nil {
			_ = socket.Close()
		}
	}()

	agent.mu.Lock()
	lastAppliedRevision := agent.lastAppliedRevision
	lastRestartResult := cloneClientRestartResult(agent.lastRestartResult)
	agent.mu.Unlock()
	hello := tunnelruntime.AgentHello{
		Type:                  "hello",
		TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
		YCYVersion:            agent.ycyVersion,
		Platform:              string(agent.wireTarget.Platform),
		Architecture:          string(agent.wireTarget.Architecture),
		LastAppliedRevision:   lastAppliedRevision,
		LastRestartResult:     lastRestartResult,
	}
	if err := socket.SetWriteDeadline(time.Now().Add(clientControlWriteTimeout)); err != nil {
		return nil, fmt.Errorf("set Tunnel client hello deadline: %w", err)
	}
	if err := socket.WriteJSON(hello); err != nil {
		return nil, fmt.Errorf("send Tunnel client hello: %w", err)
	}
	if deadline, ok := attemptContext.Deadline(); ok {
		_ = socket.SetReadDeadline(deadline)
	}
	_, source, err := socket.ReadMessage()
	if err != nil {
		return nil, clientControlReadError(err)
	}
	welcome, err := decodeClientWelcome(source, agent.expectedArtifact)
	if err != nil {
		if errors.Is(err, ErrClientProtocol) {
			closeClientControlSocket(socket, 4400, "Invalid control message")
		} else {
			closeClientControlSocket(socket, 4406, "Client failed to process control message")
		}
		return nil, err
	}
	if err := agent.reportAuthentication(); err != nil {
		closeClientControlSocket(socket, 4406, "Client failed to process control message")
		return nil, err
	}
	return newClientControlConnection(socket, welcome), nil
}

func (agent *ClientAgent) RecordRestartResult(result tunnelruntime.RestartResult) {
	if agent == nil || !validClientRestartResult(result) {
		return
	}
	agent.mu.Lock()
	if agent.lastRestartResult == nil || result.Generation >= agent.lastRestartResult.Generation {
		agent.lastRestartResult = cloneClientRestartResult(&result)
	}
	agent.mu.Unlock()
}

func cloneClientRestartResult(result *tunnelruntime.RestartResult) *tunnelruntime.RestartResult {
	if result == nil {
		return nil
	}
	copy := *result
	if result.Error != nil {
		errorCopy := *result.Error
		copy.Error = &errorCopy
	}
	return &copy
}

func (agent *ClientAgent) reportAuthentication() error {
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.authenticationReported {
		return nil
	}
	if agent.onAuthenticated != nil {
		if err := agent.onAuthenticated(); err != nil {
			return err
		}
	}
	agent.authenticationReported = true
	return nil
}

// RecordAppliedRevision advances the next reconnect hello after a local
// reconciler has durably applied and acknowledged a desired snapshot.
func (agent *ClientAgent) RecordAppliedRevision(revision int64) {
	if agent == nil || revision < 0 {
		return
	}
	agent.mu.Lock()
	if revision > agent.lastAppliedRevision {
		agent.lastAppliedRevision = revision
	}
	agent.mu.Unlock()
}

func clientAgentEndpoint(server *url.URL, websocketEndpoint bool) *url.URL {
	endpoint := *server
	endpoint.Path = "/api/agent"
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.ForceQuery = false
	endpoint.Fragment = ""
	if websocketEndpoint {
		switch endpoint.Scheme {
		case "https":
			endpoint.Scheme = "wss"
		default:
			endpoint.Scheme = "ws"
		}
	}
	return &endpoint
}

func decodeClientWelcome(source []byte, expected tunnelruntime.FRPArtifact) (tunnelruntime.AgentWelcome, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(source, &envelope); err != nil {
		return tunnelruntime.AgentWelcome{}, fmt.Errorf("%w: decode control message", ErrClientProtocol)
	}
	if envelope.Type == "incompatible" {
		var message tunnelruntime.Incompatible
		if err := json.Unmarshal(source, &message); err != nil || strings.TrimSpace(message.Message) == "" {
			return tunnelruntime.AgentWelcome{}, fmt.Errorf("%w: control plane reported an invalid incompatibility message", ErrClientProtocol)
		}
		return tunnelruntime.AgentWelcome{}, fmt.Errorf("%w: %s", ErrClientIncompatible, message.Message)
	}
	if envelope.Type != "welcome" {
		return tunnelruntime.AgentWelcome{}, fmt.Errorf("%w: expected welcome message", ErrClientProtocol)
	}
	var welcome tunnelruntime.AgentWelcome
	if err := json.Unmarshal(source, &welcome); err != nil {
		return tunnelruntime.AgentWelcome{}, fmt.Errorf("%w: decode welcome message", ErrClientProtocol)
	}
	if welcome.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion {
		return tunnelruntime.AgentWelcome{}, fmt.Errorf("%w: Control plane uses an unsupported tunnel protocol; upgrade ycy", ErrClientIncompatible)
	}
	if welcome.RequiredFRPVersion != tunnelruntime.FRPVersion || welcome.Artifact != expected.Description {
		return tunnelruntime.AgentWelcome{}, fmt.Errorf("%w: Control plane requires an unsupported tunnel protocol or FRP build; upgrade ycy", ErrClientIncompatible)
	}
	if strings.TrimSpace(welcome.AdvertisedFRPHost) == "" || welcome.AdvertisedFRPPort < 1 || welcome.AdvertisedFRPPort > 65535 || strings.TrimSpace(welcome.InternalFRPToken) == "" || welcome.Snapshot.Revision < 0 || welcome.Snapshot.Revision > clientMaximumSafeInteger || welcome.DesiredRestartGeneration < 0 || welcome.DesiredRestartGeneration > clientMaximumSafeInteger {
		return tunnelruntime.AgentWelcome{}, fmt.Errorf("%w: welcome message is incomplete", ErrClientProtocol)
	}
	return welcome, nil
}

func clientControlReadError(err error) error {
	var closeError *websocket.CloseError
	if errors.As(err, &closeError) {
		switch closeError.Code {
		case 4400:
			return fmt.Errorf("%w: %s", ErrClientProtocol, closeError.Text)
		case 4401, 4403:
			return fmt.Errorf("%w: %s", ErrClientAuthentication, closeError.Text)
		case 4406:
			return fmt.Errorf("%w: %s", ErrClientIncompatible, closeError.Text)
		}
	}
	return fmt.Errorf("read Tunnel control welcome: %w", err)
}

func closeClientControlSocket(socket *websocket.Conn, code int, message string) {
	if socket == nil {
		return
	}
	_ = socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, message), time.Now().Add(time.Second))
	_ = socket.Close()
}

// WriteJSON serializes client control frames for the later reconciler and
// supervisor observers without exposing concurrent writes to Gorilla.
func (connection *ClientControlConnection) WriteJSON(value any) error {
	if connection == nil || connection.socket == nil {
		return fmt.Errorf("Tunnel client control connection is unavailable")
	}
	request := clientControlWrite{value: value, result: make(chan error, 1)}
	select {
	case connection.outbound <- request:
	case <-connection.done:
		return connection.controlError()
	}
	select {
	case err := <-request.result:
		return err
	case <-connection.done:
		return connection.controlError()
	}
}

// ReadMessage returns the next server frame after the accepted welcome and
// preserves terminal control-plane close meaning for the lifecycle owner.
func (connection *ClientControlConnection) ReadMessage() ([]byte, error) {
	if connection == nil || connection.socket == nil {
		return nil, fmt.Errorf("Tunnel client control connection is unavailable")
	}
	_, source, err := connection.socket.ReadMessage()
	if err != nil {
		return nil, clientControlReadError(err)
	}
	_ = connection.socket.SetReadDeadline(time.Now().Add(connection.readTimeout))
	return source, nil
}

// Close releases the physical control socket. It is safe to call repeatedly.
func (connection *ClientControlConnection) Close() error {
	if connection == nil || connection.socket == nil {
		return nil
	}
	connection.closeOnce.Do(func() {
		connection.closeErr = connection.socket.Close()
		connection.finish(connection.closeErr)
	})
	return connection.closeErr
}

func newClientControlConnection(socket *websocket.Conn, welcome tunnelruntime.AgentWelcome) *ClientControlConnection {
	readTimeout := clientControlReadTimeout
	pingInterval := clientControlPingInterval
	writeTimeout := clientControlWriteTimeout
	connection := &ClientControlConnection{socket: socket, Welcome: welcome, outbound: make(chan clientControlWrite, 16), done: make(chan struct{}), readTimeout: readTimeout}
	_ = socket.SetWriteDeadline(time.Time{})
	_ = socket.SetReadDeadline(time.Now().Add(readTimeout))
	socket.SetPongHandler(func(string) error {
		return socket.SetReadDeadline(time.Now().Add(readTimeout))
	})
	go connection.writeLoop(pingInterval, writeTimeout)
	return connection
}

func (connection *ClientControlConnection) writeLoop(pingInterval, writeTimeout time.Duration) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-connection.done:
			return
		case request := <-connection.outbound:
			err := connection.socket.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err == nil {
				err = connection.socket.WriteJSON(request.value)
			}
			request.result <- err
			if err != nil {
				_ = connection.socket.Close()
				connection.finish(err)
				return
			}
		case <-ticker.C:
			err := connection.socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
			if err != nil {
				_ = connection.socket.Close()
				connection.finish(err)
				return
			}
		}
	}
}

func (connection *ClientControlConnection) finish(err error) {
	connection.writerMu.Lock()
	if connection.writerErr == nil {
		connection.writerErr = err
	}
	connection.writerMu.Unlock()
	connection.doneOnce.Do(func() { close(connection.done) })
}

func (connection *ClientControlConnection) controlError() error {
	connection.writerMu.Lock()
	defer connection.writerMu.Unlock()
	if connection.writerErr != nil {
		return connection.writerErr
	}
	return fmt.Errorf("Tunnel client control connection is closed")
}
