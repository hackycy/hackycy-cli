package node

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/flynn/noise"
)

const managementVersion = 1
const managementPrologue = "ycy/tunnel-node-management/1"
const maximumHTTPBody = 96 << 10
const handshakeLifetime = 15 * time.Second
const sessionIdleLifetime = 60 * time.Second
const sessionMaximumLifetime = 10 * time.Minute

type handshakeEntry struct {
	state   *noise.HandshakeState
	created time.Time
}

type managementSession struct {
	toNode   *noise.CipherState
	fromNode *noise.CipherState
	peer     []byte
	created  time.Time
	lastUsed time.Time
	transfer *snapshotTransfer
}

type snapshotTransfer struct {
	requestID string
	revision  int64
	total     int64
	digest    string
	file      *os.File
	received  int64
}

type snapshotFrame struct {
	Phase         string `json:"phase"`
	Revision      int64  `json:"revision,omitempty"`
	NodeID        string `json:"nodeId,omitempty"`
	TotalBytes    int64  `json:"totalBytes,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	FormatVersion int    `json:"formatVersion,omitempty"`
	FRPVersion    string `json:"frpVersion,omitempty"`
	Offset        int64  `json:"offset,omitempty"`
	Bytes         string `json:"bytes,omitempty"`
}

type managementHandler struct {
	state      *State
	runtime    *nodeRuntime
	mu         sync.Mutex
	handshakes map[string]handshakeEntry
	sessions   map[string]*managementSession
	now        func() time.Time
	routes     http.Handler
}

type startFrame struct {
	Version int    `json:"version"`
	M1      string `json:"m1"`
}

type finishFrame struct {
	HandshakeID string `json:"handshakeId"`
	M3          string `json:"m3"`
}

type messageFrame struct {
	SessionID  string `json:"sessionId"`
	Seq        uint64 `json:"seq"`
	Ciphertext string `json:"ciphertext"`
}

type secureMessage struct {
	Version   int             `json:"version"`
	SessionID string          `json:"sessionId"`
	NodeID    string          `json:"nodeId"`
	Operation string          `json:"operation"`
	RequestID string          `json:"requestId"`
	Body      json.RawMessage `json:"body,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func newManagementHandler(state *State) *managementHandler {
	handler := &managementHandler{state: state, handshakes: make(map[string]handshakeEntry), sessions: make(map[string]*managementSession), now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"status": "ok", "protocolVersion": managementVersion})
	})
	mux.HandleFunc("POST /node/noise/start", handler.start)
	mux.HandleFunc("POST /node/noise/finish", handler.finish)
	mux.HandleFunc("POST /node/noise/message", handler.message)
	handler.routes = mux
	return handler
}

func (handler *managementHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost {
		request.Body = http.MaxBytesReader(writer, request.Body, maximumHTTPBody)
	}
	handler.routes.ServeHTTP(writer, request)
}

func (handler *managementHandler) start(writer http.ResponseWriter, request *http.Request) {
	var frame startFrame
	if err := readJSON(request, &frame); err != nil {
		writeWireError(writer, err)
		return
	}
	if frame.Version != managementVersion {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "NODE_PROTOCOL_INCOMPATIBLE"})
		return
	}
	m1, err := decodeNoise(frame.M1)
	if err != nil {
		writeWireError(writer, err)
		return
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expire()
	if len(handler.handshakes) >= 64 {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "BUSY"})
		return
	}
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Random:      rand.Reader, Pattern: noise.HandshakeXX, Initiator: false,
		Prologue: []byte(managementPrologue), StaticKeypair: noise.DHKey{Private: handler.state.private, Public: handler.state.public},
	})
	if err != nil {
		writeWireError(writer, err)
		return
	}
	payload, _, _, err := handshake.ReadMessage(nil, m1)
	if err != nil || len(payload) != 0 {
		writeWireError(writer, err)
		return
	}
	m2, _, _, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		writeWireError(writer, err)
		return
	}
	id, err := randomHandle()
	if err != nil {
		writeWireError(writer, err)
		return
	}
	handler.handshakes[id] = handshakeEntry{state: handshake, created: handler.now()}
	writeJSON(writer, http.StatusOK, map[string]any{"handshakeId": id, "m2": base64.RawURLEncoding.EncodeToString(m2)})
}

func (handler *managementHandler) finish(writer http.ResponseWriter, request *http.Request) {
	var frame finishFrame
	if err := readJSON(request, &frame); err != nil {
		writeWireError(writer, err)
		return
	}
	m3, err := decodeNoise(frame.M3)
	if err != nil {
		writeWireError(writer, err)
		return
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expire()
	entry, ok := handler.handshakes[frame.HandshakeID]
	if !ok {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "BAD_FRAME"})
		return
	}
	if len(handler.sessions) >= 32 {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "BUSY"})
		return
	}
	delete(handler.handshakes, frame.HandshakeID)
	payload, toNode, fromNode, err := entry.state.ReadMessage(nil, m3)
	if err != nil || len(payload) != 0 || toNode == nil || fromNode == nil || len(entry.state.PeerStatic()) != 32 {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "BAD_FRAME"})
		return
	}
	id, err := randomHandle()
	if err != nil {
		writeWireError(writer, err)
		return
	}
	confirmation, err := json.Marshal(secureMessage{Version: managementVersion, SessionID: id, NodeID: handler.state.nodeID, Operation: "confirm"})
	if err != nil {
		writeWireError(writer, err)
		return
	}
	ciphertext, err := fromNode.Encrypt(nil, nil, confirmation)
	if err != nil {
		writeWireError(writer, err)
		return
	}
	handler.sessions[id] = &managementSession{toNode: toNode, fromNode: fromNode, peer: append([]byte(nil), entry.state.PeerStatic()...), created: handler.now(), lastUsed: handler.now()}
	writeJSON(writer, http.StatusOK, map[string]any{"sessionId": id, "seq": 0, "ciphertext": base64.RawURLEncoding.EncodeToString(ciphertext)})
}

