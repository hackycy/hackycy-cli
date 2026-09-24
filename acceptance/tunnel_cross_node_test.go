//go:build acceptance

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTunnelStandaloneBinariesSwitchOfficialClientAcrossNodes(t *testing.T) {
	binary := buildDiffStandaloneBinary(t)
	used := make(map[int]bool)
	reserve := func() int {
		t.Helper()
		for {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			if !used[port] {
				used[port] = true
				return port
			}
		}
	}
	controlPort, localFRPPort, localHTTPPort := reserve(), reserve(), reserve()
	managementPort, remoteFRPPort, remoteHTTPPort := reserve(), reserve(), reserve()
	proxyPort := reserve()
	var backend net.Listener
	for {
		var err error
		backend, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if !used[backend.Addr().(*net.TCPAddr).Port] {
			break
		}
		_ = backend.Close()
	}
	defer backend.Close()
	backendPort := backend.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			connection, err := backend.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()

	start := func(name string, environment []string, arguments ...string) string {
		t.Helper()
		logPath := filepath.Join(t.TempDir(), name+".log")
		logFile, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(resolveStandaloneBinary(binary), arguments...)
		command.Env = environment
		command.Stdout, command.Stderr = logFile, logFile
		if err := command.Start(); err != nil {
			_ = logFile.Close()
			t.Fatalf("start %s: %v", name, err)
		}
		t.Cleanup(func() {
			_ = command.Process.Signal(os.Interrupt)
			done := make(chan struct{})
			go func() {
				_ = command.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = command.Process.Kill()
				<-done
			}
			_ = logFile.Close()
		})
		return logPath
	}
	serverLog := start("server", environmentWith(map[string]string{
		"HOME": t.TempDir(), "USERPROFILE": "", "YCY_TUNNEL_DOCKER": "",
		"YCY_TUNNEL_ADMIN_USER": "admin", "YCY_TUNNEL_ADMIN_PASSWORD": "binary-switch-password",
		"YCY_TUNNEL_FRP_TOKEN": "local-binary-token",
	}), "tunnel", "server", "--address", "127.0.0.1", "--control-port", strconv.Itoa(controlPort),
		"--frp-port", strconv.Itoa(localFRPPort), "--http-port", strconv.Itoa(localHTTPPort),
		"--port-range", fmt.Sprintf("%d-%d", proxyPort, proxyPort),
		"--advertise-frp-addr", net.JoinHostPort("127.0.0.1", strconv.Itoa(localFRPPort)),
		"--data-dir", t.TempDir())
	base := fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	waitTunnelBinary(t, "Server health", 30*time.Second, serverLog, func() error {
		response, err := (&http.Client{Timeout: time.Second}).Get(base + "/healthz")
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("health status %d", response.StatusCode)
		}
		return nil
	})
	login := tunnelBinaryRequest(t, base, nil, http.MethodPost, "/api/session", map[string]string{
		"username": "admin", "password": "binary-switch-password",
	}, http.StatusOK)
	var session struct {
		Cookie *http.Cookie
	}
	if len(login.Cookies()) != 0 {
		session.Cookie = login.Cookies()[0]
	}
	if session.Cookie == nil {
		t.Fatal("Server login omitted session cookie")
	}
	_ = login.Body.Close()
	var created struct {
		Client struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"client"`
	}
	decodeTunnelBinaryResponse(t, tunnelBinaryRequest(t, base, session.Cookie, http.MethodPost, "/api/clients", map[string]string{"remark": "binary switch"}, http.StatusCreated), &created)
	if created.Client.ID == "" || created.Client.Token == "" {
		t.Fatal("Server did not create a Client with a Token")
	}
	decodeTunnelBinaryResponse(t, tunnelBinaryRequest(t, base, session.Cookie, http.MethodPost, "/api/clients/"+created.Client.ID+"/tunnels", map[string]any{
		"protocol": "tcp", "serverPort": proxyPort, "localHost": "127.0.0.1", "localPort": backendPort,
	}, http.StatusCreated), &struct{}{})

	nodeLog := start("node", environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""}),
		"tunnel", "node", "--management-bind-address", "127.0.0.1", "--management-port", strconv.Itoa(managementPort), "--data-dir", filepath.Join(t.TempDir(), "node"))
	managementAddress := fmt.Sprintf("http://127.0.0.1:%d", managementPort)
	waitTunnelBinary(t, "Node health", 20*time.Second, nodeLog, func() error {
		response, err := (&http.Client{Timeout: time.Second}).Get(managementAddress + "/health")
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("Node health status %d", response.StatusCode)
		}
		return nil
	})
	var preview struct {
		PreviewID       string `json:"previewId"`
		NodeFingerprint string `json:"nodeFingerprint"`
	}
	decodeTunnelBinaryResponse(t, tunnelBinaryRequest(t, base, session.Cookie, http.MethodPost, "/api/nodes/claim-previews", map[string]string{
		"managementAddress": managementAddress,
	}, http.StatusOK), &preview)
	var claimed struct {
		Node struct {
			ID string `json:"id"`
		} `json:"node"`
	}
	decodeTunnelBinaryResponse(t, tunnelBinaryRequest(t, base, session.Cookie, http.MethodPost, "/api/nodes", map[string]string{
		"previewId": preview.PreviewID, "name": "Binary Remote", "confirmedFingerprint": preview.NodeFingerprint, "mode": "claim",
	}, http.StatusCreated), &claimed)
	nodePath := "/api/nodes/" + claimed.Node.ID
	decodeTunnelBinaryResponse(t, tunnelBinaryRequest(t, base, session.Cookie, http.MethodPatch, nodePath, map[string]any{
		"advertisedFrpAddress": map[string]any{"host": "127.0.0.1", "port": remoteFRPPort},
	}, http.StatusOK), &struct{}{})
	decodeTunnelBinaryResponse(t, tunnelBinaryRequest(t, base, session.Cookie, http.MethodPut, nodePath+"/desired", map[string]any{
		"expectedRevision": 0,
		"settings":         map[string]any{"bindAddress": "127.0.0.1", "bindPort": remoteFRPPort, "vhostHTTPPort": remoteHTTPPort, "portRangeStart": proxyPort, "portRangeEnd": proxyPort},
	}, http.StatusOK), &struct{}{})
	waitTunnelBinary(t, "Remote FRPS selectable", 30*time.Second, nodeLog, func() error {
		var result struct {
			Node struct {
				Selectability struct {
					Selectable bool `json:"selectable"`
				} `json:"selectability"`
			} `json:"node"`
		}
		if err := readTunnelBinaryJSON(base, session.Cookie, nodePath, &result); err != nil {
			return err
		}
		if !result.Node.Selectability.Selectable {
			return fmt.Errorf("Remote Node is not selectable")
		}
		return nil
	})

	clientLog := start("client", environmentWith(map[string]string{
		"HOME": t.TempDir(), "USERPROFILE": "", "XDG_STATE_HOME": t.TempDir(), "YCY_TUNNEL_DOCKER": "",
	}), "tunnel", "connect", "--server", base, "--token", created.Client.Token)
	clientPath := "/api/clients/" + created.Client.ID
	readClient := func(nodeID string, previousGeneration string) (string, error) {
		var result struct {
			Client struct {
				DesiredRevision     int64 `json:"desiredRevision"`
				LastAppliedRevision int64 `json:"lastAppliedRevision"`
				Assignment          struct {
					NodeID        string  `json:"nodeId"`
					AppliedNodeID *string `json:"appliedNodeId"`
				} `json:"assignment"`
				FRPC struct {
					NodeID            string `json:"nodeId"`
					Connection        string `json:"connection"`
					ProcessGeneration string `json:"processGeneration"`
					Proxies           []struct {
						State string `json:"state"`
					} `json:"proxies"`
				} `json:"frpc"`
			} `json:"client"`
		}
		if err := readTunnelBinaryJSON(base, session.Cookie, clientPath, &result); err != nil {
			return "", err
		}
		client := result.Client
		if client.Assignment.NodeID != nodeID || client.Assignment.AppliedNodeID == nil || *client.Assignment.AppliedNodeID != nodeID || client.DesiredRevision != client.LastAppliedRevision || client.FRPC.NodeID != nodeID || client.FRPC.Connection != "connected" || client.FRPC.ProcessGeneration == "" || client.FRPC.ProcessGeneration == previousGeneration || len(client.FRPC.Proxies) != 1 || client.FRPC.Proxies[0].State != "registered" {
			return "", fmt.Errorf("Client state = %+v", client)
		}
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)), time.Second)
		if err != nil {
			return "", err
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		message := []byte("binary Node switch")
		if _, err := connection.Write(message); err != nil {
			return "", err
		}
		answer := make([]byte, len(message))
		if _, err := io.ReadFull(connection, answer); err != nil {
			return "", err
		}
		if !bytes.Equal(answer, message) {
			return "", fmt.Errorf("TCP forwarding returned %q", answer)
		}
		return client.FRPC.ProcessGeneration, nil
	}
	var generation string
	waitTunnelBinary(t, "Local official FRPC", 30*time.Second, clientLog, func() error {
		var err error
		generation, err = readClient("local", "")
		return err
	})
	for _, target := range []string{claimed.Node.ID, "local"} {
		decodeTunnelBinaryResponse(t, tunnelBinaryRequest(t, base, session.Cookie, http.MethodPut, clientPath+"/node-assignment", map[string]string{"nodeId": target}, http.StatusOK), &struct{}{})
		previous := generation
		waitTunnelBinary(t, "official FRPC switch to "+target, 30*time.Second, clientLog, func() error {
			var err error
			generation, err = readClient(target, previous)
			return err
		})
	}
}

func tunnelBinaryRequest(t *testing.T, base string, cookie *http.Cookie, method, path string, body any, want int) *http.Response {
	t.Helper()
	contents, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, base+path, bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", base)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		contents, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, response.StatusCode, want, contents)
	}
	return response
}

func decodeTunnelBinaryResponse(t *testing.T, response *http.Response, output any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		t.Fatal(err)
	}
}

func readTunnelBinaryJSON(base string, cookie *http.Cookie, path string, output any) error {
	request, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	request.AddCookie(cookie)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %d", path, response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(output)
}

func waitTunnelBinary(t *testing.T, name string, timeout time.Duration, logPath string, check func() error) {
	t.Helper()
	var last error
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if last = check(); last == nil {
			return
		}
	}
	log, _ := os.ReadFile(logPath)
	t.Fatalf("%s: %v\n%s", name, last, log)
}
