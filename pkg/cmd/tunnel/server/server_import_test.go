package server

import (
	"context"
	"encoding/json"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"strings"
	"testing"
)

const serverImportSource = `
serverAddr = "tunnel.example.com"
serverPort = 7000

[[proxies]]
name = "web"
type = "http"
localIP = "app"
localPort = 3000
customDomains = ["app.example.com", "alias.example.com"]
locations = ["/api", "/admin"]
httpUser = "operator"
httpPassword = "secret-value"
hostHeaderRewrite = "backend.example.com"
requestHeaders.set = { X-Forwarded-By = "ycy" }
responseHeaders.set = { X-Tunnel = "web" }

[proxies.transport]
useEncryption = true
useCompression = true
bandwidthLimit = "2MB"
bandwidthLimitMode = "server"
proxyProtocolVersion = "v2"

[proxies.healthCheck]
type = "http"
path = "/health"
intervalSeconds = 10
timeoutSeconds = 3
maxFailed = 2
httpHeaders = [{ name = "X-Probe", value = "ycy" }]

[[proxies]]
name = "database"
type = "tcp"
localPort = 5432
remotePort = 20001

[[proxies]]
name = "dns"
type = "udp"
localPort = 53
remotePort = 20002
`

func TestParseFRPCTunnelImportMapsCandidates(t *testing.T) {
	imported, err := ParseFRPCTunnelImport(serverImportSource)
	if err != nil {
		t.Fatalf("ParseFRPCTunnelImport() error = %v", err)
	}
	if len(imported.Candidates) != 4 {
		t.Fatalf("candidate count = %d, want 4", len(imported.Candidates))
	}
	first := imported.Candidates[0]
	if first.ID != "proxy-0-location-0" || first.Input.Protocol != tunnelruntime.TunnelProtocolHTTP || strings.Join(first.Input.CustomDomains, ",") != "app.example.com,alias.example.com" || first.Input.Location == nil || *first.Input.Location != "/api" || first.Input.LocalHost == nil || *first.Input.LocalHost != "app" || first.Input.Options == nil || first.Input.Options.Transport == nil || first.Input.Options.Transport.BandwidthLimit == nil || first.Input.Options.Transport.BandwidthLimit.Unit != "MB" || first.Input.Options.HealthCheck == nil || first.Input.Options.HTTP == nil || first.Input.Options.HTTP.BasicAuth == nil || first.Input.Options.HTTP.BasicAuth.Password != "secret-value" {
		t.Fatalf("first import candidate = %#v", first)
	}
	if second := imported.Candidates[1]; second.ID != "proxy-0-location-1" || second.Input.Location == nil || *second.Input.Location != "/admin" {
		t.Fatalf("second import candidate = %#v", second)
	}
	if tcp := imported.Candidates[2]; tcp.Input.Protocol != tunnelruntime.TunnelProtocolTCP || tcp.Input.ServerPort == nil || *tcp.Input.ServerPort != 20001 || tcp.Input.LocalHost == nil || *tcp.Input.LocalHost != "127.0.0.1" {
		t.Fatalf("TCP import candidate = %#v", tcp)
	}
	if udp := imported.Candidates[3]; udp.Input.Protocol != tunnelruntime.TunnelProtocolUDP || udp.Input.ServerPort == nil || *udp.Input.ServerPort != 20002 {
		t.Fatalf("UDP import candidate = %#v", udp)
	}
	if !hasTunnelImportNotice(imported.Ignored, "", "Client connection settings are not imported") {
		t.Fatalf("import notices = %#v", imported.Ignored)
	}
}

