package tunnelruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
)

const TunnelProtocolVersion = 3

var ErrUnsupportedPlatform = errors.New("unsupported Tunnel platform")

// WirePlatform and WireArchitecture retain the protocol-v3 vocabulary rather
// than exposing raw GOOS and GOARCH values to a peer.
type WirePlatform string
type WireArchitecture string

const (
	WirePlatformDarwin WirePlatform = "darwin"
	WirePlatformLinux  WirePlatform = "linux"
	WirePlatformWin32  WirePlatform = "win32"

	WireArchitectureX64   WireArchitecture = "x64"
	WireArchitectureARM64 WireArchitecture = "arm64"
)

// WireTarget identifies one protocol-v3 and FRP target.
type WireTarget struct {
	Platform     WirePlatform
	Architecture WireArchitecture
}

// WireTargetForGo maps one Go target to the protocol-v3 wire vocabulary.
func WireTargetForGo(goos, goarch string) (WireTarget, error) {
	platform, found := map[string]WirePlatform{
		"darwin":  WirePlatformDarwin,
		"linux":   WirePlatformLinux,
		"windows": WirePlatformWin32,
	}[goos]
	if !found {
		return WireTarget{}, fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, goos, goarch)
	}
	architecture, found := map[string]WireArchitecture{
		"amd64": WireArchitectureX64,
		"arm64": WireArchitectureARM64,
	}[goarch]
	if !found {
		return WireTarget{}, fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, goos, goarch)
	}
	return WireTarget{Platform: platform, Architecture: architecture}, nil
}

// GoTarget maps a protocol-v3 target back to its Go target.
func (target WireTarget) GoTarget() (string, string, error) {
	goos, found := map[WirePlatform]string{
		WirePlatformDarwin: "darwin",
		WirePlatformLinux:  "linux",
		WirePlatformWin32:  "windows",
	}[target.Platform]
	if !found {
		return "", "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, target.Platform, target.Architecture)
	}
	goarch, found := map[WireArchitecture]string{
		WireArchitectureX64:   "amd64",
		WireArchitectureARM64: "arm64",
	}[target.Architecture]
	if !found {
		return "", "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, target.Platform, target.Architecture)
	}
	return goos, goarch, nil
}

// CurrentWireTarget reports the current executable's protocol-v3 target.
func CurrentWireTarget() (WireTarget, error) {
	return WireTargetForGo(runtime.GOOS, runtime.GOARCH)
}

type TunnelProtocol string

const (
	TunnelProtocolHTTP TunnelProtocol = "http"
	TunnelProtocolTCP  TunnelProtocol = "tcp"
	TunnelProtocolUDP  TunnelProtocol = "udp"
)

type FRPProcessState string

const (
	FRPProcessStopped             FRPProcessState = "stopped"
	FRPProcessRunning             FRPProcessState = "running"
	FRPProcessRecovering          FRPProcessState = "recovering"
	FRPProcessConfigurationFailed FRPProcessState = "configuration_failed"
)

type TunnelHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TunnelBandwidthLimit struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
	Mode  string  `json:"mode"`
}

type TunnelTransportOptions struct {
	UseEncryption        bool                  `json:"useEncryption"`
	UseCompression       bool                  `json:"useCompression"`
	BandwidthLimit       *TunnelBandwidthLimit `json:"bandwidthLimit"`
	ProxyProtocolVersion *string               `json:"proxyProtocolVersion"`
}

type TunnelHealthCheck struct {
	Type            string         `json:"type"`
	Path            *string        `json:"path,omitempty"`
	IntervalSeconds int64          `json:"intervalSeconds"`
	TimeoutSeconds  int64          `json:"timeoutSeconds"`
	MaxFailed       int64          `json:"maxFailed"`
	Headers         []TunnelHeader `json:"headers,omitempty"`
}

type TunnelBasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type TunnelHTTPOptions struct {
	BasicAuth         *TunnelBasicAuth `json:"basicAuth"`
	HostHeaderRewrite *string          `json:"hostHeaderRewrite"`
	RequestHeaders    []TunnelHeader   `json:"requestHeaders"`
	ResponseHeaders   []TunnelHeader   `json:"responseHeaders"`
}

type TunnelOptions struct {
	Transport   TunnelTransportOptions `json:"transport"`
	HealthCheck *TunnelHealthCheck     `json:"healthCheck"`
	HTTP        *TunnelHTTPOptions     `json:"http"`
}

