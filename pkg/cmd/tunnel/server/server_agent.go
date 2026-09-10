package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/hackycy/hackycy-cli/internal/logging"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

var ErrServerAgentGatewayConfiguration = errors.New("Tunnel server agent gateway configuration is invalid")

// ServerAgentFRPSStateProvider keeps agent authorization independent of the
// HTTP state projection while preserving the managed FRPS availability gate.
type ServerAgentFRPSStateProvider interface {
	FRPSState() tunnelruntime.FRPSupervisorState
}

// ServerAgentWelcomeSettings contains the deployment secret and endpoint that
// are safe only for an already authenticated agent welcome frame.
type ServerAgentWelcomeSettings struct {
	AdvertisedFRPHost string
	AdvertisedFRPPort int64
	InternalFRPToken  string
}

// ServerAgentWelcomeSource keeps deployment-secret ownership with the FRPS
// composition instead of the HTTP state projection.
type ServerAgentWelcomeSource interface {
	AgentWelcomeSettings(requestHost string) ServerAgentWelcomeSettings
}

type ServerAgentGatewayOptions struct {
	ControlPlane  *ServerControlPlane
	FRPS          ServerAgentFRPSStateProvider
	WelcomeSource ServerAgentWelcomeSource
	Logger        logging.Logger
}

// ServerAgentGateway owns process-local agent admission, connection runtime
// projection, handshake reads, and desired/revoke presentation. Durable
// desired state remains in ServerControlPlane; later protocol messages stay
// outside this boundary.
type ServerAgentGateway struct {
	controlPlane  *ServerControlPlane
	frps          ServerAgentFRPSStateProvider
	welcomeSource ServerAgentWelcomeSource
	logger        logging.Logger
	warningMu     sync.Mutex
	warnings      map[string]bool

	mu       sync.RWMutex
	slots    map[string]serverAgentSlot
	runtime  map[string]serverAgentRuntime
	nextSlot uint64
}

type serverAgentSlot struct {
	generation uint64
	active     bool
	revoking   bool
	connection *ServerAgentConnection
}

type serverAgentRuntime struct {
	processState        tunnelruntime.FRPProcessState
	lastError           *tunnelruntime.StructuredRuntimeError
	lastAppliedRevision int64
	hasAppliedRevision  bool
	lastApplySuccess    bool
	lastApplyCode       string
	hasApplyResult      bool
	lastProcessState    tunnelruntime.FRPProcessState
	lastProcessCode     string
	hasProcessState     bool
}

func NewServerAgentGateway(options ServerAgentGatewayOptions) (*ServerAgentGateway, error) {
	if options.ControlPlane == nil {
		return nil, fmt.Errorf("%w: control plane is required", ErrServerAgentGatewayConfiguration)
	}
	if options.FRPS == nil {
		return nil, fmt.Errorf("%w: managed frps state is required", ErrServerAgentGatewayConfiguration)
	}
	gateway := &ServerAgentGateway{
		controlPlane:  options.ControlPlane,
		frps:          options.FRPS,
		welcomeSource: options.WelcomeSource,
		logger:        options.Logger,
		slots:         make(map[string]serverAgentSlot),
		runtime:       make(map[string]serverAgentRuntime),
		warnings:      make(map[string]bool),
	}
	gateway.controlPlane.Subscribe(gateway.handleControlPlaneEvent)
	return gateway, nil
}

func (gateway *ServerAgentGateway) lifecycleEvent(level logging.Level, id, message string, fields map[string]any) {
	if gateway == nil {
		return
	}
	gateway.logger.Event(level, id, message, fields)
}

func (gateway *ServerAgentGateway) warning(category, failureClass string) {
	gateway.warningForClient("", category, failureClass)
}

func (gateway *ServerAgentGateway) warningForClient(clientID, category, failureClass string) {
	if gateway == nil {
		return
	}
	key := category + "\x00" + clientID
	gateway.warningMu.Lock()
	if gateway.warnings[key] {
		gateway.warningMu.Unlock()
		return
	}
	gateway.warnings[key] = true
	gateway.warningMu.Unlock()
	gateway.lifecycleEvent(logging.Warn, serverEventAgentWarning, "Tunnel agent warning", map[string]any{
		"category":     category,
		"failureClass": failureClass,
	})
}

func (gateway *ServerAgentGateway) clearWarning(category, clientID string) bool {
	if gateway == nil {
		return false
	}
	gateway.warningMu.Lock()
	key := category + "\x00" + clientID
	wasSet := gateway.warnings[key]
	delete(gateway.warnings, key)
	gateway.warningMu.Unlock()
	return wasSet
}

