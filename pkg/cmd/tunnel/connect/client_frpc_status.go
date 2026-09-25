package connect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

const clientFRPCStatusTimeout = 2 * time.Second

type clientFRPCStatusEndpoint struct {
	config tunnelruntime.FRPClientWebServer
	client *http.Client
}

func newClientFRPCStatusEndpoint() (*clientFRPCStatusEndpoint, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("reserve FRPC status port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return nil, fmt.Errorf("release FRPC status port reservation: %w", err)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, fmt.Errorf("generate FRPC status credentials: %w", err)
	}
	return &clientFRPCStatusEndpoint{
		config: tunnelruntime.FRPClientWebServer{Port: port, User: "ycy", Password: hex.EncodeToString(secret[:])},
		client: &http.Client{Timeout: clientFRPCStatusTimeout, Transport: &http.Transport{Proxy: nil}},
	}, nil
}

func (endpoint *clientFRPCStatusEndpoint) observe(ctx context.Context, runtime tunnelruntime.ClientRuntime) (string, []tunnelruntime.ProxyState, error) {
	proxies := make([]tunnelruntime.ProxyState, 0)
	for _, tunnel := range runtime.Tunnels {
		if tunnel.Enabled {
			proxies = append(proxies, tunnelruntime.ProxyState{TunnelID: tunnel.ID, State: "unknown"})
		}
	}
	if len(proxies) == 0 {
		return "not_required", proxies, nil
	}
	if endpoint == nil || endpoint.client == nil {
		return "unknown", proxies, fmt.Errorf("FRPC status interface is unavailable")
	}
	url := "http://127.0.0.1:" + strconv.Itoa(endpoint.config.Port) + "/api/status"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "unknown", proxies, fmt.Errorf("create FRPC status request: %w", err)
	}
	request.SetBasicAuth(endpoint.config.User, endpoint.config.Password)
	response, err := endpoint.client.Do(request)
	if err != nil {
		return "unknown", proxies, fmt.Errorf("FRPC status interface is unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "unknown", proxies, fmt.Errorf("FRPC status interface returned HTTP %d", response.StatusCode)
	}
	var status map[string][]struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Error  string `json:"err"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&status); err != nil {
		return "unknown", proxies, fmt.Errorf("decode FRPC status interface: %w", err)
	}
	byName := make(map[string]struct{ status, errorText string })
	for _, entries := range status {
		for _, entry := range entries {
			byName[entry.Name] = struct{ status, errorText string }{entry.Status, entry.Error}
		}
	}
	connection := "unknown"
	for _, tunnel := range runtime.Tunnels {
		if !tunnel.Enabled {
			continue
		}
		name := tunnelruntime.FRPProxyName(tunnel.ID)
		entry, found := byName[name]
		if !found {
			continue
		}
		for j := range proxies {
			if proxies[j].TunnelID != tunnel.ID {
				continue
			}
			switch entry.status {
			case "running":
				proxies[j].State = "registered"
				connection = "connected"
			case "start error", "check failed":
				proxies[j].State = "failed"
				proxies[j].ErrorCode = "FRPC_PROXY_FAILED"
			}
			break
		}
	}
	return connection, proxies, nil
}