func TestParseFRPCTunnelImportRejectsInvalidDocumentsAndReportsIgnoredProxies(t *testing.T) {
	for _, source := range []string{"[common]\nserver_addr = \"example.com\"", "proxies = ["} {
		if _, err := ParseFRPCTunnelImport(source); err == nil {
			t.Fatalf("ParseFRPCTunnelImport(%q) error = nil", source)
		}
	}
	imported, err := ParseFRPCTunnelImport(`
[[proxies]]
name = "private"
type = "stcp"
localPort = 7000
secretKey = "not-imported"

[[proxies]]
name = "catch-all"
type = "http"
localPort = 3000
customDomains = ["catch.example.com"]
plugin = { type = "static_file" }
`)
	if err != nil {
		t.Fatalf("ParseFRPCTunnelImport() error = %v", err)
	}
	if len(imported.Candidates) != 1 || imported.Candidates[0].Input.Location != nil {
		t.Fatalf("import candidates = %#v", imported.Candidates)
	}
	if !hasTunnelImportNotice(imported.Ignored, "private", "Ignored unsupported proxy type: stcp") || !hasTunnelImportNotice(imported.Ignored, "catch-all", "Ignored unsupported fields: plugin") {
		t.Fatalf("import notices = %#v", imported.Ignored)
	}
}

func TestParseFRPCTunnelImportRetainsIndependentTransportFieldsAfterAnIgnoredValue(t *testing.T) {
	imported, err := ParseFRPCTunnelImport(`
[[proxies]]
name = "database"
type = "tcp"
localPort = 5432
remotePort = 20001

[proxies.transport]
bandwidthLimit = "2MB"
bandwidthLimitMode = "unsupported"
proxyProtocolVersion = "v1"
`)
	if err != nil {
		t.Fatalf("ParseFRPCTunnelImport() error = %v", err)
	}
	if len(imported.Candidates) != 1 || imported.Candidates[0].Input.Options == nil || imported.Candidates[0].Input.Options.Transport == nil || imported.Candidates[0].Input.Options.Transport.BandwidthLimit != nil || imported.Candidates[0].Input.Options.Transport.ProxyProtocolVersion == nil || *imported.Candidates[0].Input.Options.Transport.ProxyProtocolVersion != "v1" {
		t.Fatalf("import candidates = %#v", imported.Candidates)
	}
	if !hasTunnelImportNotice(imported.Ignored, "database", "Bandwidth limit was ignored because its mode is not client or server") {
		t.Fatalf("import notices = %#v", imported.Ignored)
	}
}

