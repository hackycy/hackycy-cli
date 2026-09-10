package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"io"
	"math"
	"strings"
)

const (
	serverAgentCloseInvalidMessage  = 4400
	serverAgentCloseRevoked         = 4401
	serverAgentCloseIncompatible    = 4406
	serverAgentCloseLivenessTimeout = 4408
	serverAgentCloseFRPSUnavailable = 4503

	serverAgentMaximumSafeInteger = 9007199254740991
)

// ServerAgentProtocolError retains the close semantics that a v3 peer sees
// without coupling the protocol validator to a WebSocket implementation.
type ServerAgentProtocolError struct {
	CloseCode int
	Message   string
}

func (err *ServerAgentProtocolError) Error() string { return err.Message }

// AcceptHello validates and records the first v3 message on an accepted
// connection before any active protocol frame can be handled.
func (connection *ServerAgentConnection) AcceptHello(ctx context.Context, source []byte) *ServerAgentProtocolError {
	if connection == nil || connection.gateway == nil {
		return &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server agent session is unavailable"}
	}
	connection.helloMu.Lock()
	defer connection.helloMu.Unlock()
	if connection.helloAccepted {
		return &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Unexpected agent message"}
	}
	hello, protocolError := decodeServerAgentHello(source)
	if protocolError != nil {
		return protocolError
	}
	if connection.gateway.frps.FRPSState().State != tunnelruntime.FRPProcessRunning {
		return &ServerAgentProtocolError{CloseCode: serverAgentCloseFRPSUnavailable, Message: "Managed frps is not running"}
	}
	if hello.TunnelProtocolVersion != tunnelruntime.TunnelProtocolVersion {
		return &ServerAgentProtocolError{
			CloseCode: serverAgentCloseIncompatible,
			Message:   fmt.Sprintf("Client tunnel protocol %d is incompatible; upgrade ycy", hello.TunnelProtocolVersion),
		}
	}
	if _, err := tunnelruntime.ResolveFRPArtifact(tunnelruntime.WireTarget{Platform: tunnelruntime.WirePlatform(hello.Platform), Architecture: tunnelruntime.WireArchitecture(hello.Architecture)}); err != nil {
		return &ServerAgentProtocolError{
			CloseCode: serverAgentCloseIncompatible,
			Message:   "FRP " + tunnelruntime.FRPVersion + " is unavailable for " + hello.Platform + "/" + hello.Architecture + "; upgrade ycy or use a supported platform",
		}
	}
	client, err := connection.gateway.controlPlane.GetClient(ctx, connection.clientID)
	if err != nil {
		var domainError *ServerDomainError
		if errors.As(err, &domainError) && domainError.Code == "NOT_FOUND" {
			return &ServerAgentProtocolError{CloseCode: serverAgentCloseRevoked, Message: "Client Token revoked"}
		}
		return &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server control plane is unavailable"}
	}
	if hello.LastAppliedRevision > client.DesiredRevision {
		return &ServerAgentProtocolError{
			CloseCode: serverAgentCloseIncompatible,
			Message:   "Client Applied Revision exceeds the control plane Desired Revision; inspect or upgrade the client",
		}
	}
	connection.helloAccepted = true
	connection.hello = hello
	connection.gateway.agentConnected(connection.clientID)
	return nil
}

// AcceptApplyResult records a successful durable acknowledgement or projects
// a failed acknowledgement into the process-local runtime state.
func (connection *ServerAgentConnection) AcceptApplyResult(ctx context.Context, source []byte) *ServerAgentProtocolError {
	if connection == nil || connection.gateway == nil {
		return &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server agent session is unavailable"}
	}
	if !connection.acceptedHello() {
		return &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "A valid hello message is required"}
	}
	result, protocolError := decodeServerAgentApplyResult(source)
	if protocolError != nil {
		return protocolError
	}
	return connection.recordApplyResult(ctx, result)
}

