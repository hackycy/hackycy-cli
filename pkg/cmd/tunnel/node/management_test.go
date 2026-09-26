package node

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/flynn/noise"
)

type testController struct {
	key      noise.DHKey
	client   *http.Client
	baseURL  string
	state    *noise.HandshakeState
	toNode   *noise.CipherState
	fromNode *noise.CipherState
	session  string
	nodeID   string
}

func newTestController(t *testing.T, baseURL string) *testController {
	t.Helper()
	key, err := noise.DH25519.GenerateKeypair(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &testController{key: key, client: &http.Client{}, baseURL: baseURL}
}

func (controller *testController) post(t *testing.T, path string, request any, response any) int {
	t.Helper()
	contents, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := controller.client.Post(controller.baseURL+path, "application/json", bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	if response != nil {
		if err := json.NewDecoder(result.Body).Decode(response); err != nil {
			t.Fatal(err)
		}
	}
	return result.StatusCode
}

func (controller *testController) start(t *testing.T) (string, []byte) {
	t.Helper()
	handshake, err := noise.NewHandshakeState(noise.Config{CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256), Random: rand.Reader, Pattern: noise.HandshakeXX, Initiator: true, Prologue: []byte(managementPrologue), StaticKeypair: controller.key})
	if err != nil {
		t.Fatal(err)
	}
	controller.state = handshake
	m1, _, _, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		HandshakeID string `json:"handshakeId"`
		M2          string `json:"m2"`
	}
	if status := controller.post(t, "/node/noise/start", startFrame{Version: 1, M1: base64.RawURLEncoding.EncodeToString(m1)}, &response); status != http.StatusOK {
		t.Fatalf("start status %d", status)
	}
	m2, err := base64.RawURLEncoding.DecodeString(response.M2)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := handshake.ReadMessage(nil, m2); err != nil {
		t.Fatal(err)
	}
	return response.HandshakeID, append([]byte(nil), handshake.PeerStatic()...)
}