func TestPreviewFRPCTunnelImportRedactsCredentialsAndPreservesOptionalFields(t *testing.T) {
	imported, err := ParseFRPCTunnelImport(serverImportSource)
	if err != nil {
		t.Fatalf("ParseFRPCTunnelImport() error = %v", err)
	}
	preview := PreviewFRPCTunnelImport(imported)
	if len(preview.Candidates) != 4 || preview.Candidates[0].BasicAuth == nil || preview.Candidates[0].BasicAuth.Username != "operator" || !preview.Candidates[0].BasicAuth.PasswordConfigured {
		t.Fatalf("preview = %#v", preview)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("json.Marshal(preview) error = %v", err)
	}
	if strings.Contains(string(encoded), "secret-value") {
		t.Fatalf("preview leaked Basic Auth password: %s", encoded)
	}
	var document struct {
		Candidates []map[string]any `json:"candidates"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal(preview) error = %v", err)
	}
	if document.Candidates[0]["location"] != "/api" || document.Candidates[0]["serverPort"] != nil || document.Candidates[0]["basicAuth"].(map[string]any)["password"] != nil {
		t.Fatalf("HTTP preview document = %#v", document.Candidates[0])
	}
	if _, found := document.Candidates[2]["location"]; found {
		t.Fatalf("TCP preview unexpectedly has a location: %#v", document.Candidates[2])
	}
	if document.Candidates[2]["serverPort"] != float64(20001) || document.Candidates[2]["basicAuth"] != nil {
		t.Fatalf("TCP preview document = %#v", document.Candidates[2])
	}
	catchAll, err := ParseFRPCTunnelImport(`
[[proxies]]
name = "catch-all"
type = "http"
localPort = 3000
customDomains = ["catch.example.com"]
`)
	if err != nil {
		t.Fatalf("ParseFRPCTunnelImport(catch-all) error = %v", err)
	}
	encoded, err = json.Marshal(PreviewFRPCTunnelImport(catchAll))
	if err != nil {
		t.Fatalf("json.Marshal(catch-all preview) error = %v", err)
	}
	if err := json.Unmarshal(encoded, &document); err != nil || len(document.Candidates) != 1 || document.Candidates[0]["location"] != nil {
		t.Fatalf("catch-all preview document = (%#v, %v)", document, err)
	}
}

func TestServerControlPlaneImportsSelectedFRPCTOMLCandidatesInOneTransaction(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	ctx := context.Background()
	client, err := plane.CreateClient(ctx, "environment-admin", "Import target")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	events := make([]ServerControlPlaneEvent, 0)
	stop := plane.Subscribe(func(event ServerControlPlaneEvent) { events = append(events, event) })
	t.Cleanup(stop)
	created, err := plane.ImportFRPCTunnels(ctx, client.ID, serverImportSource, []string{"proxy-0-location-0", "proxy-0-location-1", "proxy-1", "proxy-2"})
	if err != nil {
		t.Fatalf("ImportFRPCTunnels() error = %v", err)
	}
	if len(created) != 4 {
		t.Fatalf("imported tunnel count = %d, want 4", len(created))
	}
	for _, tunnel := range created {
		if tunnel.Enabled {
			t.Fatalf("imported tunnel is enabled: %#v", tunnel)
		}
	}
	if created[0].Options.HTTP == nil || created[0].Options.HTTP.BasicAuth == nil || created[0].Options.HTTP.BasicAuth.Password != "secret-value" {
		t.Fatalf("imported HTTP tunnel = %#v", created[0])
	}
	if created[2].ServerPort == nil || *created[2].ServerPort != 20001 || created[3].ServerPort == nil || *created[3].ServerPort != 20002 {
		t.Fatalf("imported port tunnels = (%#v, %#v)", created[2], created[3])
	}
	if current, err := plane.GetClient(ctx, client.ID); err != nil || current.DesiredRevision != 1 {
		t.Fatalf("client after import = (%#v, %v)", current, err)
	}
	if len(events) != 1 || events[0].Type != serverDesiredState || events[0].ClientID != client.ID || events[0].OwnerAccountID != "environment-admin" {
		t.Fatalf("import events = %#v", events)
	}
}

func TestServerControlPlaneImportFRPCTunnelsRejectsSelectionsAndRollsBackReservations(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	ctx := context.Background()
	client, err := plane.CreateClient(ctx, "environment-admin", "Import target")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	events := make([]ServerControlPlaneEvent, 0)
	stop := plane.Subscribe(func(event ServerControlPlaneEvent) { events = append(events, event) })
	t.Cleanup(stop)
	assertServerDomainCode(t, func() error {
		_, err := plane.ImportFRPCTunnels(ctx, client.ID, serverImportSource, []string{"proxy-1", "proxy-1"})
		return err
	}(), "INVALID_TUNNEL")
	assertServerDomainCode(t, func() error {
		_, err := plane.ImportFRPCTunnels(ctx, client.ID, serverImportSource, []string{"missing"})
		return err
	}(), "INVALID_TUNNEL")
	if current, err := plane.GetClient(ctx, client.ID); err != nil || current.DesiredRevision != 0 || len(events) != 0 {
		t.Fatalf("client after duplicate selection = (%#v, %v), events %#v", current, err, events)
	}
	location := "/api"
	if _, err := plane.CreateTunnel(ctx, client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"app.example.com"}, Location: &location, LocalPort: 3000}); err != nil {
		t.Fatalf("CreateTunnel(conflict) error = %v", err)
	}
	events = nil
	assertServerDomainCode(t, func() error {
		_, err := plane.ImportFRPCTunnels(ctx, client.ID, serverImportSource, []string{"proxy-1", "proxy-0-location-0"})
		return err
	}(), "RESOURCE_RESERVED")
	if current, err := plane.GetClient(ctx, client.ID); err != nil || current.DesiredRevision != 1 {
		t.Fatalf("client after rejected import = (%#v, %v)", current, err)
	}
	tunnels, err := plane.ListTunnels(ctx, client.ID)
	if err != nil || len(tunnels) != 1 || tunnels[0].Location == nil || *tunnels[0].Location != location || len(events) != 0 {
		t.Fatalf("tunnels after rejected import = (%#v, %v), events %#v", tunnels, err, events)
	}
}

func hasTunnelImportNotice(notices []TunnelImportNotice, proxy, reason string) bool {
	for _, notice := range notices {
		if notice.Proxy == proxy && notice.Reason == reason {
			return true
		}
	}
	return false
}
