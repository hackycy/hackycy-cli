package tunnelruntime

import (
	"errors"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestFRPManifestPinsAllSixOfficialArtifacts(t *testing.T) {
	want := map[WireTarget]struct {
		archive, archiveSHA, frpcSHA, frpsSHA string
	}{
		{WirePlatformDarwin, WireArchitectureARM64}: {"frp_0.70.1_darwin_arm64.tar.gz", "cfa733b5a261c1647edee3c1fc4133d2542989b28f5602e81d47fc821d25c55f", "dced7d6e9c35ecfd5a4625ddf3073660dd28e700387e7d838c64ef3cc1e4c1a9", "5ec9a8d3a25c117b737c9318c3d52805f829a61d8942411bda2f5f11d990416f"},
		{WirePlatformDarwin, WireArchitectureX64}:   {"frp_0.70.1_darwin_amd64.tar.gz", "cbf69cf26e5553e914e97d37f5d4367fa30f5f531d073a889465af4719281e25", "32808dfdf91c4729f3c450d5a46afaa2fc293c8f6ee891743e3ca58685ba7a05", "1bc014d4f52b687c7bb27344273b1ae504ca7a992021feed1e8445b67d981ef6"},
		{WirePlatformLinux, WireArchitectureARM64}:  {"frp_0.70.1_linux_arm64.tar.gz", "3990f396a9a490ee7f0e5f355287750ed41520064ed999eab443b5e9a78d773d", "312be2787dc17c79b68ebf6cc9b536039b2fba595431782c68e3c056c1d491f8", "1930b2cf4ccf7b37834f2c88279d89c2aff5a177ecc307f77c483dbfe1adeb4a"},
		{WirePlatformLinux, WireArchitectureX64}:    {"frp_0.70.1_linux_amd64.tar.gz", "333da23d1b9009d7c01638e9ba38cf4600f7d37d393f854e96ee1396adefa9a6", "7d0270753bd171566a5389d2709fea29d2151f8fb4066ac20947e548e1da193a", "ed1dfde60fd9f6b10237b5ab5953a6d791072c9a378ff9d77d6dfb5f370be482"},
		{WirePlatformWin32, WireArchitectureARM64}:  {"frp_0.70.1_windows_arm64.zip", "74d3acaf0f03ee190dd0462f9b49861dca50b0559c5488af4b36572fc951fcca", "66c6f031d36bed993d0b54ee2f6f834b85d01d8f502c42f62308a4368f5e8936", "29c7b664a6b2b12f0168c72bcca4c9ab19733ca58659cd944cd3b2555c4668df"},
		{WirePlatformWin32, WireArchitectureX64}:    {"frp_0.70.1_windows_amd64.zip", "531f3cd3cc41c0b4f077b54fe6b7dd83c0ff727e7f0bf412a4c78fa279165de5", "1320325b3fd46d83ef7b2161d5e19f2b5dd9341b3391084a58d75ad82ef374d3", "9df8a65fe693de28a8fa4baf7189c44a354a34b94c31f4254e18cc26dea3c57f"},
	}
	artifacts := FRPArtifacts()
	if len(artifacts) != len(want) {
		t.Fatalf("FRP artifacts = %d, want %d", len(artifacts), len(want))
	}
	for _, artifact := range artifacts {
		expected, found := want[artifact.Target]
		if !found || artifact.Description.Version != FRPVersion || artifact.Description.Archive != expected.archive || artifact.Description.SHA256 != expected.archiveSHA || artifact.Description.FRPCSHA256 != expected.frpcSHA || artifact.FRPSSHA256 != expected.frpsSHA {
			t.Fatalf("artifact = %#v", artifact)
		}
		if artifact.Description.URL != frpDownloadBaseURL+"/"+expected.archive {
			t.Fatalf("artifact URL = %q", artifact.Description.URL)
		}
	}
	if _, err := ResolveFRPArtifact(WireTarget{Platform: "freebsd", Architecture: WireArchitectureX64}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("ResolveFRPArtifact(unsupported) error = %v", err)
	}
}

func TestRenderFRPSConfigUsesOnlyTypedFields(t *testing.T) {
	rendered, err := RenderFRPSConfig(FRPServerConfiguration{
		BindAddress: "0.0.0.0", BindPort: 7000, VhostHTTPPort: 8080, Custom404Page: "/state/404.html", InternalFRPToken: "internal-token", PortRangeStart: 20000, PortRangeEnd: 20100, LogLevel: "info",
	})
	if err != nil {
		t.Fatalf("RenderFRPSConfig() error = %v", err)
	}
	for _, field := range []string{"bindAddr", "bindPort", "vhostHTTPPort", "custom404Page", "allowPorts", "method = 'token'", "level = 'info'"} {
		if !strings.Contains(rendered, field) {
			t.Fatalf("frps TOML omitted %q:\n%s", field, rendered)
		}
	}
	var decoded frpsTOML
	if err := toml.Unmarshal([]byte(rendered), &decoded); err != nil || decoded.Auth.Token != "internal-token" || len(decoded.AllowPorts) != 1 || decoded.AllowPorts[0].Start != 20000 {
		t.Fatalf("decode frps TOML = (%#v, %v)", decoded, err)
	}
}

func TestRenderFRPCConfigMapsTypedSnapshotAndCatchAll(t *testing.T) {
	location := "/service"
	proxyProtocol := "v2"
	remotePort := int64(20002)
	rendered, err := RenderFRPCConfig(FRPClientConfiguration{
		AdvertisedFRPHost: "frp.example", AdvertisedFRPPort: 7000, InternalFRPToken: "internal-token", LogLevel: "debug",
		Snapshot: TunnelSnapshot{ClientKey: "client/key", Revision: 4, Tunnels: []TunnelDefinition{
			{ID: "http/id", Protocol: TunnelProtocolHTTP, CustomDomains: []string{"example.test"}, Location: &location, LocalHost: "127.0.0.1", LocalPort: 3000, Enabled: true, Options: TunnelOptions{
				Transport:   TunnelTransportOptions{UseEncryption: true, BandwidthLimit: &TunnelBandwidthLimit{Value: 2.5, Unit: "MB", Mode: "client"}, ProxyProtocolVersion: &proxyProtocol},
				HealthCheck: &TunnelHealthCheck{Type: "http", Path: &location, TimeoutSeconds: 3, MaxFailed: 2, IntervalSeconds: 5, Headers: []TunnelHeader{{Name: "X-Health", Value: "ready"}}},
				HTTP:        &TunnelHTTPOptions{BasicAuth: &TunnelBasicAuth{Username: "user", Password: "password"}, RequestHeaders: []TunnelHeader{{Name: "X-Request", Value: "set"}}, ResponseHeaders: []TunnelHeader{{Name: "X-Response", Value: "set"}}},
			}},
			{ID: "tcp id", Protocol: TunnelProtocolTCP, ServerPort: &remotePort, LocalHost: "127.0.0.1", LocalPort: 5432, Enabled: true, Options: TunnelOptions{Transport: TunnelTransportOptions{}, HTTP: nil}},
			{ID: "disabled", Protocol: TunnelProtocolUDP, ServerPort: &remotePort, LocalHost: "127.0.0.1", LocalPort: 53, Enabled: false, Options: TunnelOptions{Transport: TunnelTransportOptions{}, HTTP: nil}},
		}},
	})
	if err != nil {
		t.Fatalf("RenderFRPCConfig() error = %v", err)
	}
	if strings.Contains(rendered, "disabled") || !strings.Contains(rendered, "t_http_id") || !strings.Contains(rendered, "t_tcp_id") || !strings.Contains(rendered, "locations") || !strings.Contains(rendered, "bandwidthLimit") {
		t.Fatalf("frpc TOML =\n%s", rendered)
	}
	var decoded frpcTOML
	if err := toml.Unmarshal([]byte(rendered), &decoded); err != nil || decoded.User != "ycy_client_key" || len(decoded.Proxies) != 2 {
		t.Fatalf("decode frpc TOML = (%#v, %v)", decoded, err)
	}
	if decoded.Proxies[0].Locations[0] != "/service" || decoded.Proxies[1].RemotePort == nil || *decoded.Proxies[1].RemotePort != remotePort {
		t.Fatalf("decoded proxies = %#v", decoded.Proxies)
	}

	catchAll, err := RenderFRPCConfig(FRPClientConfiguration{Snapshot: TunnelSnapshot{ClientKey: "client", Tunnels: []TunnelDefinition{
		{ID: "catch-all", Protocol: TunnelProtocolHTTP, CustomDomains: []string{"example.test"}, LocalHost: "127.0.0.1", LocalPort: 3000, Enabled: true, Options: TunnelOptions{Transport: TunnelTransportOptions{}, HTTP: &TunnelHTTPOptions{}}},
	}}})
	if err != nil || strings.Contains(catchAll, "locations") {
		t.Fatalf("catch-all TOML = (%q, %v)", catchAll, err)
	}
}

func TestRenderFRPCConfigRejectsIncompleteTypedDefinitions(t *testing.T) {
	_, err := RenderFRPCConfig(FRPClientConfiguration{Snapshot: TunnelSnapshot{Tunnels: []TunnelDefinition{{ID: "tcp", Protocol: TunnelProtocolTCP, Enabled: true}}}})
	if !errors.Is(err, ErrInvalidFRPConfiguration) {
		t.Fatalf("RenderFRPCConfig(incomplete TCP) error = %v", err)
	}
	_, err = RenderFRPCConfig(FRPClientConfiguration{Snapshot: TunnelSnapshot{Tunnels: []TunnelDefinition{{ID: "http", Protocol: TunnelProtocolHTTP, Enabled: true}}}})
	if !errors.Is(err, ErrInvalidFRPConfiguration) {
		t.Fatalf("RenderFRPCConfig(incomplete HTTP) error = %v", err)
	}
}