// AcceptProcessState stores the latest process-local agent report without
// changing durable desired or applied revisions.
func (connection *ServerAgentConnection) AcceptProcessState(ctx context.Context, source []byte) *ServerAgentProtocolError {
	if connection == nil || connection.gateway == nil {
		return &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server agent session is unavailable"}
	}
	if !connection.acceptedHello() {
		return &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "A valid hello message is required"}
	}
	state, protocolError := decodeServerAgentProcessState(source)
	if protocolError != nil {
		return protocolError
	}
	return connection.recordProcessState(state)
}

// AcceptActiveMessage dispatches only messages that may follow a successful
// hello/welcome exchange without making the HTTP adapter own protocol parsing.
func (connection *ServerAgentConnection) AcceptActiveMessage(ctx context.Context, source []byte) *ServerAgentProtocolError {
	if connection == nil || connection.gateway == nil {
		return &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server agent session is unavailable"}
	}
	if !connection.acceptedHello() {
		return &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "A valid hello message is required"}
	}
	value, messageType, protocolError := decodeServerAgentActiveMessage(source)
	if protocolError != nil {
		return protocolError
	}
	switch messageType {
	case "apply_result":
		result, protocolError := decodeServerAgentApplyResultValue(value)
		if protocolError != nil {
			return protocolError
		}
		return connection.recordApplyResult(ctx, result)
	case "process_state":
		state, protocolError := decodeServerAgentProcessStateValue(value)
		if protocolError != nil {
			return protocolError
		}
		return connection.recordProcessState(state)
	default:
		return &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Unexpected agent message"}
	}
}

func (connection *ServerAgentConnection) acceptedHello() bool {
	connection.helloMu.Lock()
	defer connection.helloMu.Unlock()
	return connection.helloAccepted
}

func (connection *ServerAgentConnection) recordApplyResult(ctx context.Context, result tunnelruntime.ApplyResult) *ServerAgentProtocolError {
	if result.Success {
		if err := connection.gateway.controlPlane.RecordAppliedRevision(ctx, connection.clientID, result.Revision); err != nil {
			return &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Invalid Applied Revision"}
		}
		accepted, changed, recovered := connection.gateway.recordApplyResult(connection.clientID, connection.slot, result)
		if !accepted {
			return &ServerAgentProtocolError{CloseCode: serverAgentCloseRevoked, Message: "Client Token revoked"}
		}
		connection.gateway.logApplyResult(result, changed, recovered)
		return nil
	}
	lastError := result.Error
	if lastError == nil {
		revision := result.Revision
		lastError = &tunnelruntime.StructuredRuntimeError{
			Code:     "APPLY_FAILED",
			Message:  "Client could not apply Desired Revision",
			Revision: &revision,
		}
	}
	result.Error = lastError
	accepted, changed, _ := connection.gateway.recordApplyResult(connection.clientID, connection.slot, result)
	if !accepted {
		return &ServerAgentProtocolError{CloseCode: serverAgentCloseRevoked, Message: "Client Token revoked"}
	}
	connection.gateway.logApplyResult(result, changed, false)
	return nil
}

func (connection *ServerAgentConnection) recordProcessState(state tunnelruntime.ProcessState) *ServerAgentProtocolError {
	accepted, changed := connection.gateway.recordProcessStateWithChange(connection.clientID, connection.slot, state.State, state.Error)
	if !accepted {
		return &ServerAgentProtocolError{CloseCode: serverAgentCloseRevoked, Message: "Client Token revoked"}
	}
	if changed {
		connection.gateway.logProcessState(state.State)
	}
	return nil
}