func (handler *managementHandler) message(writer http.ResponseWriter, request *http.Request) {
	var frame messageFrame
	if err := readJSON(request, &frame); err != nil {
		writeWireError(writer, err)
		return
	}
	ciphertext, err := decodeNoise(frame.Ciphertext)
	if err != nil {
		writeWireError(writer, err)
		return
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.expire()
	session, ok := handler.sessions[frame.SessionID]
	if !ok || frame.Seq != session.toNode.Nonce() {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "BAD_FRAME"})
		return
	}
	plaintext, err := session.toNode.Decrypt(nil, nil, ciphertext)
	if err != nil {
		session.closeTransfer()
		delete(handler.sessions, frame.SessionID)
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "BAD_FRAME"})
		return
	}
	var message secureMessage
	if err := json.Unmarshal(plaintext, &message); err != nil || message.Version != managementVersion || message.SessionID != frame.SessionID || message.NodeID != handler.state.nodeID || message.RequestID == "" || len(message.RequestID) > 128 || message.Operation == "" {
		session.closeTransfer()
		delete(handler.sessions, frame.SessionID)
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "BAD_FRAME"})
		return
	}
	session.lastUsed = handler.now()
	response := secureMessage{Version: managementVersion, SessionID: frame.SessionID, NodeID: handler.state.nodeID, Operation: message.Operation, RequestID: message.RequestID}
	keep := false
	response.Error, keep = handler.dispatch(session, message, &response)
	contents, err := json.Marshal(response)
	if err != nil {
		session.closeTransfer()
		delete(handler.sessions, frame.SessionID)
		writeWireError(writer, err)
		return
	}
	seq := session.fromNode.Nonce()
	encrypted, err := session.fromNode.Encrypt(nil, nil, contents)
	if !keep || response.Error != "" {
		delete(handler.sessions, frame.SessionID)
		session.closeTransfer()
	}
	if err != nil {
		writeWireError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, messageFrame{SessionID: frame.SessionID, Seq: seq, Ciphertext: base64.RawURLEncoding.EncodeToString(encrypted)})
}

func (handler *managementHandler) dispatch(session *managementSession, message secureMessage, response *secureMessage) (string, bool) {
	switch message.Operation {
	case "claim":
		if !bytes.Equal(bytes.TrimSpace(message.Body), []byte("{}")) {
			return "BAD_FRAME", false
		}
		claimed, err := handler.state.Claim(context.Background(), session.peer)
		if err != nil {
			return "UNAVAILABLE", false
		}
		if !claimed {
			return "NODE_ALREADY_CLAIMED", false
		}
		response.Body = json.RawMessage(`{"claimed":true}`)
		return "", false
	case "status":
		matched, err := handler.state.ControllerMatches(context.Background(), session.peer)
		if err != nil {
			return "UNAVAILABLE", false
		}
		if !matched {
			return "NODE_ALREADY_CLAIMED", false
		}
		record, err := handler.state.readRuntime(context.Background())
		if err != nil {
			return "UNAVAILABLE", false
		}
		process := "unknown"
		var processPID *int
		recoveryCode := ""
		failedRevision := int64(0)
		if record.Phase == "failed" {
			failedRevision = record.HighestRevision
		}
		if handler.runtime != nil {
			processState := handler.runtime.processState()
			process, processPID = string(processState.State), processState.PID
			recoveryCode = handler.runtime.recoveryCode()
		}
		response.Body, _ = json.Marshal(map[string]any{"claimed": true, "highestAcceptedRevision": record.HighestRevision, "sha256": record.HighestDigest, "appliedRevision": record.AppliedRevision, "failedRevision": failedRevision, "phase": record.Phase, "failureCode": record.FailureCode, "recoveryError": recoveryCode, "bootDisabled": record.BootDisabled, "disabledComplete": record.DisabledComplete, "frpsProcess": process, "frpsPID": processPID, "observedAt": handler.now().UTC().Format(time.RFC3339Nano)})
		return "", false
	case "applySnapshot":
		matched, err := handler.state.ControllerMatches(context.Background(), session.peer)
		if err != nil {
			return "UNAVAILABLE", false
		}
		if !matched {
			return "NODE_ALREADY_CLAIMED", false
		}
		return handler.applySnapshot(session, message, response)
	default:
		return "NODE_OPERATION_UNAVAILABLE", false
	}
}

func (handler *managementHandler) expire() {
	now := handler.now()
	for id, entry := range handler.handshakes {
		if now.Sub(entry.created) >= handshakeLifetime {
			delete(handler.handshakes, id)
		}
	}
	for id, session := range handler.sessions {
		if now.Sub(session.lastUsed) >= sessionIdleLifetime || now.Sub(session.created) >= sessionMaximumLifetime {
			session.closeTransfer()
			delete(handler.sessions, id)
		}
	}
}

func (session *managementSession) closeTransfer() {
	if session.transfer == nil {
		return
	}
	path := session.transfer.file.Name()
	_ = session.transfer.file.Close()
	_ = os.Remove(path)
	session.transfer = nil
}

func randomHandle() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func decodeNoise(value string) ([]byte, error) {
	if value == "" || strings.Contains(value, "=") {
		return nil, fmt.Errorf("invalid Noise frame")
	}
	bytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(bytes) > noise.MaxMsgLen {
		return nil, fmt.Errorf("invalid Noise frame")
	}
	return bytes, nil
}

func readJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func writeWireError(writer http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "BAD_FRAME"})
		return
	}
	writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "BAD_FRAME"})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