func (gateway *ServerAgentGateway) agentConnected(clientID string) {
	gateway.clearWarning("authentication", clientID)
	if gateway.clearWarning("connection", clientID) {
		gateway.lifecycleEvent(logging.Info, serverEventAgentRestored, "Tunnel agent restored", map[string]any{"clientRef": serverClientRef(clientID)})
		return
	}
	gateway.lifecycleEvent(logging.Info, serverEventAgentConnected, "Tunnel agent connected", map[string]any{"clientRef": serverClientRef(clientID)})
}

func (gateway *ServerAgentGateway) agentDisconnected(clientID string) {
	if gateway == nil {
		return
	}
	gateway.warningMu.Lock()
	key := "connection\x00" + clientID
	if gateway.warnings[key] {
		gateway.warningMu.Unlock()
		return
	}
	gateway.warnings[key] = true
	gateway.warningMu.Unlock()
	gateway.lifecycleEvent(logging.Warn, serverEventAgentDisconnected, "Tunnel agent disconnected", map[string]any{"failureClass": "transport", "clientRef": serverClientRef(clientID)})
}

func (gateway *ServerAgentGateway) agentRevoked(reason string) {
	if reason == "" {
		reason = "deleted"
	}
	gateway.lifecycleEvent(logging.Warn, serverEventAgentRevoked, "Tunnel agent revoked", map[string]any{"reason": reason})
}

func (gateway *ServerAgentGateway) protocolWarning(clientID, category string) {
	if category == "" {
		category = "protocol"
	}
	gateway.warningForClient(clientID, category, "protocol")
}

func (gateway *ServerAgentGateway) controlChange(event ServerControlPlaneEvent) {
	action := "updated"
	object := "control"
	switch event.Type {
	case serverClientCreated:
		action, object = "created", "client"
	case serverClientUpdated:
		action, object = "updated", "client"
	case serverClientRotated:
		action, object = "rotated", "client"
	case serverClientDeleted:
		action, object = "deleted", "client"
	case serverDesiredState:
		action, object = "applied", "desired-state"
	default:
		return
	}
	gateway.lifecycleEvent(logging.Info, serverEventControlChange, "Tunnel control change committed", map[string]any{
		"action": action,
		"object": object,
	})
}

func (gateway *ServerAgentGateway) logApplyResult(result tunnelruntime.ApplyResult, changed, recovered bool) {
	if gateway == nil {
		return
	}
	if !changed {
		return
	}
	fields := map[string]any{"revision": result.Revision}
	if result.Success {
		if recovered {
			gateway.lifecycleEvent(logging.Info, serverEventAgentStateRecovered, "Tunnel agent state recovered", fields)
		} else {
			gateway.lifecycleEvent(logging.Info, serverEventAgentStateChanged, "Tunnel agent state applied", fields)
		}
		return
	}
	fields["failureClass"] = serverAgentFailureClass(result.Error)
	gateway.lifecycleEvent(logging.Warn, serverEventAgentStateWarning, "Tunnel agent state warning", fields)
}

func (gateway *ServerAgentGateway) logProcessState(state tunnelruntime.FRPProcessState) {
	if gateway == nil {
		return
	}
	gateway.lifecycleEvent(logging.Debug, serverEventAgentProcessState, "Tunnel agent process state", map[string]any{"state": string(state)})
}

func serverClientRef(clientID string) string {
	if strings.TrimSpace(clientID) == "" {
		return "unknown"
	}
	digest := sha256.Sum256([]byte(clientID))
	return hex.EncodeToString(digest[:4])
}

func serverAgentFailureClass(lastError *tunnelruntime.StructuredRuntimeError) string {
	if lastError == nil {
		return "unknown"
	}
	switch strings.ToUpper(strings.TrimSpace(lastError.Code)) {
	case "AUTHENTICATION_FAILED", "UNAUTHORIZED":
		return "authentication"
	case "FRPS_UNAVAILABLE", "FRP_START_FAILED":
		return "frps"
	case "CONFIGURATION_FAILED":
		return "configuration"
	case "ACTIVATION_FAILED":
		return "activation"
	case "TRANSPORT", "TIMEOUT":
		return "transport"
	case "CLIENT_CONNECTED", "INVALID_MESSAGE", "INVALID_REVISION", "PROTOCOL":
		return "protocol"
	default:
		return "unknown"
	}
}