// BuildWelcome composes the one successful hello response. The HTTP adapter
// owns writing the frame, and later slices own subsequent server messages.
func (connection *ServerAgentConnection) BuildWelcome(ctx context.Context, requestHost string) (tunnelruntime.AgentWelcome, *ServerAgentProtocolError) {
	if connection == nil || connection.gateway == nil {
		return tunnelruntime.AgentWelcome{}, &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server agent session is unavailable"}
	}
	connection.helloMu.Lock()
	if !connection.helloAccepted {
		connection.helloMu.Unlock()
		return tunnelruntime.AgentWelcome{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "A valid hello message is required"}
	}
	hello := connection.hello
	welcomeSource := connection.gateway.welcomeSource
	connection.helloMu.Unlock()
	if connection.gateway.frps.FRPSState().State != tunnelruntime.FRPProcessRunning {
		return tunnelruntime.AgentWelcome{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseFRPSUnavailable, Message: "Managed frps is not running"}
	}
	if welcomeSource == nil {
		return tunnelruntime.AgentWelcome{}, &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server welcome configuration is unavailable"}
	}
	settings := welcomeSource.AgentWelcomeSettings(requestHost)
	if strings.TrimSpace(settings.AdvertisedFRPHost) == "" || settings.AdvertisedFRPPort < 1 || settings.AdvertisedFRPPort > 65535 || strings.TrimSpace(settings.InternalFRPToken) == "" {
		return tunnelruntime.AgentWelcome{}, &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server welcome configuration is unavailable"}
	}
	artifact, err := tunnelruntime.ResolveFRPArtifact(tunnelruntime.WireTarget{Platform: tunnelruntime.WirePlatform(hello.Platform), Architecture: tunnelruntime.WireArchitecture(hello.Architecture)})
	if err != nil {
		return tunnelruntime.AgentWelcome{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseIncompatible, Message: "Client platform is incompatible"}
	}
	snapshot, err := connection.gateway.controlPlane.Snapshot(ctx, connection.clientID)
	if err != nil {
		var domainError *ServerDomainError
		if errors.As(err, &domainError) && domainError.Code == "NOT_FOUND" {
			return tunnelruntime.AgentWelcome{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseRevoked, Message: "Client Token revoked"}
		}
		return tunnelruntime.AgentWelcome{}, &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server control plane is unavailable"}
	}
	return tunnelruntime.AgentWelcome{
		Type:                  "welcome",
		TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
		RequiredFRPVersion:    tunnelruntime.FRPVersion,
		Artifact:              artifact.Description,
		AdvertisedFRPHost:     settings.AdvertisedFRPHost,
		AdvertisedFRPPort:     settings.AdvertisedFRPPort,
		InternalFRPToken:      settings.InternalFRPToken,
		Snapshot:              snapshot,
	}, nil
}

// PresentWelcome writes the initial frame before allowing later durable
// desired-state events to write a replacement snapshot on the same connection.
func (connection *ServerAgentConnection) PresentWelcome(ctx context.Context, requestHost string, writeFrame func(any) error) *ServerAgentProtocolError {
	if connection == nil || connection.gateway == nil || writeFrame == nil {
		return &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server agent presentation is unavailable"}
	}
	connection.presentationMu.Lock()
	defer connection.presentationMu.Unlock()
	if connection.closed {
		return &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server agent session is unavailable"}
	}
	welcome, protocolError := connection.BuildWelcome(ctx, requestHost)
	if protocolError != nil {
		return protocolError
	}
	if err := writeFrame(welcome); err != nil {
		return &ServerAgentProtocolError{CloseCode: 1011, Message: "Tunnel server agent presentation is unavailable"}
	}
	connection.writeFrame = writeFrame
	connection.presentationActive = true
	return nil
}

// PresentDesiredState writes only later committed desired-state replacements.
// Events before welcome are already included in its durable snapshot.
func (connection *ServerAgentConnection) PresentDesiredState() {
	if connection == nil || connection.gateway == nil {
		return
	}
	connection.presentationMu.Lock()
	defer connection.presentationMu.Unlock()
	if connection.closed || !connection.presentationActive || connection.writeFrame == nil {
		return
	}
	snapshot, err := connection.gateway.controlPlane.Snapshot(context.Background(), connection.clientID)
	if err != nil {
		return
	}
	_ = connection.writeFrame(tunnelruntime.DesiredState{
		Type:                  "desired_state",
		TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
		Snapshot:              snapshot,
	})
}

