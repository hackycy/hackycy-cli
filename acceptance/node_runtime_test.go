//go:build acceptance

package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/flynn/noise"
	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

type acceptanceNodePeer struct {
	key     noise.DHKey
	url     string
	send    *noise.CipherState
	receive *noise.CipherState
	session string
	nodeID  string
	public  []byte
}

func (peer *acceptanceNodePeer) post(t *testing.T, path string, input any, output any) {
	t.Helper()
	contents, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Post(peer.url+path, "application/json", bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Node %s returned HTTP %d", path, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		t.Fatal(err)
	}
}

func (peer *acceptanceNodePeer) connect(t *testing.T) {
	t.Helper()
	handshake, err := noise.NewHandshakeState(noise.Config{CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256), Random: rand.Reader, Pattern: noise.HandshakeXX, Initiator: true, Prologue: []byte("ycy/tunnel-node-management/1"), StaticKeypair: peer.key})
	if err != nil {
		t.Fatal(err)
	}
	m1, _, _, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var start struct {
		HandshakeID string `json:"handshakeId"`
		M2          string `json:"m2"`
	}
	peer.post(t, "/node/noise/start", map[string]any{"version": 1, "m1": base64.RawURLEncoding.EncodeToString(m1)}, &start)
	m2, err := base64.RawURLEncoding.DecodeString(start.M2)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := handshake.ReadMessage(nil, m2); err != nil {
		t.Fatal(err)
	}
	if peer.public != nil && !bytes.Equal(peer.public, handshake.PeerStatic()) {
		t.Fatal("Node identity changed across restart")
	}
	peer.public = append([]byte(nil), handshake.PeerStatic()...)
	m3, send, receive, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var finish struct {
		SessionID  string `json:"sessionId"`
		Seq        uint64 `json:"seq"`
		Ciphertext string `json:"ciphertext"`
	}
	peer.post(t, "/node/noise/finish", map[string]any{"handshakeId": start.HandshakeID, "m3": base64.RawURLEncoding.EncodeToString(m3)}, &finish)
	if finish.Seq != 0 {
		t.Fatalf("confirmation seq %d", finish.Seq)
	}
	peer.send, peer.receive, peer.session = send, receive, finish.SessionID
	ciphertext, err := base64.RawURLEncoding.DecodeString(finish.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := receive.Decrypt(nil, nil, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	var confirmation struct {
		SessionID string `json:"sessionId"`
		NodeID    string `json:"nodeId"`
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(plaintext, &confirmation); err != nil {
		t.Fatal(err)
	}
	if confirmation.SessionID != finish.SessionID || confirmation.Operation != "confirm" || confirmation.NodeID == "" {
		t.Fatalf("invalid confirmation: %+v", confirmation)
	}
	if peer.nodeID != "" && peer.nodeID != confirmation.NodeID {
		t.Fatal("Node ID changed across restart")
	}
	peer.nodeID = confirmation.NodeID
}

func (peer *acceptanceNodePeer) message(t *testing.T, operation, requestID string, body any) (string, json.RawMessage) {
	t.Helper()
	contents, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := json.Marshal(map[string]any{"version": 1, "sessionId": peer.session, "nodeId": peer.nodeID, "operation": operation, "requestId": requestID, "body": json.RawMessage(contents)})
	if err != nil {
		t.Fatal(err)
	}
	seq := peer.send.Nonce()
	ciphertext, err := peer.send.Encrypt(nil, nil, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		SessionID  string `json:"sessionId"`
		Seq        uint64 `json:"seq"`
		Ciphertext string `json:"ciphertext"`
	}
	peer.post(t, "/node/noise/message", map[string]any{"sessionId": peer.session, "seq": seq, "ciphertext": base64.RawURLEncoding.EncodeToString(ciphertext)}, &response)
	if response.SessionID != peer.session || response.Seq != peer.receive.Nonce() {
		t.Fatalf("response frame mismatch: %+v", response)
	}
	encrypted, err := base64.RawURLEncoding.DecodeString(response.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := peer.receive.Decrypt(nil, nil, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Error string          `json:"error"`
		Body  json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(decoded, &result); err != nil {
		t.Fatal(err)
	}
	return result.Error, result.Body
}

func (peer *acceptanceNodePeer) discardResponse(t *testing.T, operation, requestID string, body any) {
	t.Helper()
	contents, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := json.Marshal(map[string]any{"version": 1, "sessionId": peer.session, "nodeId": peer.nodeID, "operation": operation, "requestId": requestID, "body": json.RawMessage(contents)})
	if err != nil {
		t.Fatal(err)
	}
	seq := peer.send.Nonce()
	ciphertext, err := peer.send.Encrypt(nil, nil, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(map[string]any{"sessionId": peer.session, "seq": seq, "ciphertext": base64.RawURLEncoding.EncodeToString(ciphertext)})
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Post(peer.url+"/node/noise/message", "application/json", bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unobserved commit returned HTTP %d", response.StatusCode)
	}
}

func (peer *acceptanceNodePeer) apply(t *testing.T, snapshot map[string]any, revision int64, loseCommitResponse bool) {
	t.Helper()
	contents, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	sha := hex.EncodeToString(digest[:])
	peer.connect(t)
	requestID := fmt.Sprintf("apply-%d", revision)
	for index, frame := range []map[string]any{
		{"phase": "begin", "revision": revision, "nodeId": peer.nodeID, "totalBytes": len(contents), "sha256": sha, "formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion},
		{"phase": "chunk", "offset": 0, "bytes": base64.RawURLEncoding.EncodeToString(contents)},
		{"phase": "commit", "revision": revision, "sha256": sha},
	} {
		if index == 2 && loseCommitResponse {
			peer.discardResponse(t, "applySnapshot", requestID, frame)
			continue
		}
		if code, body := peer.message(t, "applySnapshot", requestID, frame); code != "" {
			t.Fatalf("apply revision %d: %s, %s", revision, code, body)
		}
	}
}

type acceptanceNodeStatus struct {
	AppliedRevision  int64  `json:"appliedRevision"`
	BootDisabled     bool   `json:"bootDisabled"`
	DisabledComplete bool   `json:"disabledComplete"`
	FRPSProcess      string `json:"frpsProcess"`
	FRPSPID          *int   `json:"frpsPID"`
}

func (peer *acceptanceNodePeer) status(t *testing.T) acceptanceNodeStatus {
	t.Helper()
	peer.connect(t)
	code, body := peer.message(t, "status", "status", struct{}{})
	if code != "" {
		t.Fatalf("status error: %s", code)
	}
	var result acceptanceNodeStatus
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTunnelNodeStandaloneBinaryAppliesRestoresAndDisablesFRPS(t *testing.T) {
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatal(err)
	}
	frpDirectory, err := tunnelruntime.DefaultFRPRuntimeDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tunnelruntime.EnsureFRPRuntimeAt(context.Background(), frpDirectory, artifact); err != nil {
		t.Fatal(err)
	}
	binary := buildDiffStandaloneBinary(t)
	directory := filepath.Join(t.TempDir(), "node")
	ports := make([]int, 0, 4)
	for len(ports) < 4 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		value := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		unique := true
		for _, previous := range ports {
			if previous == value {
				unique = false
			}
		}
		if unique {
			ports = append(ports, value)
		}
	}
	managementURL := fmt.Sprintf("http://127.0.0.1:%d", ports[0])
	peerKey, err := noise.DH25519.GenerateKeypair(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peer := &acceptanceNodePeer{key: peerKey, url: managementURL}
	start := func() *exec.Cmd {
		t.Helper()
		command := exec.Command(resolveStandaloneBinary(binary), "tunnel", "node", "--management-bind-address", "127.0.0.1", "--management-port", strconv.Itoa(ports[0]), "--data-dir", directory)
		command.Env = environmentWith(map[string]string{})
		command.Stdout, command.Stderr = io.Discard, io.Discard
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			client := &http.Client{Timeout: time.Second}
			response, err := client.Get(managementURL + "/health")
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return command
				}
			}
		}
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal("standalone Node did not become healthy")
		return nil
	}
	stop := func(command *exec.Cmd) { _ = command.Process.Kill(); _ = command.Wait() }
	command := start()
	defer func() {
		if command != nil {
			stop(command)
		}
	}()
	peer.connect(t)
	if code, _ := peer.message(t, "claim", "claim", struct{}{}); code != "" {
		t.Fatalf("claim: %s", code)
	}
	running := map[string]any{"formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion, "nodeId": peer.nodeID, "revision": 1, "state": "running", "bindAddress": "127.0.0.1", "bindPort": ports[1], "vhostHTTPPort": ports[2], "portRangeStart": ports[3], "portRangeEnd": ports[3], "token": "standalone-secret"}
	peer.apply(t, running, 1, true)
	firstStatus := peer.status(t)
	if firstStatus.AppliedRevision != 1 || firstStatus.FRPSProcess != "running" || firstStatus.FRPSPID == nil {
		t.Fatalf("running status after lost commit response: %+v", firstStatus)
	}
	peer.apply(t, running, 1, false)
	if status := peer.status(t); status.AppliedRevision != 1 || status.FRPSPID == nil || *status.FRPSPID != *firstStatus.FRPSPID {
		t.Fatalf("same-revision retry changed FRPS: %+v", status)
	}
	if status := peer.status(t); status.AppliedRevision != 1 || status.FRPSProcess != "running" {
		t.Fatalf("running status: %+v", status)
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[1])), time.Second)
	if err != nil {
		t.Fatalf("FRPS did not listen: %v", err)
	}
	_ = connection.Close()
	stop(command)
	command = nil
	command = start()
	if status := peer.status(t); status.AppliedRevision != 1 || status.FRPSProcess != "running" {
		t.Fatalf("restart status: %+v", status)
	}
	peer.apply(t, map[string]any{"formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion, "nodeId": peer.nodeID, "revision": 2, "state": "disabled"}, 2, false)
	if status := peer.status(t); status.AppliedRevision != 2 || !status.BootDisabled || !status.DisabledComplete || status.FRPSProcess != "stopped" {
		t.Fatalf("disabled status: %+v", status)
	}
	stop(command)
	command = nil
	command = start()
	if status := peer.status(t); status.AppliedRevision != 2 || !status.DisabledComplete || status.FRPSProcess != "stopped" {
		t.Fatalf("disabled restart status: %+v", status)
	}
	if connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[1])), time.Second); err == nil {
		_ = connection.Close()
		t.Fatal("disabled FRPS revived")
	}
}
