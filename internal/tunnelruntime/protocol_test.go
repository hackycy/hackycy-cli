package tunnelruntime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWireTargetMapsEverySupportedGoTarget(t *testing.T) {
	for _, test := range []struct {
		goos, goarch string
		platform     WirePlatform
		architecture WireArchitecture
	}{
		{"darwin", "amd64", WirePlatformDarwin, WireArchitectureX64},
		{"darwin", "arm64", WirePlatformDarwin, WireArchitectureARM64},
		{"linux", "amd64", WirePlatformLinux, WireArchitectureX64},
		{"linux", "arm64", WirePlatformLinux, WireArchitectureARM64},
		{"windows", "amd64", WirePlatformWin32, WireArchitectureX64},
		{"windows", "arm64", WirePlatformWin32, WireArchitectureARM64},
	} {
		t.Run(test.goos+"/"+test.goarch, func(t *testing.T) {
			target, err := WireTargetForGo(test.goos, test.goarch)
			if err != nil || target.Platform != test.platform || target.Architecture != test.architecture {
				t.Fatalf("WireTargetForGo() = (%#v, %v)", target, err)
			}
			goos, goarch, err := target.GoTarget()
			if err != nil || goos != test.goos || goarch != test.goarch {
				t.Fatalf("GoTarget() = (%q, %q, %v)", goos, goarch, err)
			}
		})
	}
}

func TestWireTargetRejectsUnsupportedValues(t *testing.T) {
	if _, err := WireTargetForGo("freebsd", "amd64"); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("WireTargetForGo(freebsd) error = %v", err)
	}
	if _, err := WireTargetForGo("linux", "386"); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("WireTargetForGo(linux/386) error = %v", err)
	}
	if _, _, err := (WireTarget{Platform: "win32", Architecture: "386"}).GoTarget(); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("GoTarget(unknown architecture) error = %v", err)
	}
}

func TestProtocolV3MessagesRetainExactWireFields(t *testing.T) {
	httpDefinition := TunnelDefinition{
		ID: "tunnel-id", Label: "HTTP", Protocol: TunnelProtocolHTTP,
		CustomDomains: []string{"example.test"}, LocalHost: "127.0.0.1", LocalPort: 3000,
		Enabled: true, Options: protocolTestOptions(), CreatedAt: "2026-08-24T00:00:00.000Z", UpdatedAt: "2026-08-24T00:00:00.000Z",
	}
	welcome := AgentWelcome{
		Type: "welcome", TunnelProtocolVersion: TunnelProtocolVersion, RequiredFRPVersion: "0.70.1",
		Artifact:          FRPArtifactDescription{Version: "0.70.1", Archive: "frp.tar.gz", URL: "https://example.test/frp.tar.gz", SHA256: strings.Repeat("a", 64), FRPCSHA256: strings.Repeat("b", 64)},
		AdvertisedFRPHost: "tunnel.example", AdvertisedFRPPort: 7000, InternalFRPToken: "secret", Snapshot: TunnelSnapshot{ClientKey: "client-id", Revision: 2, Tunnels: []TunnelDefinition{httpDefinition}},
	}
	encoded, err := json.Marshal(welcome)
	if err != nil {
		t.Fatalf("marshal welcome: %v", err)
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &message); err != nil {
		t.Fatalf("unmarshal welcome map: %v", err)
	}
	for _, field := range []string{"type", "tunnelProtocolVersion", "requiredFrpVersion", "artifact", "advertisedFrpHost", "advertisedFrpPort", "internalFrpToken", "snapshot"} {
		if _, found := message[field]; !found {
			t.Fatalf("welcome omitted %q: %s", field, encoded)
		}
	}
	var snapshot struct {
		Tunnels []map[string]json.RawMessage `json:"tunnels"`
	}
	if err := json.Unmarshal(message["snapshot"], &snapshot); err != nil || len(snapshot.Tunnels) != 1 {
		t.Fatalf("decode snapshot = (%#v, %v)", snapshot, err)
	}
	if string(snapshot.Tunnels[0]["location"]) != "null" || string(snapshot.Tunnels[0]["serverPort"]) != "null" {
		t.Fatalf("HTTP tunnel null fields = %s", encoded)
	}
	if string(snapshot.Tunnels[0]["customDomains"]) != `["example.test"]` {
		t.Fatalf("HTTP tunnel custom domains = %s", encoded)
	}
}

func TestProtocolV3ToleratesUnknownFieldsAndOmitsAbsentPortFields(t *testing.T) {
	var hello AgentHello
	if err := json.Unmarshal([]byte(`{"type":"hello","tunnelProtocolVersion":3,"ycyVersion":"0.0.0-dev","platform":"win32","architecture":"x64","lastAppliedRevision":0,"futureField":true}`), &hello); err != nil {
		t.Fatalf("unmarshal hello with future field: %v", err)
	}
	if hello.Platform != "win32" || hello.Architecture != "x64" || hello.TunnelProtocolVersion != TunnelProtocolVersion {
		t.Fatalf("hello = %#v", hello)
	}
	port := int64(20000)
	encoded, err := json.Marshal(TunnelDefinition{
		ID: "tunnel-id", Protocol: TunnelProtocolTCP, ServerPort: &port, LocalHost: "127.0.0.1", LocalPort: 8080, Options: protocolTestOptions(),
	})
	if err != nil {
		t.Fatalf("marshal port tunnel: %v", err)
	}
	if strings.Contains(string(encoded), "customDomains") || strings.Contains(string(encoded), "location") || !strings.Contains(string(encoded), `"serverPort":20000`) {
		t.Fatalf("port tunnel wire form = %s", encoded)
	}
}

func protocolTestOptions() TunnelOptions {
	return TunnelOptions{
		Transport:   TunnelTransportOptions{},
		HealthCheck: nil,
		HTTP:        nil,
	}
}