// RestartFRPC sends the imperative restart frame without changing durable
// desired or applied state.
func (connection *ServerAgentConnection) RestartFRPC() bool {
	if connection == nil || connection.gateway == nil {
		return false
	}
	connection.presentationMu.Lock()
	defer connection.presentationMu.Unlock()
	if connection.closed || !connection.presentationActive || connection.writeFrame == nil {
		return false
	}
	return connection.writeFrame(tunnelruntime.RestartFRPC{
		Type:                  "restart_frpc",
		TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
	}) == nil
}

// Revoke ends the active token's session after delivering its final server
// frame. A socket that has not completed welcome still closes with 4401.
func (connection *ServerAgentConnection) Revoke(reason string) {
	if connection == nil {
		return
	}
	connection.presentationMu.Lock()
	if connection.closed {
		connection.presentationMu.Unlock()
		return
	}
	connection.revoked = true
	if connection.presentationActive && connection.writeFrame != nil {
		_ = connection.writeFrame(tunnelruntime.Revoke{
			Type:                  "revoke",
			TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
			Reason:                reason,
		})
	}
	connection.presentationActive = false
	connection.writeFrame = nil
	closeSocket := connection.closeSocket
	connection.presentationMu.Unlock()
	if closeSocket != nil {
		closeSocket(&ServerAgentProtocolError{CloseCode: serverAgentCloseRevoked, Message: "Client Token revoked"})
	}
}

func decodeServerAgentHello(source []byte) (tunnelruntime.AgentHello, *ServerAgentProtocolError) {
	value, protocolError := decodeServerAgentObject(source)
	if protocolError != nil {
		return tunnelruntime.AgentHello{}, protocolError
	}
	messageType, validType := serverAgentHelloString(value, "type")
	ycyVersion, validVersion := serverAgentHelloString(value, "ycyVersion")
	platform, validPlatform := serverAgentHelloString(value, "platform")
	architecture, validArchitecture := serverAgentHelloString(value, "architecture")
	protocolVersion, validProtocolVersion := serverAgentSafeInteger(value["tunnelProtocolVersion"])
	lastAppliedRevision, validRevision := serverAgentSafeInteger(value["lastAppliedRevision"])
	if !validType || messageType != "hello" || !validVersion || !validPlatform || !validArchitecture || !validProtocolVersion || !validRevision || lastAppliedRevision < 0 || protocolVersion != int64(int(protocolVersion)) {
		return tunnelruntime.AgentHello{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "A valid hello message is required"}
	}
	return tunnelruntime.AgentHello{
		Type:                  messageType,
		TunnelProtocolVersion: int(protocolVersion),
		YCYVersion:            ycyVersion,
		Platform:              platform,
		Architecture:          architecture,
		LastAppliedRevision:   lastAppliedRevision,
	}, nil
}

