package server

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flynn/noise"
	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

const nodeManagementVersion = 1
const nodeManagementPrologue = "ycy/tunnel-node-management/1"
const nodeManagementResponseLimit = 96 << 10
const nodeSnapshotLimit = 2 << 20
const nodeSnapshotChunkLimit = 32 << 10

type nodeManagementPeer struct {
	PublicKey   []byte
	Fingerprint string
}

type nodeManagementWire struct {
	http *http.Client
}

type nodeManagementSession struct {
	wire     *nodeManagementWire
	address  string
	id       string
	nodeID   string
	toNode   *noise.CipherState
	fromNode *noise.CipherState
}

type nodeSecureMessage struct {
	Version   int             `json:"version"`
	SessionID string          `json:"sessionId"`
	NodeID    string          `json:"nodeId"`
	Operation string          `json:"operation"`
	RequestID string          `json:"requestId"`
	Body      json.RawMessage `json:"body,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type nodeStatus struct {
	Claimed                 bool   `json:"claimed"`
	HighestAcceptedRevision int64  `json:"highestAcceptedRevision"`
	SHA256                  string `json:"sha256"`
	AppliedRevision         int64  `json:"appliedRevision"`
	FailedRevision          int64  `json:"failedRevision"`
	Phase                   string `json:"phase"`
	FailureCode             string `json:"failureCode"`
	RecoveryError           string `json:"recoveryError"`
	BootDisabled            bool   `json:"bootDisabled"`
	DisabledComplete        bool   `json:"disabledComplete"`
	FRPSProcess             string `json:"frpsProcess"`
	FRPSPID                 *int   `json:"frpsPID"`
	ObservedAt              string `json:"observedAt"`
}

type nodeApplyResult struct {
	HighestAcceptedRevision int64  `json:"highestAcceptedRevision"`
	SHA256                  string `json:"sha256"`
	AppliedRevision         int64  `json:"appliedRevision"`
	FailedRevision          int64  `json:"failedRevision"`
	Phase                   string `json:"phase"`
	FailureCode             string `json:"failureCode"`
}

func newNodeManagementWire() *nodeManagementWire {
	return &nodeManagementWire{http: &http.Client{Timeout: 10 * time.Second}}
}

func (wire *nodeManagementWire) preview(ctx context.Context, address string) (nodeManagementPeer, error) {
	address, err := normalizeNodeManagementAddress(address)
	if err != nil {
		return nodeManagementPeer{}, err
	}
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Random:      rand.Reader, Pattern: noise.HandshakeXX, Initiator: true,
		Prologue: []byte(nodeManagementPrologue),
	})
	if err != nil {
		return nodeManagementPeer{}, err
	}
	m1, _, _, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		return nodeManagementPeer{}, err
	}
	var response struct {
		HandshakeID string `json:"handshakeId"`
		M2          string `json:"m2"`
	}
	if err := wire.post(ctx, address, "/node/noise/start", map[string]any{
		"version": nodeManagementVersion,
		"m1":      base64.RawURLEncoding.EncodeToString(m1),
	}, &response); err != nil {
		return nodeManagementPeer{}, err
	}
	if response.HandshakeID == "" || response.M2 == "" {
		return nodeManagementPeer{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node management handshake is invalid")
	}
	m2, err := base64.RawURLEncoding.DecodeString(response.M2)
	if err != nil || len(m2) > noise.MaxMsgLen {
		return nodeManagementPeer{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node management handshake is invalid")
	}
	payload, _, _, err := handshake.ReadMessage(nil, m2)
	if err != nil || len(payload) != 0 || len(handshake.PeerStatic()) != 32 {
		return nodeManagementPeer{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node management handshake is invalid")
	}
	publicKey := append([]byte(nil), handshake.PeerStatic()...)
	digest := sha256.Sum256(publicKey)
	return nodeManagementPeer{PublicKey: publicKey, Fingerprint: "SHA256:" + base64.RawURLEncoding.EncodeToString(digest[:])}, nil
}

func (wire *nodeManagementWire) status(ctx context.Context, address string, privateKey, expectedPublic []byte) (string, nodeStatus, error) {
	session, err := wire.open(ctx, address, privateKey, expectedPublic)
	if err != nil {
		return "", nodeStatus{}, err
	}
	response, err := session.request(ctx, "status", "", map[string]any{})
	if err != nil {
		return "", nodeStatus{}, err
	}
	var status nodeStatus
	if err := json.Unmarshal(response.Body, &status); err != nil || !status.Claimed || status.ObservedAt == "" {
		return "", nodeStatus{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node status response is invalid")
	}
	return session.nodeID, status, nil
}

func (wire *nodeManagementWire) claim(ctx context.Context, address string, privateKey, expectedPublic []byte) (string, error) {
	session, err := wire.open(ctx, address, privateKey, expectedPublic)
	if err != nil {
		return "", err
	}
	requestBytes := make([]byte, 16)
	if _, err := rand.Read(requestBytes); err != nil {
		return "", err
	}
	response, err := session.request(ctx, "claim", hex.EncodeToString(requestBytes), map[string]any{})
	if err != nil {
		var domain *ServerDomainError
		if errors.As(err, &domain) && domain.Code == "NODE_UNREACHABLE" {
			return "", serverDomainError("NODE_CLAIM_OUTCOME_UNKNOWN", "Node claim outcome is unknown; preview and explicitly re-add this Node")
		}
		return "", err
	}
	var result struct {
		Claimed bool `json:"claimed"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil || !result.Claimed {
		return "", serverDomainError("NODE_CLAIM_OUTCOME_UNKNOWN", "Node claim outcome is unknown; preview and explicitly re-add this Node")
	}
	return session.nodeID, nil
}

