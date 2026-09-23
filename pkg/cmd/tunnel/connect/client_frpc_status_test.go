package connect

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestFRPCStatusObservationSeparatesRegisteredFailedAndUnknownProxies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		user, password, ok := request.BasicAuth()
		if !ok || user != "ycy" || password != "secret" || request.URL.Path != "/api/status" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = writer.Write([]byte(`{"tcp":[{"name":"t_registered","status":"running","err":""},{"name":"t_failed","status":"start error","err":"port occupied"}]}`))
	}))
	defer server.Close()
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := &clientFRPCStatusEndpoint{config: tunnelruntime.FRPClientWebServer{Port: port, User: "ycy", Password: "secret"}, client: server.Client()}
	runtime := tunnelruntime.ClientRuntime{Tunnels: []tunnelruntime.TunnelDefinition{{ID: "registered", Enabled: true}, {ID: "failed", Enabled: true}, {ID: "missing", Enabled: true}}}
	connection, proxies, err := endpoint.observe(context.Background(), runtime)
	if err != nil || connection != "connected" || len(proxies) != 3 || proxies[0].State != "registered" || proxies[1].State != "failed" || proxies[2].State != "unknown" {
		t.Fatalf("observe() = (%q, %#v, %v)", connection, proxies, err)
	}
	server.Close()
	connection, proxies, err = endpoint.observe(context.Background(), runtime)
	if err == nil || connection != "unknown" || proxies[0].State != "unknown" {
		t.Fatalf("observe(unavailable) = (%q, %#v, %v)", connection, proxies, err)
	}
}