func decodeServerAgentApplyResult(source []byte) (tunnelruntime.ApplyResult, *ServerAgentProtocolError) {
	value, messageType, protocolError := decodeServerAgentActiveMessage(source)
	if protocolError != nil {
		return tunnelruntime.ApplyResult{}, protocolError
	}
	if messageType != "apply_result" {
		return tunnelruntime.ApplyResult{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Unexpected agent message"}
	}
	return decodeServerAgentApplyResultValue(value)
}

func decodeServerAgentApplyResultValue(value map[string]any) (tunnelruntime.ApplyResult, *ServerAgentProtocolError) {
	revision, validRevision := serverAgentSafeInteger(value["revision"])
	success, validSuccess := value["success"].(bool)
	if !validRevision || revision < 0 || !validSuccess {
		return tunnelruntime.ApplyResult{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Invalid apply result"}
	}
	lastError, validError := decodeServerAgentStructuredRuntimeError(value)
	if !validError {
		return tunnelruntime.ApplyResult{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Invalid apply result"}
	}
	return tunnelruntime.ApplyResult{
		Type:                  "apply_result",
		TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
		Revision:              revision,
		Success:               success,
		Error:                 lastError,
	}, nil
}

func decodeServerAgentProcessState(source []byte) (tunnelruntime.ProcessState, *ServerAgentProtocolError) {
	value, messageType, protocolError := decodeServerAgentActiveMessage(source)
	if protocolError != nil {
		return tunnelruntime.ProcessState{}, protocolError
	}
	if messageType != "process_state" {
		return tunnelruntime.ProcessState{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Unexpected agent message"}
	}
	return decodeServerAgentProcessStateValue(value)
}

func decodeServerAgentProcessStateValue(value map[string]any) (tunnelruntime.ProcessState, *ServerAgentProtocolError) {
	state, validState := value["state"].(string)
	if !validState || (state != string(tunnelruntime.FRPProcessStopped) && state != string(tunnelruntime.FRPProcessRunning) && state != string(tunnelruntime.FRPProcessRecovering) && state != string(tunnelruntime.FRPProcessConfigurationFailed)) {
		return tunnelruntime.ProcessState{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Invalid process state"}
	}
	lastError, validError := decodeServerAgentStructuredRuntimeError(value)
	if !validError {
		return tunnelruntime.ProcessState{}, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Invalid process state"}
	}
	return tunnelruntime.ProcessState{
		Type:                  "process_state",
		TunnelProtocolVersion: tunnelruntime.TunnelProtocolVersion,
		State:                 tunnelruntime.FRPProcessState(state),
		Error:                 lastError,
	}, nil
}

func decodeServerAgentStructuredRuntimeError(value map[string]any) (*tunnelruntime.StructuredRuntimeError, bool) {
	raw, found := value["error"]
	if !found || raw == nil {
		return nil, true
	}
	fields, valid := raw.(map[string]any)
	if !valid {
		return nil, false
	}
	code, validCode := fields["code"].(string)
	message, validMessage := fields["message"].(string)
	if !validCode || !validMessage {
		return nil, false
	}
	result := &tunnelruntime.StructuredRuntimeError{Code: code, Message: message}
	if rawRevision, found := fields["revision"]; found {
		revision, validRevision := serverAgentSafeInteger(rawRevision)
		if !validRevision || revision < 0 {
			return nil, false
		}
		result.Revision = &revision
	}
	return result, true
}

func decodeServerAgentActiveMessage(source []byte) (map[string]any, string, *ServerAgentProtocolError) {
	value, protocolError := decodeServerAgentObject(source)
	if protocolError != nil {
		return nil, "", protocolError
	}
	protocolVersion, validProtocolVersion := serverAgentSafeInteger(value["tunnelProtocolVersion"])
	if !validProtocolVersion || protocolVersion != int64(tunnelruntime.TunnelProtocolVersion) {
		return nil, "", &ServerAgentProtocolError{CloseCode: serverAgentCloseIncompatible, Message: "Unsupported tunnel protocol version"}
	}
	messageType, validType := serverAgentHelloString(value, "type")
	if !validType {
		return nil, "", &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Unexpected agent message"}
	}
	return value, messageType, nil
}

func decodeServerAgentObject(source []byte) (map[string]any, *ServerAgentProtocolError) {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Invalid JSON message"}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, &ServerAgentProtocolError{CloseCode: serverAgentCloseInvalidMessage, Message: "Invalid JSON message"}
	}
	return value, nil
}

func serverAgentHelloString(value map[string]any, name string) (string, bool) {
	field, found := value[name]
	text, valid := field.(string)
	return text, found && valid
}

func serverAgentSafeInteger(value any) (int64, bool) {
	number, valid := value.(json.Number)
	if !valid {
		return 0, false
	}
	parsed, err := number.Float64()
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || math.Trunc(parsed) != parsed || parsed < -serverAgentMaximumSafeInteger || parsed > serverAgentMaximumSafeInteger {
		return 0, false
	}
	return int64(parsed), true
}
