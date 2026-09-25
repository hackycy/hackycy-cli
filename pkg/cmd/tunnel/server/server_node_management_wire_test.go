package server

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"github.com/hackycy/hackycy-cli/pkg/cmd/tunnel/node"
)

func TestNodeManagementPreviewReadsFullIdentityWithoutClaiming(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	directory := filepath.Join(t.TempDir(), "node")
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- node.Run(ctx, node.Config{DataDir: directory, ManagementBindAddress: "127.0.0.1", ManagementPort: port}, nil)
	}()
	address := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := http.Get(address + "/health")
		if err == nil {
			_ = response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-finished
			t.Fatalf("Node did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	peer, err := newNodeManagementWire().preview(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	if len(peer.PublicKey) != 32 || len(peer.Fingerprint) != len("SHA256:")+43 {
		t.Fatalf("incomplete Node identity: %+v", peer)
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	state, err := node.OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if peer.Fingerprint != state.Fingerprint() {
		t.Fatal("preview fingerprint differs from persisted Node identity")
	}
	controller := make([]byte, 32)
	if _, err := rand.Read(controller); err != nil {
		t.Fatal(err)
	}
	claimed, err := state.Claim(context.Background(), controller)
	if err != nil || !claimed {
		t.Fatalf("preview bound the Node: claimed=%t, err=%v", claimed, err)
	}
}

func TestNodeManagementStatusRequiresPinnedIdentityAndController(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "node")
	state, err := node.OpenState(directory)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := state.Claim(context.Background(), controller.PublicKey().Bytes())
	if err != nil || !claimed {
		t.Fatalf("claim = (%t, %v)", claimed, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- node.Run(ctx, node.Config{DataDir: directory, ManagementBindAddress: "127.0.0.1", ManagementPort: port}, nil)
	}()
	defer func() {
		cancel()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	address := "http://127.0.0.1:" + strconv.Itoa(port)
	wire := newNodeManagementWire()
	var peer nodeManagementPeer
	deadline := time.Now().Add(5 * time.Second)
	for {
		peer, err = wire.preview(context.Background(), address)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	wrong := append([]byte(nil), peer.PublicKey...)
	wrong[0] ^= 1
	if _, _, err := wire.status(context.Background(), address, controller.Bytes(), wrong); err == nil {
		t.Fatal("wrong pinned Node identity was accepted")
	}
	other, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wire.status(context.Background(), address, other.Bytes(), peer.PublicKey); err == nil {
		t.Fatal("unbound Controller read Node status")
	}
	nodeID, status, err := wire.status(context.Background(), address, controller.Bytes(), peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if nodeID == "" || !status.Claimed || status.ObservedAt == "" || status.HighestAcceptedRevision != 0 {
		t.Fatalf("invalid authenticated status: nodeID=%q, status=%+v", nodeID, status)
	}
}

func TestNodeManagementClaimPinsPreviewAndBindsOnce(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	directory := filepath.Join(t.TempDir(), "node")
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- node.Run(ctx, node.Config{DataDir: directory, ManagementBindAddress: "127.0.0.1", ManagementPort: port}, nil)
	}()
	defer func() {
		cancel()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	address := "http://127.0.0.1:" + strconv.Itoa(port)
	wire := newNodeManagementWire()
	var peer nodeManagementPeer
	deadline := time.Now().Add(5 * time.Second)
	for {
		peer, err = wire.preview(context.Background(), address)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	controller, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrong := append([]byte(nil), peer.PublicKey...)
	wrong[0] ^= 1
	if _, err := wire.claim(context.Background(), address, controller.Bytes(), wrong); err == nil {
		t.Fatal("claim accepted a changed Node identity")
	}
	nodeID, err := wire.claim(context.Background(), address, controller.Bytes(), peer.PublicKey)
	if err != nil || nodeID == "" {
		t.Fatalf("claim = (%q, %v)", nodeID, err)
	}
	if _, _, err := wire.status(context.Background(), address, controller.Bytes(), peer.PublicKey); err != nil {
		t.Fatalf("bound Controller cannot read status: %v", err)
	}
	if _, err := wire.claim(context.Background(), address, controller.Bytes(), peer.PublicKey); err == nil {
		t.Fatal("repeat claim succeeded")
	}
}

func TestNodeManagementTransfersCompleteDisabledSnapshot(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	directory := filepath.Join(t.TempDir(), "node")
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- node.Run(ctx, node.Config{DataDir: directory, ManagementBindAddress: "127.0.0.1", ManagementPort: port}, nil)
	}()
	defer func() {
		cancel()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	address := "http://127.0.0.1:" + strconv.Itoa(port)
	wire := newNodeManagementWire()
	var peer nodeManagementPeer
	deadline := time.Now().Add(5 * time.Second)
	for {
		peer, err = wire.preview(context.Background(), address)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	controller, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := wire.claim(context.Background(), address, controller.Bytes(), peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(map[string]any{
		"formatVersion": 1, "frpVersion": tunnelruntime.FRPVersion,
		"nodeId": nodeID, "revision": 1, "state": "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := wire.applySnapshot(context.Background(), address, controller.Bytes(), peer.PublicKey, nodeID, 1, snapshot)
	if err != nil || result.AppliedRevision != 1 || result.Phase != "disabled" {
		t.Fatalf("apply result = (%+v, %v)", result, err)
	}
	_, status, err := wire.status(context.Background(), address, controller.Bytes(), peer.PublicKey)
	if err != nil || status.AppliedRevision != 1 || !status.DisabledComplete {
		t.Fatalf("durable status = (%+v, %v)", status, err)
	}
}