// TunnelDefinition is the protocol-v3 snapshot shape. Validation and database
// ownership remain with the later server-domain slice.
type TunnelDefinition struct {
	ID            string         `json:"id"`
	Label         string         `json:"label"`
	Protocol      TunnelProtocol `json:"protocol"`
	CustomDomains []string       `json:"customDomains,omitempty"`
	Location      *string        `json:"location,omitempty"`
	ServerPort    *int64         `json:"serverPort,omitempty"`
	LocalHost     string         `json:"localHost"`
	LocalPort     int64          `json:"localPort"`
	Enabled       bool           `json:"enabled"`
	Options       TunnelOptions  `json:"options"`
	CreatedAt     string         `json:"createdAt"`
	UpdatedAt     string         `json:"updatedAt"`
}

func (definition TunnelDefinition) MarshalJSON() ([]byte, error) {
	type common struct {
		ID        string         `json:"id"`
		Label     string         `json:"label"`
		Protocol  TunnelProtocol `json:"protocol"`
		LocalHost string         `json:"localHost"`
		LocalPort int64          `json:"localPort"`
		Enabled   bool           `json:"enabled"`
		Options   TunnelOptions  `json:"options"`
		CreatedAt string         `json:"createdAt"`
		UpdatedAt string         `json:"updatedAt"`
	}
	base := common{
		ID: definition.ID, Label: definition.Label, Protocol: definition.Protocol,
		LocalHost: definition.LocalHost, LocalPort: definition.LocalPort, Enabled: definition.Enabled,
		Options: definition.Options, CreatedAt: definition.CreatedAt, UpdatedAt: definition.UpdatedAt,
	}
	if definition.Protocol == TunnelProtocolHTTP {
		return json.Marshal(struct {
			common
			CustomDomains []string `json:"customDomains"`
			Location      *string  `json:"location"`
			ServerPort    *int64   `json:"serverPort"`
		}{base, definition.CustomDomains, definition.Location, definition.ServerPort})
	}
	return json.Marshal(struct {
		common
		ServerPort *int64 `json:"serverPort"`
	}{base, definition.ServerPort})
}

type TunnelSnapshot struct {
	ClientKey string             `json:"clientKey"`
	Revision  int64              `json:"revision"`
	Tunnels   []TunnelDefinition `json:"tunnels"`
}

type FRPArtifactDescription struct {
	Version    string `json:"version"`
	Archive    string `json:"archive"`
	URL        string `json:"url"`
	SHA256     string `json:"sha256"`
	FRPCSHA256 string `json:"frpcSha256"`
}

type StructuredRuntimeError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Revision *int64 `json:"revision,omitempty"`
}

type AgentHello struct {
	Type                  string `json:"type"`
	TunnelProtocolVersion int    `json:"tunnelProtocolVersion"`
	YCYVersion            string `json:"ycyVersion"`
	Platform              string `json:"platform"`
	Architecture          string `json:"architecture"`
	LastAppliedRevision   int64  `json:"lastAppliedRevision"`
}

type AgentWelcome struct {
	Type                  string                 `json:"type"`
	TunnelProtocolVersion int                    `json:"tunnelProtocolVersion"`
	RequiredFRPVersion    string                 `json:"requiredFrpVersion"`
	Artifact              FRPArtifactDescription `json:"artifact"`
	AdvertisedFRPHost     string                 `json:"advertisedFrpHost"`
	AdvertisedFRPPort     int64                  `json:"advertisedFrpPort"`
	InternalFRPToken      string                 `json:"internalFrpToken"`
	Snapshot              TunnelSnapshot         `json:"snapshot"`
}

type DesiredState struct {
	Type                  string         `json:"type"`
	TunnelProtocolVersion int            `json:"tunnelProtocolVersion"`
	Snapshot              TunnelSnapshot `json:"snapshot"`
}

type ApplyResult struct {
	Type                  string                  `json:"type"`
	TunnelProtocolVersion int                     `json:"tunnelProtocolVersion"`
	Revision              int64                   `json:"revision"`
	Success               bool                    `json:"success"`
	Error                 *StructuredRuntimeError `json:"error,omitempty"`
}

type ProcessState struct {
	Type                  string                  `json:"type"`
	TunnelProtocolVersion int                     `json:"tunnelProtocolVersion"`
	State                 FRPProcessState         `json:"state"`
	Error                 *StructuredRuntimeError `json:"error,omitempty"`
}

type RestartFRPC struct {
	Type                  string `json:"type"`
	TunnelProtocolVersion int    `json:"tunnelProtocolVersion"`
}

type Revoke struct {
	Type                  string `json:"type"`
	TunnelProtocolVersion int    `json:"tunnelProtocolVersion"`
	Reason                string `json:"reason"`
}

type Incompatible struct {
	Type                  string `json:"type"`
	TunnelProtocolVersion int    `json:"tunnelProtocolVersion"`
	Message               string `json:"message"`
}