func (wire *nodeManagementWire) applySnapshot(ctx context.Context, address string, privateKey, expectedPublic []byte, nodeID string, revision int64, snapshot []byte) (nodeApplyResult, error) {
	if nodeID == "" || revision < 1 || len(snapshot) == 0 || len(snapshot) > nodeSnapshotLimit {
		return nodeApplyResult{}, serverDomainError("NODE_SNAPSHOT_INVALID", "Node snapshot is invalid")
	}
	session, err := wire.open(ctx, address, privateKey, expectedPublic)
	if err != nil {
		return nodeApplyResult{}, err
	}
	if session.nodeID != nodeID {
		return nodeApplyResult{}, serverDomainError("NODE_IDENTITY_MISMATCH", "Node ID differs from the registered identity")
	}
	requestBytes := make([]byte, 16)
	if _, err := rand.Read(requestBytes); err != nil {
		return nodeApplyResult{}, err
	}
	requestID := hex.EncodeToString(requestBytes)
	digest := sha256.Sum256(snapshot)
	sha := hex.EncodeToString(digest[:])
	_, err = session.request(ctx, "applySnapshot", requestID, map[string]any{
		"phase": "begin", "revision": revision, "nodeId": nodeID,
		"totalBytes": len(snapshot), "sha256": sha, "formatVersion": 1,
		"frpVersion": tunnelruntime.FRPVersion,
	})
	if err != nil {
		return nodeApplyResult{}, err
	}
	for offset := 0; offset < len(snapshot); offset += nodeSnapshotChunkLimit {
		end := min(offset+nodeSnapshotChunkLimit, len(snapshot))
		_, err := session.request(ctx, "applySnapshot", requestID, map[string]any{
			"phase": "chunk", "offset": offset,
			"bytes": base64.RawURLEncoding.EncodeToString(snapshot[offset:end]),
		})
		if err != nil {
			return nodeApplyResult{}, err
		}
	}
	response, err := session.request(ctx, "applySnapshot", requestID, map[string]any{
		"phase": "commit", "revision": revision, "sha256": sha,
	})
	var result nodeApplyResult
	if len(response.Body) > 0 {
		if decodeErr := json.Unmarshal(response.Body, &result); decodeErr != nil {
			return nodeApplyResult{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node apply result is invalid")
		}
	}
	if err != nil {
		return result, err
	}
	if result.HighestAcceptedRevision != revision || result.SHA256 != sha {
		return nodeApplyResult{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node apply result does not match the submitted snapshot")
	}
	return result, nil
}

func (wire *nodeManagementWire) open(ctx context.Context, address string, privateKey, expectedPublic []byte) (*nodeManagementSession, error) {
	address, err := normalizeNodeManagementAddress(address)
	if err != nil {
		return nil, err
	}
	key, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil || len(expectedPublic) != 32 {
		return nil, serverDomainError("NODE_IDENTITY_MISMATCH", "Node or Controller identity is invalid")
	}
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Random:      rand.Reader, Pattern: noise.HandshakeXX, Initiator: true,
		Prologue:      []byte(nodeManagementPrologue),
		StaticKeypair: noise.DHKey{Private: privateKey, Public: key.PublicKey().Bytes()},
	})
	if err != nil {
		return nil, err
	}
	m1, _, _, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	var start struct {
		HandshakeID string `json:"handshakeId"`
		M2          string `json:"m2"`
	}
	if err := wire.post(ctx, address, "/node/noise/start", map[string]any{"version": nodeManagementVersion, "m1": base64.RawURLEncoding.EncodeToString(m1)}, &start); err != nil {
		return nil, err
	}
	m2, err := base64.RawURLEncoding.DecodeString(start.M2)
	if err != nil || len(m2) > noise.MaxMsgLen || start.HandshakeID == "" {
		return nil, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node management handshake is invalid")
	}
	payload, _, _, err := handshake.ReadMessage(nil, m2)
	if err != nil || len(payload) != 0 || !bytes.Equal(handshake.PeerStatic(), expectedPublic) {
		return nil, serverDomainError("NODE_IDENTITY_MISMATCH", "Node identity differs from the confirmed fingerprint")
	}
	m3, toNode, fromNode, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	var finish struct {
		SessionID  string `json:"sessionId"`
		Seq        uint64 `json:"seq"`
		Ciphertext string `json:"ciphertext"`
	}
	if err := wire.post(ctx, address, "/node/noise/finish", map[string]any{"handshakeId": start.HandshakeID, "m3": base64.RawURLEncoding.EncodeToString(m3)}, &finish); err != nil {
		return nil, err
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(finish.Ciphertext)
	if err != nil || finish.Seq != 0 || finish.SessionID == "" || len(ciphertext) > noise.MaxMsgLen {
		return nil, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node confirmation is invalid")
	}
	plaintext, err := fromNode.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return nil, serverDomainError("NODE_IDENTITY_MISMATCH", "Node confirmation could not be authenticated")
	}
	var confirmation nodeSecureMessage
	if err := json.Unmarshal(plaintext, &confirmation); err != nil || confirmation.Version != nodeManagementVersion || confirmation.SessionID != finish.SessionID || confirmation.Operation != "confirm" || confirmation.NodeID == "" {
		return nil, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node confirmation is invalid")
	}
	return &nodeManagementSession{wire: wire, address: address, id: finish.SessionID, nodeID: confirmation.NodeID, toNode: toNode, fromNode: fromNode}, nil
}

