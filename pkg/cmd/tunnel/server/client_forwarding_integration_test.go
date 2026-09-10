package server

import (
	"context"
	"errors"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	clientcommand "github.com/hackycy/hackycy-cli/pkg/cmd/tunnel/connect"
)

const goToGoHTTPHostname = "forwarding.example.test"

func TestGoClientToGoServerForwardsHTTPAndTCPAndUDPWithPinnedFRP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Setenv("HOME", t.TempDir())

	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	frpDirectory := filepath.Join(t.TempDir(), "frp", tunnelruntime.FRPVersion)
	paths, err := tunnelruntime.EnsureFRPRuntimeAt(ctx, frpDirectory, artifact)
	if err != nil {
		t.Fatalf("EnsureFRPRuntimeAt() error = %v", err)
	}
	if want := tunnelruntime.FRPRuntimePathsFor(frpDirectory, artifact.Target); paths != want {
		t.Fatalf("pinned runtime paths = %#v, want %#v", paths, want)
	}

	httpPort := startGoToGoHTTPBackend(t)
	tcpPort := startGoToGoTCPBackend(t)
	udpPort := startGoToGoUDPBackend(t)
	ports := reserveGoToGoFRPPorts(t)
	defer ports.Close()

	serverDirectory := t.TempDir()
	runtime, err := NewServerRuntime(ctx, ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address:          "127.0.0.1",
			ControlPort:      0,
			FRPPort:          ports.frp,
			HTTPPort:         ports.http,
			PortRange:        ServerHTTPPortRange{Start: ports.proxy, End: ports.proxy},
			AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "127.0.0.1", Port: ports.frp},
			DataDir:          serverDirectory,
			AdminUser:        "admin",
		},
		AdminPassword:       "integration-password",
		FRPToken:            "go-to-go-internal-frp-token",
		SessionIdleLifetime: time.Hour,
		frpArtifact:         &artifact,
		frpRuntimeDirectory: frpDirectory,
		ensureFRPRuntime:    tunnelruntime.EnsureFRPRuntimeAt,
	})
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}

	client, err := runtime.controlPlane.CreateClient(ctx, environmentAdministratorID, "Go-to-Go forwarding")
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("CreateClient() error = %v", err)
	}
	loopback := "127.0.0.1"
	remotePort := int64(ports.proxy)
	for _, input := range []TunnelMutationInput{
		{
			Protocol:      tunnelruntime.TunnelProtocolHTTP,
			CustomDomains: []string{goToGoHTTPHostname},
			LocalHost:     &loopback,
			LocalPort:     int64(httpPort),
		},
		{
			Protocol:   tunnelruntime.TunnelProtocolTCP,
			ServerPort: &remotePort,
			LocalHost:  &loopback,
			LocalPort:  int64(tcpPort),
		},
		{
			Protocol:   tunnelruntime.TunnelProtocolUDP,
			ServerPort: &remotePort,
			LocalHost:  &loopback,
			LocalPort:  int64(udpPort),
		},
	} {
		if _, err := runtime.controlPlane.CreateTunnel(ctx, client.ID, input); err != nil {
			_ = runtime.Close()
			t.Fatalf("CreateTunnel(%s) error = %v", input.Protocol, err)
		}
	}

	ports.Close()
	server, err := runtime.Start()
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("ServerRuntime.Start() error = %v", err)
	}
	defer func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("RunningServer.Close() error = %v", closeErr)
		}
	}()

	waitForGoToGoForwarding(t, "managed frps startup", 20*time.Second, func() error {
		state := runtime.frps.FRPSState()
		if state.State != tunnelruntime.FRPProcessRunning {
			return fmt.Errorf("frps state = %#v", state)
		}
		return nil
	})

	controlURL, err := url.Parse(server.URL())
	if err != nil {
		t.Fatalf("parse control URL: %v", err)
	}
	clientRoot := t.TempDir()
	clientID := goToGoClientInstanceID()
	clientContext, cancelClient := context.WithCancel(ctx)
	defer cancelClient()
	clientDone := make(chan struct{})
	var clientResult error
	go func() {
		clientResult = clientcommand.RunClient(clientContext, clientcommand.ClientConfig{Server: controlURL, Token: client.Token}, clientcommand.ClientRunOptions{
			InstanceIdentity: goToGoClientIdentity{},
			StateRoot:        clientRoot,
			YCYVersion:       "go-to-go-integration",
		})
		close(clientDone)
	}()

	waitForGoToGoForwarding(t, "client desired-state application", 20*time.Second, func() error {
		select {
		case <-clientDone:
			return fmt.Errorf("RunClient() ended before forwarding: %w", clientResult)
		default:
		}
		updated, getErr := runtime.controlPlane.GetClient(ctx, client.ID)
		if getErr != nil {
			return getErr
		}
		if updated.DesiredRevision != 3 || updated.LastAppliedRevision != updated.DesiredRevision {
			return fmt.Errorf("client revisions = desired %d, applied %d", updated.DesiredRevision, updated.LastAppliedRevision)
		}
		state, found := clientcommand.ReadClientAppliedState(filepath.Join(clientRoot, clientID))
		if !found || state.Revision != updated.DesiredRevision || len(state.Snapshot.Tunnels) != 3 {
			return fmt.Errorf("client applied state = (%#v, %t)", state, found)
		}
		return nil
	})

	waitForGoToGoForwarding(t, "HTTP forwarding", 15*time.Second, func() error {
		return verifyGoToGoHTTPForwarding(ctx, ports.http)
	})
	waitForGoToGoForwarding(t, "TCP forwarding", 15*time.Second, func() error {
		return verifyGoToGoTCPForwarding(ctx, ports.proxy)
	})
	waitForGoToGoForwarding(t, "UDP forwarding", 15*time.Second, func() error {
		return verifyGoToGoUDPForwarding(ctx, ports.proxy)
	})

	cancelClient()
	select {
	case <-clientDone:
		if clientResult != nil {
			t.Fatalf("RunClient() after cancellation error = %v", clientResult)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunClient() did not stop after cancellation")
	}
	if _, err := os.Stat(filepath.Join(clientRoot, clientID, ".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("client instance lock remains after cancellation: %v", err)
	}
	waitForGoToGoForwarding(t, "remote proxy port release", 10*time.Second, func() error {
		return verifyGoToGoRemotePortReleased(ports.proxy)
	})
}

type goToGoClientIdentity struct{}

func (goToGoClientIdentity) TunnelInstanceID(*url.URL, string) (string, error) {
	return goToGoClientInstanceID(), nil
}

func goToGoClientInstanceID() string {
	return "v1_" + strings.Repeat("g", 43)
}

type goToGoPortReservation struct {
	frp   int
	http  int
	proxy int

	closers   []io.Closer
	closeOnce sync.Once
}

func reserveGoToGoFRPPorts(t *testing.T) *goToGoPortReservation {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		frpListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve FRP port: %v", err)
		}
		httpListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = frpListener.Close()
			t.Fatalf("reserve HTTP vhost port: %v", err)
		}
		proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = httpListener.Close()
			_ = frpListener.Close()
			t.Fatalf("reserve proxy TCP port: %v", err)
		}
		proxyPort := proxyListener.Addr().(*net.TCPAddr).Port
		proxyPacket, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", proxyPort)))
		if err == nil {
			return &goToGoPortReservation{
				frp:   frpListener.Addr().(*net.TCPAddr).Port,
				http:  httpListener.Addr().(*net.TCPAddr).Port,
				proxy: proxyPort,
				closers: []io.Closer{
					frpListener,
					httpListener,
					proxyListener,
					proxyPacket,
				},
			}
		}
		_ = proxyListener.Close()
		_ = httpListener.Close()
		_ = frpListener.Close()
	}
	t.Fatal("could not reserve one local TCP/UDP proxy port")
	return nil
}