// State combines process-local connection ownership and the latest agent
// process report. Desired and applied revisions remain durable control-plane
// state rather than gateway state.
func (gateway *ServerAgentGateway) State(clientID string) ServerClientRuntimeState {
	state := ServerClientRuntimeState{
		ConnectionState: ServerClientDisconnected,
		ProcessState:    tunnelruntime.FRPProcessStopped,
	}
	if gateway == nil {
		return state
	}
	gateway.mu.RLock()
	slot, connected := gateway.slots[clientID]
	runtime, reported := gateway.runtime[clientID]
	gateway.mu.RUnlock()
	if reported {
		state.ProcessState = runtime.processState
		state.LastError = cloneServerAgentRuntimeError(runtime.lastError)
	}
	if connected && slot.revoking {
		state.ConnectionState = ServerClientRevocationPending
		return state
	}
	if connected && slot.active {
		state.ConnectionState = ServerClientConnected
		return state
	}
	client, err := gateway.controlPlane.GetClient(context.Background(), clientID)
	if err == nil && client.RevocationPending {
		state.ConnectionState = ServerClientRevocationPending
	}
	return state
}

func (gateway *ServerAgentGateway) recordProcessState(clientID string, slot uint64, processState tunnelruntime.FRPProcessState, lastError *tunnelruntime.StructuredRuntimeError) bool {
	accepted, _ := gateway.recordProcessStateWithChange(clientID, slot, processState, lastError)
	return accepted
}