func (session *nodeManagementSession) request(ctx context.Context, operation, requestID string, body any) (nodeSecureMessage, error) {
	if requestID == "" {
		requestID = "status"
	}
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return nodeSecureMessage{}, err
	}
	message := nodeSecureMessage{Version: nodeManagementVersion, SessionID: session.id, NodeID: session.nodeID, Operation: operation, RequestID: requestID, Body: encodedBody}
	plaintext, err := json.Marshal(message)
	if err != nil {
		return nodeSecureMessage{}, err
	}
	seq := session.toNode.Nonce()
	ciphertext, err := session.toNode.Encrypt(nil, nil, plaintext)
	if err != nil {
		return nodeSecureMessage{}, err
	}
	var frame struct {
		SessionID  string `json:"sessionId"`
		Seq        uint64 `json:"seq"`
		Ciphertext string `json:"ciphertext"`
	}
	if err := session.wire.post(ctx, session.address, "/node/noise/message", map[string]any{"sessionId": session.id, "seq": seq, "ciphertext": base64.RawURLEncoding.EncodeToString(ciphertext)}, &frame); err != nil {
		return nodeSecureMessage{}, err
	}
	ciphertext, err = base64.RawURLEncoding.DecodeString(frame.Ciphertext)
	if err != nil || frame.SessionID != session.id || frame.Seq != session.fromNode.Nonce() || len(ciphertext) > noise.MaxMsgLen {
		return nodeSecureMessage{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node encrypted response is invalid")
	}
	plaintext, err = session.fromNode.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return nodeSecureMessage{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node encrypted response is invalid")
	}
	var response nodeSecureMessage
	if err := json.Unmarshal(plaintext, &response); err != nil || response.Version != nodeManagementVersion || response.SessionID != session.id || response.NodeID != session.nodeID || response.Operation != operation || response.RequestID != requestID {
		return nodeSecureMessage{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node encrypted response is invalid")
	}
	if response.Error != "" {
		return response, nodeManagementCodeError(response.Error)
	}
	return response, nil
}

func nodeManagementCodeError(code string) error {
	switch code {
	case "NODE_ALREADY_CLAIMED", "NODE_IDENTITY_MISMATCH", "NODE_REVISION_STALE", "NODE_REVISION_CONFLICT", "NODE_SNAPSHOT_TOO_LARGE", "NODE_SNAPSHOT_INVALID", "NODE_CONFIG_REJECTED", "NODE_APPLY_FAILED", "NODE_ROLLBACK_FAILED":
		return serverDomainError(code, "Node management operation failed")
	default:
		return serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node management response is invalid")
	}
}

func normalizeNodeManagementAddress(input string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(input))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", serverDomainError("INVALID_MANAGEMENT_ADDRESS", "Node management address must be an HTTP URL without credentials, path, query or fragment")
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func (wire *nodeManagementWire) post(ctx context.Context, address, path string, input, output any) error {
	contents, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+path, bytes.NewReader(contents))
	if err != nil {
		return serverDomainError("INVALID_MANAGEMENT_ADDRESS", "Node management address is invalid")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := wire.http.Do(request)
	if err != nil {
		return serverDomainError("NODE_UNREACHABLE", "Node management endpoint is unreachable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, nodeManagementResponseLimit+1))
	if err != nil || len(body) > nodeManagementResponseLimit {
		return serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node management response is invalid")
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &failure) == nil && failure.Error == "NODE_PROTOCOL_INCOMPATIBLE" {
			return serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node management protocol is incompatible")
		}
		return serverDomainError("NODE_UNREACHABLE", "Node management request failed")
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode Node management response: %w", err)
	}
	return nil
}