func (controller *testController) finish(t *testing.T, handshakeID string) {
	t.Helper()
	m3, send, receive, err := controller.state.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	controller.toNode, controller.fromNode = send, receive
	var response struct {
		SessionID  string `json:"sessionId"`
		Seq        uint64 `json:"seq"`
		Ciphertext string `json:"ciphertext"`
	}
	if status := controller.post(t, "/node/noise/finish", finishFrame{HandshakeID: handshakeID, M3: base64.RawURLEncoding.EncodeToString(m3)}, &response); status != http.StatusOK {
		t.Fatalf("finish status %d", status)
	}
	if response.Seq != 0 {
		t.Fatalf("confirmation seq %d", response.Seq)
	}
	controller.session = response.SessionID
	ciphertext, err := base64.RawURLEncoding.DecodeString(response.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := receive.Decrypt(nil, nil, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	var confirm secureMessage
	if err := json.Unmarshal(plaintext, &confirm); err != nil || confirm.SessionID != response.SessionID || confirm.Operation != "confirm" {
		t.Fatalf("confirmation = (%+v, %v)", confirm, err)
	}
	controller.nodeID = confirm.NodeID
}

func (controller *testController) message(t *testing.T, operation string, body any) (secureMessage, messageFrame) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	message := secureMessage{Version: 1, SessionID: controller.session, NodeID: controller.nodeID, Operation: operation, RequestID: "test-request", Body: encoded}
	plaintext, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	seq := controller.toNode.Nonce()
	ciphertext, err := controller.toNode.Encrypt(nil, nil, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	frame := messageFrame{SessionID: controller.session, Seq: seq, Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)}
	var result messageFrame
	if status := controller.post(t, "/node/noise/message", frame, &result); status != http.StatusOK {
		t.Fatalf("message status %d", status)
	}
	if result.Seq != controller.fromNode.Nonce() {
		t.Fatalf("response seq = %d, want %d", result.Seq, controller.fromNode.Nonce())
	}
	responseCiphertext, err := base64.RawURLEncoding.DecodeString(result.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	responsePlaintext, err := controller.fromNode.Decrypt(nil, nil, responseCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	var response secureMessage
	if err := json.Unmarshal(responsePlaintext, &response); err != nil {
		t.Fatal(err)
	}
	return response, frame
}

func TestNodeNoiseXXHandshakeAndEncryptedFrame(t *testing.T) {
	state, err := OpenState(filepath.Join(t.TempDir(), "node"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	server := httptest.NewServer(newManagementHandler(state))
	defer server.Close()
	controller := newTestController(t, server.URL)
	id, peer := controller.start(t)
	if !bytes.Equal(peer, state.public) {
		t.Fatal("XX did not reveal the Node static key")
	}
	controller.finish(t, id)
	response, frame := controller.message(t, "status", map[string]any{})
	if response.Error != "NODE_ALREADY_CLAIMED" || response.NodeID != state.nodeID || response.RequestID != "test-request" {
		t.Fatalf("encrypted response %+v", response)
	}
	var replay map[string]any
	if status := controller.post(t, "/node/noise/message", frame, &replay); status != http.StatusBadRequest || replay["error"] != "BAD_FRAME" {
		t.Fatalf("replay = (%d, %+v)", status, replay)
	}
}

func TestNodeClaimFirstControllerWinsAndBindingSurvivesRestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	state, err := OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newManagementHandler(state))
	preview := newTestController(t, server.URL)
	_, nodePublic := preview.start(t)
	if !bytes.Equal(nodePublic, state.public) {
		t.Fatal("preview fingerprint did not use persisted key")
	}
	var bindingCount int
	if err := state.db.QueryRow(`SELECT count(*) FROM controller_binding`).Scan(&bindingCount); err != nil || bindingCount != 0 {
		t.Fatalf("preview bound Controller: (%d, %v)", bindingCount, err)
	}
	controllers := []*testController{newTestController(t, server.URL), newTestController(t, server.URL)}
	for _, controller := range controllers {
		id, peer := controller.start(t)
		if !bytes.Equal(peer, nodePublic) {
			t.Fatal("claim handshake changed Node identity")
		}
		controller.finish(t, id)
	}
	var responses [2]secureMessage
	var wait sync.WaitGroup
	for index, controller := range controllers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			responses[index], _ = controller.message(t, "claim", map[string]any{})
		}()
	}
	wait.Wait()
	winner := -1
	for index, response := range responses {
		if response.Error == "" {
			if winner != -1 {
				t.Fatalf("both Controllers claimed: %+v", responses)
			}
			winner = index
		} else if response.Error != "NODE_ALREADY_CLAIMED" {
			t.Fatalf("unexpected claim outcome %+v", response)
		}
	}
	if winner == -1 {
		t.Fatalf("no Controller claimed: %+v", responses)
	}
	server.Close()
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	server = httptest.NewServer(newManagementHandler(state))
	defer server.Close()
	for index, controller := range controllers {
		controller.baseURL = server.URL
		id, peer := controller.start(t)
		if !bytes.Equal(peer, nodePublic) {
			t.Fatal("Node identity changed after claim restart")
		}
		controller.finish(t, id)
		response, _ := controller.message(t, "status", map[string]any{})
		if index == winner && response.Error != "" {
			t.Fatalf("winning Controller could not query: %+v", response)
		}
		if index != winner && response.Error != "NODE_ALREADY_CLAIMED" {
			t.Fatalf("other Controller learned binding: %+v", response)
		}
	}
	id, _ := controllers[winner].start(t)
	controllers[winner].finish(t, id)
	response, _ := controllers[winner].message(t, "claim", map[string]any{})
	if response.Error != "NODE_ALREADY_CLAIMED" {
		t.Fatalf("repeat claim = %+v", response)
	}
}

func TestNodeRejectsWrongVersionOversizeSequenceAndIdentityWithoutBinding(t *testing.T) {
	state, err := OpenState(filepath.Join(t.TempDir(), "node"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := newManagementHandler(state)
	server := httptest.NewServer(handler)
	defer server.Close()
	controller := newTestController(t, server.URL)
	var wireError map[string]any
	if status := controller.post(t, "/node/noise/start", startFrame{Version: 2, M1: "AA"}, &wireError); status != http.StatusBadRequest || wireError["error"] != "NODE_PROTOCOL_INCOMPATIBLE" {
		t.Fatalf("wrong version = (%d, %+v)", status, wireError)
	}
	if status := controller.post(t, "/node/noise/start", startFrame{Version: 1, M1: base64.RawURLEncoding.EncodeToString(make([]byte, noise.MaxMsgLen+1))}, &wireError); status != http.StatusBadRequest || wireError["error"] != "BAD_FRAME" {
		t.Fatalf("oversize Noise frame = (%d, %+v)", status, wireError)
	}
	if status := controller.post(t, "/node/noise/start", startFrame{Version: 1, M1: "x" + string(bytes.Repeat([]byte("A"), maximumHTTPBody))}, &wireError); status != http.StatusRequestEntityTooLarge || wireError["error"] != "BAD_FRAME" {
		t.Fatalf("oversize HTTP request = (%d, %+v)", status, wireError)
	}
	id, _ := controller.start(t)
	controller.finish(t, id)
	request := secureMessage{Version: 1, SessionID: controller.session, NodeID: state.nodeID, Operation: "claim", RequestID: "claim-1", Body: json.RawMessage(`{}`)}
	contents, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := controller.toNode.Encrypt(nil, nil, contents)
	if err != nil {
		t.Fatal(err)
	}
	frame := messageFrame{SessionID: controller.session, Seq: 0, Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)}
	frame.Seq = 1
	if status := controller.post(t, "/node/noise/message", frame, &wireError); status != http.StatusBadRequest || wireError["error"] != "BAD_FRAME" {
		t.Fatalf("wrong sequence = (%d, %+v)", status, wireError)
	}
	var bindingCount int
	if err := state.db.QueryRow(`SELECT count(*) FROM controller_binding`).Scan(&bindingCount); err != nil || bindingCount != 0 {
		t.Fatalf("bad frame changed binding = (%d, %v)", bindingCount, err)
	}
	frame.Seq = 0
	var result messageFrame
	if status := controller.post(t, "/node/noise/message", frame, &result); status != http.StatusOK || result.Seq != 1 {
		t.Fatalf("valid frame after wrong sequence = (%d, %+v)", status, result)
	}
	// Deliberately discard the encrypted success receipt; a fresh authenticated status disambiguates it.
	id, _ = controller.start(t)
	controller.finish(t, id)
	response, _ := controller.message(t, "status", map[string]any{})
	if response.Error != "" || !bytes.Contains(response.Body, []byte(`"claimed":true`)) {
		t.Fatalf("status after lost receipt = %+v", response)
	}
	if status := controller.post(t, "/node/noise/message", frame, &wireError); status != http.StatusBadRequest || wireError["error"] != "BAD_FRAME" {
		t.Fatalf("replayed claim = (%d, %+v)", status, wireError)
	}
	if err := state.db.QueryRow(`SELECT count(*) FROM controller_binding`).Scan(&bindingCount); err != nil || bindingCount != 1 {
		t.Fatalf("replay changed binding = (%d, %v)", bindingCount, err)
	}
	id, _ = controller.start(t)
	controller.finish(t, id)
	wrongIdentity := secureMessage{Version: 1, SessionID: controller.session, NodeID: "wrong", Operation: "claim", RequestID: "claim-2", Body: json.RawMessage(`{}`)}
	contents, _ = json.Marshal(wrongIdentity)
	ciphertext, _ = controller.toNode.Encrypt(nil, nil, contents)
	frame = messageFrame{SessionID: controller.session, Seq: 0, Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)}
	if status := controller.post(t, "/node/noise/message", frame, &wireError); status != http.StatusBadRequest || wireError["error"] != "BAD_FRAME" {
		t.Fatalf("wrong encrypted Node identity = (%d, %+v)", status, wireError)
	}
}

func TestNodeExpiresHandshakeAndSessionWithoutBinding(t *testing.T) {
	state, err := OpenState(filepath.Join(t.TempDir(), "node"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := newManagementHandler(state)
	now := time.Now()
	handler.now = func() time.Time { return now }
	server := httptest.NewServer(handler)
	defer server.Close()
	controller := newTestController(t, server.URL)
	id, _ := controller.start(t)
	now = now.Add(handshakeLifetime)
	m3, _, _, err := controller.state.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wireError map[string]any
	if status := controller.post(t, "/node/noise/finish", finishFrame{HandshakeID: id, M3: base64.RawURLEncoding.EncodeToString(m3)}, &wireError); status != http.StatusBadRequest || wireError["error"] != "BAD_FRAME" {
		t.Fatalf("expired handshake = (%d, %+v)", status, wireError)
	}
	id, _ = controller.start(t)
	controller.finish(t, id)
	now = now.Add(sessionIdleLifetime)
	response, _ := json.Marshal(secureMessage{Version: 1, SessionID: controller.session, NodeID: state.nodeID, Operation: "claim", RequestID: "expired", Body: json.RawMessage(`{}`)})
	ciphertext, err := controller.toNode.Encrypt(nil, nil, response)
	if err != nil {
		t.Fatal(err)
	}
	frame := messageFrame{SessionID: controller.session, Seq: 0, Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)}
	if status := controller.post(t, "/node/noise/message", frame, &wireError); status != http.StatusBadRequest || wireError["error"] != "BAD_FRAME" {
		t.Fatalf("expired session = (%d, %+v)", status, wireError)
	}
	var count int
	if err := state.db.QueryRow(`SELECT count(*) FROM controller_binding`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expiry changed binding = (%d, %v)", count, err)
	}
}

func TestNodePreviewFingerprintMismatchSendsNoClaim(t *testing.T) {
	first, err := OpenState(filepath.Join(t.TempDir(), "first"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	firstServer := httptest.NewServer(newManagementHandler(first))
	defer firstServer.Close()
	controller := newTestController(t, firstServer.URL)
	_, expectedPublic := controller.start(t)

	second, err := OpenState(filepath.Join(t.TempDir(), "second"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondServer := httptest.NewServer(newManagementHandler(second))
	defer secondServer.Close()
	controller.baseURL = secondServer.URL
	_, actualPublic := controller.start(t)
	if bytes.Equal(expectedPublic, actualPublic) {
		t.Fatal("independent Nodes shared a static identity")
	}
	// A Controller that pinned the preview key must end here, before XX m3 or claim.
	var count int
	if err := second.db.QueryRow(`SELECT count(*) FROM controller_binding`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("mismatched Node was claimed: (%d, %v)", count, err)
	}
}