func (gateway *ServerAgentGateway) recordProcessStateWithChange(clientID string, slot uint64, processState tunnelruntime.FRPProcessState, lastError *tunnelruntime.StructuredRuntimeError) (bool, bool) {
	if gateway == nil {
		return false, false
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	current, found := gateway.slots[clientID]
	if !found || current.generation != slot || !current.active || current.revoking {
		return false, false
	}
	runtime := gateway.runtime[clientID]
	if runtime.processState == "" {
		runtime.processState = tunnelruntime.FRPProcessStopped
	}
	code := ""
	if lastError != nil {
		code = strings.ToUpper(strings.TrimSpace(lastError.Code))
	}
	changed := !runtime.hasProcessState || runtime.lastProcessState != processState || runtime.lastProcessCode != code
	runtime.processState = processState
	runtime.lastProcessState = processState
	runtime.lastProcessCode = code
	runtime.hasProcessState = true
	if lastError != nil {
		runtime.lastError = cloneServerAgentRuntimeError(lastError)
	} else if runtime.lastError == nil || runtime.lastError.Revision == nil {
		runtime.lastError = nil
	}
	gateway.runtime[clientID] = runtime
	return true, changed
}

func (gateway *ServerAgentGateway) recordRuntimeError(clientID string, slot uint64, lastError *tunnelruntime.StructuredRuntimeError) bool {
	accepted, _, _ := gateway.recordApplyResult(clientID, slot, tunnelruntime.ApplyResult{Revision: runtimeErrorRevision(lastError), Success: lastError == nil, Error: lastError})
	return accepted
}

func runtimeErrorRevision(lastError *tunnelruntime.StructuredRuntimeError) int64 {
	if lastError == nil || lastError.Revision == nil {
		return 0
	}
	return *lastError.Revision
}

// recordApplyResult updates the process-local acknowledgement projection and
// reports whether the incoming revision/error represents a real transition.
func (gateway *ServerAgentGateway) recordApplyResult(clientID string, slot uint64, result tunnelruntime.ApplyResult) (bool, bool, bool) {
	if gateway == nil {
		return false, false, false
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	current, found := gateway.slots[clientID]
	if !found || current.generation != slot || !current.active || current.revoking {
		return false, false, false
	}
	runtime := gateway.runtime[clientID]
	if runtime.processState == "" {
		runtime.processState = tunnelruntime.FRPProcessStopped
	}
	code := ""
	if result.Error != nil {
		code = strings.ToUpper(strings.TrimSpace(result.Error.Code))
	}
	if runtime.hasApplyResult && result.Revision < runtime.lastAppliedRevision {
		// A late success acknowledgement may clear a previously projected
		// error, but it must never move the observed revision backwards.
		if result.Success && !runtime.lastApplySuccess {
			runtime.lastApplySuccess = true
			runtime.lastApplyCode = ""
			runtime.lastError = nil
			gateway.runtime[clientID] = runtime
			return true, true, true
		}
		return true, false, false
	}
	changed := !runtime.hasApplyResult || runtime.lastAppliedRevision != result.Revision || runtime.lastApplySuccess != result.Success || runtime.lastApplyCode != code
	recovered := runtime.hasApplyResult && !runtime.lastApplySuccess && result.Success && result.Revision >= runtime.lastAppliedRevision
	runtime.lastAppliedRevision = result.Revision
	runtime.hasAppliedRevision = true
	runtime.lastApplySuccess = result.Success
	runtime.lastApplyCode = code
	runtime.hasApplyResult = true
	if result.Success {
		runtime.lastError = nil
	} else {
		runtime.lastError = cloneServerAgentRuntimeError(result.Error)
	}
	gateway.runtime[clientID] = runtime
	return true, changed, recovered
}

func cloneServerAgentRuntimeError(value *tunnelruntime.StructuredRuntimeError) *tunnelruntime.StructuredRuntimeError {
	if value == nil {
		return nil
	}
	copy := *value
	if value.Revision != nil {
		revision := *value.Revision
		copy.Revision = &revision
	}
	return &copy
}

// RestartFRPC sends the explicit restart request only to a currently active
// agent that has completed its server-frame presentation.
func (gateway *ServerAgentGateway) RestartFRPC(clientID string) bool {
	if gateway == nil {
		return false
	}
	gateway.mu.RLock()
	slot, found := gateway.slots[clientID]
	gateway.mu.RUnlock()
	if !found || !slot.active || slot.revoking || slot.connection == nil {
		return false
	}
	return slot.connection.RestartFRPC()
}

// ServerAgentReservation holds the one pending connection slot for a Client
// Token until the HTTP upgrade either takes ownership or is rejected.
type ServerAgentReservation struct {
	gateway     *ServerAgentGateway
	clientID    string
	slot        uint64
	releaseOnce sync.Once
}

func (reservation *ServerAgentReservation) ClientID() string {
	if reservation == nil {
		return ""
	}
	return reservation.clientID
}

// Release returns a pending slot to the gateway. It is safe to call more than
// once and cannot release a later reservation for the same client.
func (reservation *ServerAgentReservation) Release() {
	if reservation == nil || reservation.gateway == nil {
		return
	}
	reservation.releaseOnce.Do(func() {
		reservation.gateway.release(reservation.clientID, reservation.slot)
	})
}

// Activate transfers this pending slot to the accepted WebSocket lifetime.
// A stale or already-released reservation cannot take ownership of a later
// connection for the same client.
func (reservation *ServerAgentReservation) Activate() *ServerAgentConnection {
	if reservation == nil || reservation.gateway == nil {
		return nil
	}
	var connection *ServerAgentConnection
	reservation.releaseOnce.Do(func() {
		candidate := &ServerAgentConnection{
			gateway:  reservation.gateway,
			clientID: reservation.clientID,
			slot:     reservation.slot,
		}
		if reservation.gateway.activate(reservation.clientID, reservation.slot, candidate) {
			connection = candidate
		}
	})
	return connection
}

// ServerAgentConnection owns one gateway slot while its accepted WebSocket is
// open, keeps the first accepted hello, and serializes server-frame delivery.
type ServerAgentConnection struct {
	gateway            *ServerAgentGateway
	clientID           string
	slot               uint64
	closeOnce          sync.Once
	helloMu            sync.Mutex
	helloAccepted      bool
	hello              tunnelruntime.AgentHello
	presentationMu     sync.Mutex
	presentationActive bool
	closed             bool
	revoked            bool
	writeFrame         func(any) error
	closeSocket        func(*ServerAgentProtocolError)
}

func (connection *ServerAgentConnection) ClientID() string {
	if connection == nil {
		return ""
	}
	return connection.clientID
}

// AcknowledgeReplacementToken clears durable revocation state only after the
// replacement token has acquired an active agent connection.
func (connection *ServerAgentConnection) AcknowledgeReplacementToken(ctx context.Context) error {
	if connection == nil || connection.gateway == nil {
		return errors.New("Tunnel server agent session is unavailable")
	}
	return connection.gateway.controlPlane.AcknowledgeReplacementToken(ctx, connection.clientID)
}

// Close makes the client eligible for a later authorization. It is idempotent
// and cannot release a newer connection's slot.
func (connection *ServerAgentConnection) Close() {
	if connection == nil || connection.gateway == nil {
		return
	}
	connection.closeOnce.Do(func() {
		connection.helloMu.Lock()
		helloAccepted := connection.helloAccepted
		connection.helloMu.Unlock()
		connection.presentationMu.Lock()
		revoked := connection.revoked
		connection.closed = true
		connection.presentationMu.Unlock()
		connection.gateway.release(connection.clientID, connection.slot)
		if helloAccepted && !revoked {
			connection.gateway.agentDisconnected(connection.clientID)
		}
	})
}

// AttachCloser keeps physical WebSocket ownership at the HTTP boundary while
// letting the gateway terminate an active session after durable revocation.
func (connection *ServerAgentConnection) AttachCloser(closeSocket func(*ServerAgentProtocolError)) {
	if connection == nil {
		return
	}
	connection.presentationMu.Lock()
	if !connection.closed {
		connection.closeSocket = closeSocket
	}
	connection.presentationMu.Unlock()
}

// Authorize validates one Bearer Client Token and atomically reserves its
// pending control-session slot. WebSocket upgrade handling is intentionally
// separate so a non-upgrade request can release this reservation first.
func (gateway *ServerAgentGateway) Authorize(ctx context.Context, authorization string) (*ServerAgentReservation, error) {
	if gateway.frps.FRPSState().State != tunnelruntime.FRPProcessRunning {
		gateway.warning("frps", "frps")
		return nil, serverDomainError("FRPS_UNAVAILABLE", "Managed frps is not running")
	}
	token := parseServerAgentBearerToken(authorization)
	if token == "" {
		gateway.warning("authentication", "authentication")
		return nil, serverDomainError("AUTHENTICATION_FAILED", "Client Token is invalid")
	}
	client, err := gateway.controlPlane.FindClientByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if client == nil {
		gateway.warning("authentication", "authentication")
		return nil, serverDomainError("AUTHENTICATION_FAILED", "Client Token is invalid")
	}
	slot, reserved := gateway.reserve(client.ID)
	if !reserved {
		gateway.warning("connection", "protocol")
		return nil, serverDomainError("CLIENT_CONNECTED", "Client Token already has an active control session")
	}
	return &ServerAgentReservation{gateway: gateway, clientID: client.ID, slot: slot}, nil
}

func parseServerAgentBearerToken(authorization string) string {
	separator := strings.IndexByte(authorization, ' ')
	if separator < 1 || !strings.EqualFold(authorization[:separator], "bearer") {
		return ""
	}
	return strings.TrimSpace(authorization[separator+1:])
}

func (gateway *ServerAgentGateway) reserve(clientID string) (uint64, bool) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if _, found := gateway.slots[clientID]; found {
		return 0, false
	}
	gateway.nextSlot++
	if gateway.nextSlot == 0 {
		gateway.nextSlot++
	}
	gateway.slots[clientID] = serverAgentSlot{generation: gateway.nextSlot}
	return gateway.nextSlot, true
}

func (gateway *ServerAgentGateway) activate(clientID string, slot uint64, connection *ServerAgentConnection) bool {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	current, found := gateway.slots[clientID]
	if !found || current.generation != slot || current.active || connection == nil {
		return false
	}
	current.active = true
	current.connection = connection
	gateway.slots[clientID] = current
	if _, found := gateway.runtime[clientID]; !found {
		gateway.runtime[clientID] = serverAgentRuntime{processState: tunnelruntime.FRPProcessStopped}
	}
	return true
}

func (gateway *ServerAgentGateway) release(clientID string, slot uint64) {
	gateway.mu.Lock()
	if current, found := gateway.slots[clientID]; found && current.generation == slot {
		delete(gateway.slots, clientID)
	}
	gateway.mu.Unlock()
}

func (gateway *ServerAgentGateway) handleControlPlaneEvent(event ServerControlPlaneEvent) {
	gateway.controlChange(event)
	switch event.Type {
	case serverDesiredState:
		gateway.mu.RLock()
		slot, found := gateway.slots[event.ClientID]
		gateway.mu.RUnlock()
		if found && slot.active && !slot.revoking && slot.connection != nil {
			slot.connection.PresentDesiredState()
		}
	case serverClientRotated:
		gateway.revoke(event.ClientID, "rotated", false)
	case serverClientDeleted:
		gateway.revoke(event.ClientID, "deleted", true)
	}
}

func (gateway *ServerAgentGateway) revoke(clientID, reason string, deleted bool) {
	gateway.mu.Lock()
	slot, found := gateway.slots[clientID]
	if !found {
		if deleted {
			delete(gateway.runtime, clientID)
		}
		gateway.mu.Unlock()
		return
	}
	if deleted {
		delete(gateway.slots, clientID)
		delete(gateway.runtime, clientID)
		gateway.mu.Unlock()
		if slot.connection != nil {
			slot.connection.Revoke(reason)
		}
		gateway.agentRevoked(reason)
		return
	}
	if !slot.active || slot.connection == nil {
		delete(gateway.slots, clientID)
		gateway.mu.Unlock()
		return
	}
	slot.revoking = true
	gateway.slots[clientID] = slot
	gateway.mu.Unlock()
	slot.connection.Revoke(reason)
	gateway.agentRevoked(reason)
}
