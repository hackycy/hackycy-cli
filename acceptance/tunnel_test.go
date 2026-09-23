//go:build acceptance

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTunnelServerStandaloneBinaryPreservesCLIValidation(t *testing.T) {
	binary := buildDiffStandaloneBinary(t)
	environment := environmentWith(map[string]string{
		"HOME":                         t.TempDir(),
		"USERPROFILE":                  "",
		"YCY_TUNNEL_ADMIN_USER":        "admin",
		"YCY_TUNNEL_ADMIN_PASSWORD":    "standalone-password",
		"YCY_TUNNEL_FRP_TOKEN":         "standalone-token",
		"YCY_TUNNEL_DOCKER":            "",
		"YCY_TUNNEL_ADDRESS":           "127.0.0.1",
		"YCY_TUNNEL_CONTROL_PORT":      "7500",
		"YCY_TUNNEL_FRP_PORT":          "7000",
		"YCY_TUNNEL_HTTP_PORT":         "8080",
		"YCY_TUNNEL_PORT_RANGE":        "20000-20100",
		"YCY_TUNNEL_DATA_DIR":          t.TempDir(),
		"YCY_TUNNEL_SESSION_IDLE_DAYS": "7",
	})

	help, err := runDiffStandalone(binary, t.TempDir(), environment, "tunnel", "server", "--help")
	if err != nil {
		t.Fatalf("tunnel server --help: %v\n%s", err, help)
	}
	for _, expected := range []string{
		"Run the Tunnel Control Plane and supervised frps process",
		"--address", "--control-port", "--frp-port", "--http-port", "--port-range",
		"--advertise-frp-addr", "--data-dir", "--session-idle-days",
		"Flags:", "--log-level",
	} {
		if !strings.Contains(string(help), expected) {
			t.Fatalf("tunnel server help omitted %q:\n%s", expected, help)
		}
	}

	for _, testCase := range []struct {
		arguments []string
		message   string
	}{
		{arguments: []string{"tunnel", "server", "--control-port", "0"}, message: "Control port must be an integer from 1 through 65535"},
		{arguments: []string{"tunnel", "server", "--control-port", "7000", "--frp-port", "7000"}, message: "Control, FRP bind, and FRP HTTP listener ports must be distinct"},
		{arguments: []string{"tunnel", "server", "--port-range", "7000-7010"}, message: "Server Port Pool must not include listener port 7000"},
		{arguments: []string{"tunnel", "server", "--advertise-frp-addr", "https://tunnels.example.test:7000"}, message: "Advertised FRP address must be host:port or [IPv6]:port"},
		{arguments: []string{"tunnel", "connect", "--server", "", "--token", "client-token"}, message: "Control plane must not be empty"},
	} {
		output, runErr := runDiffStandalone(binary, t.TempDir(), environment, testCase.arguments...)
		if exitCode(runErr) != 1 || !strings.Contains(string(output), testCase.message) {
			t.Fatalf("arguments %q = (%v, %q), want %q", testCase.arguments, runErr, output, testCase.message)
		}
	}

	connectHelp, err := runDiffStandalone(binary, t.TempDir(), environment, "tunnel", "connect", "--help")
	if err != nil {
		t.Fatalf("tunnel connect --help: %v\n%s", err, connectHelp)
	}
	for _, expected := range []string{
		"Connect a native trusted client to a Tunnel Control Plane",
		"--server", "--token", "Flags:", "--log-level",
	} {
		if !strings.Contains(string(connectHelp), expected) {
			t.Fatalf("tunnel connect help omitted %q:\n%s", expected, connectHelp)
		}
	}

	withoutPassword := environmentWith(map[string]string{
		"HOME":                         t.TempDir(),
		"USERPROFILE":                  "",
		"YCY_TUNNEL_ADMIN_PASSWORD":    "",
		"YCY_TUNNEL_DOCKER":            "",
		"YCY_TUNNEL_ADDRESS":           "127.0.0.1",
		"YCY_TUNNEL_CONTROL_PORT":      "7500",
		"YCY_TUNNEL_FRP_PORT":          "7000",
		"YCY_TUNNEL_HTTP_PORT":         "8080",
		"YCY_TUNNEL_PORT_RANGE":        "20000-20100",
		"YCY_TUNNEL_DATA_DIR":          t.TempDir(),
		"YCY_TUNNEL_SESSION_IDLE_DAYS": "7",
	})
	output, runErr := runDiffStandalone(binary, t.TempDir(), withoutPassword, "tunnel", "server")
	if exitCode(runErr) != 1 || !strings.Contains(string(output), "YCY_TUNNEL_ADMIN_PASSWORD must contain 5-256 characters") {
		t.Fatalf("missing administrator password = (%v, %q)", runErr, output)
	}

}

func TestTunnelStandaloneBinaryRejectsV4ClientHello(t *testing.T) {
	binary := buildDiffStandaloneBinary(t)
	reservePort := func() int {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		return port
	}
	controlPort, frpPort, httpPort := reservePort(), reservePort(), reservePort()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, resolveStandaloneBinary(binary), "tunnel", "server",
		"--address", "127.0.0.1", "--control-port", strconv.Itoa(controlPort),
		"--frp-port", strconv.Itoa(frpPort), "--http-port", strconv.Itoa(httpPort),
		"--port-range", "40000-40010", "--data-dir", t.TempDir())
	command.Env = environmentWith(map[string]string{
		"YCY_TUNNEL_ADMIN_USER": "admin", "YCY_TUNNEL_ADMIN_PASSWORD": "standalone-password",
		"YCY_TUNNEL_FRP_TOKEN": "standalone-token", "YCY_TUNNEL_DOCKER": "",
	})
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	httpClient := &http.Client{Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		response, err := httpClient.Get(base + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			ready = response.StatusCode == http.StatusOK
		}
		if ready {
			break
		}
	}
	if !ready {
		t.Fatalf("standalone Server did not become healthy: %s", output.String())
	}
	request, err := http.NewRequest(http.MethodPost, base+"/api/session", strings.NewReader(`{"username":"admin","password":"standalone-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", base)
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || len(response.Cookies()) == 0 {
		t.Fatalf("standalone login status = %d", response.StatusCode)
	}
	request, err = http.NewRequest(http.MethodPost, base+"/api/clients", strings.NewReader(`{"remark":"protocol probe"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", base)
	request.AddCookie(response.Cookies()[0])
	response, err = httpClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create Client status = %d", response.StatusCode)
	}
	var created struct {
		Client struct {
			Token string `json:"token"`
		} `json:"client"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil || created.Client.Token == "" {
		t.Fatalf("decode created Client = (%#v, %v)", created, err)
	}
	endpoint := fmt.Sprintf("ws://127.0.0.1:%d/api/agent", controlPort)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		socket, _, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": []string{"Bearer " + created.Client.Token}})
		if err != nil {
			continue
		}
		_ = socket.SetReadDeadline(time.Now().Add(3 * time.Second))
		if err := socket.WriteJSON(map[string]any{"type": "hello", "tunnelProtocolVersion": 4, "ycyVersion": "old", "platform": "linux", "architecture": "x64", "lastAppliedRevision": 0}); err != nil {
			_ = socket.Close()
			continue
		}
		_, _, err = socket.ReadMessage()
		_ = socket.Close()
		var closeError *websocket.CloseError
		if errors.As(err, &closeError) && closeError.Code == 4406 && strings.Contains(closeError.Text, "upgrade ycy") {
			return
		}
	}
	t.Fatalf("standalone Server did not reject v4 hello with 4406 and upgrade advice: %s", output.String())
}