func (ports *goToGoPortReservation) Close() {
	if ports == nil {
		return
	}
	ports.closeOnce.Do(func() {
		for _, closer := range ports.closers {
			_ = closer.Close()
		}
	})
}

func startGoToGoHTTPBackend(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start HTTP backend: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/proof" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(writer, "http-forwarded")
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
	})
	return listener.Addr().(*net.TCPAddr).Port
}

func startGoToGoTCPBackend(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start TCP backend: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				contents := make([]byte, len("tcp-forward"))
				if _, readErr := io.ReadFull(connection, contents); readErr != nil {
					return
				}
				_, _ = connection.Write(append([]byte("tcp:"), contents...))
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return listener.Addr().(*net.TCPAddr).Port
}

func startGoToGoUDPBackend(t *testing.T) int {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start UDP backend: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 64*1024)
		for {
			length, address, readErr := packet.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			_, _ = packet.WriteTo(append([]byte("udp:"), buffer[:length]...), address)
		}
	}()
	t.Cleanup(func() {
		_ = packet.Close()
		<-done
	})
	return packet.LocalAddr().(*net.UDPAddr).Port
}

func waitForGoToGoForwarding(t *testing.T, label string, timeout time.Duration, check func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if err := check(); err == nil {
			return
		} else {
			last = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s did not succeed within %s: %v", label, timeout, last)
}

func verifyGoToGoHTTPForwarding(ctx context.Context, port int) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/proof", port), nil)
	if err != nil {
		return err
	}
	request.Host = goToGoHTTPHostname
	response, err := (&http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || string(contents) != "http-forwarded" {
		return fmt.Errorf("HTTP response = (%d, %q)", response.StatusCode, contents)
	}
	return nil
}

func verifyGoToGoTCPForwarding(ctx context.Context, port int) error {
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	if _, err := io.WriteString(connection, "tcp-forward"); err != nil {
		return err
	}
	contents := make([]byte, len("tcp:tcp-forward"))
	if _, err := io.ReadFull(connection, contents); err != nil {
		return err
	}
	if string(contents) != "tcp:tcp-forward" {
		return fmt.Errorf("TCP response = %q", contents)
	}
	return nil
}

func verifyGoToGoUDPForwarding(ctx context.Context, port int) error {
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "udp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	if _, err := io.WriteString(connection, "udp-forward"); err != nil {
		return err
	}
	contents := make([]byte, len("udp:udp-forward"))
	if _, err := io.ReadFull(connection, contents); err != nil {
		return err
	}
	if string(contents) != "udp:udp-forward" {
		return fmt.Errorf("UDP response = %q", contents)
	}
	return nil
}

func verifyGoToGoRemotePortReleased(port int) error {
	tcp, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	if err != nil {
		return fmt.Errorf("TCP remote port remains occupied: %w", err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	if err != nil {
		return fmt.Errorf("UDP remote port remains occupied: %w", err)
	}
	return udp.Close()
}
