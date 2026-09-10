package tunnelruntime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var ErrInvalidFRPConfiguration = errors.New("invalid FRP configuration")

// FRPServerConfiguration contains only the typed values needed to render frps.
type FRPServerConfiguration struct {
	BindAddress      string
	BindPort         int64
	VhostHTTPPort    int64
	Custom404Page    string
	InternalFRPToken string
	PortRangeStart   int64
	PortRangeEnd     int64
	LogLevel         string
}

// FRPClientConfiguration contains one authenticated v3 snapshot for frpc.
type FRPClientConfiguration struct {
	AdvertisedFRPHost string
	AdvertisedFRPPort int64
	InternalFRPToken  string
	Snapshot          TunnelSnapshot
	LogLevel          string
}

type frpAuthenticationTOML struct {
	Method string `toml:"method"`
	Token  string `toml:"token"`
}

type frpLogTOML struct {
	To    string `toml:"to"`
	Level string `toml:"level"`
}

type frpPortRangeTOML struct {
	Start int64 `toml:"start"`
	End   int64 `toml:"end"`
}

type frpsTOML struct {
	BindAddr      string                `toml:"bindAddr"`
	BindPort      int64                 `toml:"bindPort"`
	VhostHTTPPort int64                 `toml:"vhostHTTPPort"`
	Custom404Page string                `toml:"custom404Page"`
	Auth          frpAuthenticationTOML `toml:"auth"`
	AllowPorts    []frpPortRangeTOML    `toml:"allowPorts"`
	Log           frpLogTOML            `toml:"log"`
}

type frpTransportTOML struct {
	BandwidthLimit       string `toml:"bandwidthLimit,omitempty"`
	BandwidthLimitMode   string `toml:"bandwidthLimitMode,omitempty"`
	UseEncryption        bool   `toml:"useEncryption,omitempty"`
	UseCompression       bool   `toml:"useCompression,omitempty"`
	ProxyProtocolVersion string `toml:"proxyProtocolVersion,omitempty"`
}

type frpHealthCheckTOML struct {
	Type            string          `toml:"type"`
	TimeoutSeconds  int64           `toml:"timeoutSeconds"`
	MaxFailed       int64           `toml:"maxFailed"`
	IntervalSeconds int64           `toml:"intervalSeconds"`
	Path            string          `toml:"path,omitempty"`
	HTTPHeaders     []frpHeaderTOML `toml:"httpHeaders,omitempty"`
}

type frpHeaderTOML struct {
	Name  string `toml:"name"`
	Value string `toml:"value"`
}

type frpHeaderSetTOML struct {
	Set map[string]string `toml:"set"`
}

type frpProxyTOML struct {
	Name              string              `toml:"name"`
	Type              TunnelProtocol      `toml:"type"`
	LocalIP           string              `toml:"localIP"`
	LocalPort         int64               `toml:"localPort"`
	RemotePort        *int64              `toml:"remotePort,omitempty"`
	CustomDomains     []string            `toml:"customDomains,omitempty"`
	Locations         []string            `toml:"locations,omitempty"`
	HTTPUser          string              `toml:"httpUser,omitempty"`
	HTTPPassword      string              `toml:"httpPassword,omitempty"`
	HostHeaderRewrite string              `toml:"hostHeaderRewrite,omitempty"`
	Transport         *frpTransportTOML   `toml:"transport,omitempty"`
	HealthCheck       *frpHealthCheckTOML `toml:"healthCheck,omitempty"`
	RequestHeaders    *frpHeaderSetTOML   `toml:"requestHeaders,omitempty"`
	ResponseHeaders   *frpHeaderSetTOML   `toml:"responseHeaders,omitempty"`
}

type frpcTOML struct {
	ServerAddr    string                `toml:"serverAddr"`
	ServerPort    int64                 `toml:"serverPort"`
	User          string                `toml:"user"`
	LoginFailExit bool                  `toml:"loginFailExit"`
	Auth          frpAuthenticationTOML `toml:"auth"`
	Log           frpLogTOML            `toml:"log"`
	Proxies       []frpProxyTOML        `toml:"proxies,omitempty"`
}

// RenderFRPSConfig serializes only the selected server FRP fields.
func RenderFRPSConfig(configuration FRPServerConfiguration) (string, error) {
	document := frpsTOML{
		BindAddr: configuration.BindAddress, BindPort: configuration.BindPort, VhostHTTPPort: configuration.VhostHTTPPort,
		Custom404Page: configuration.Custom404Page,
		Auth:          frpAuthenticationTOML{Method: "token", Token: configuration.InternalFRPToken},
		AllowPorts:    []frpPortRangeTOML{{Start: configuration.PortRangeStart, End: configuration.PortRangeEnd}},
		Log:           frpLogTOML{To: "console", Level: defaultFRPLogLevel(configuration.LogLevel)},
	}
	encoded, err := toml.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("marshal frps TOML: %w", err)
	}
	return string(encoded), nil
}

// RenderFRPCConfig serializes enabled snapshot definitions as typed FRP proxies.
func RenderFRPCConfig(configuration FRPClientConfiguration) (string, error) {
	proxies := make([]frpProxyTOML, 0, len(configuration.Snapshot.Tunnels))
	for _, definition := range configuration.Snapshot.Tunnels {
		if !definition.Enabled {
			continue
		}
		proxy, err := buildFRPProxy(definition)
		if err != nil {
			return "", err
		}
		proxies = append(proxies, proxy)
	}
	document := frpcTOML{
		ServerAddr: configuration.AdvertisedFRPHost, ServerPort: configuration.AdvertisedFRPPort,
		User:          "ycy_" + frpIdentifier(configuration.Snapshot.ClientKey),
		LoginFailExit: false,
		Auth:          frpAuthenticationTOML{Method: "token", Token: configuration.InternalFRPToken},
		Log:           frpLogTOML{To: "console", Level: defaultFRPLogLevel(configuration.LogLevel)},
		Proxies:       proxies,
	}
	encoded, err := toml.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("marshal frpc TOML: %w", err)
	}
	return string(encoded), nil
}

func buildFRPProxy(definition TunnelDefinition) (frpProxyTOML, error) {
	proxy := frpProxyTOML{
		Name: "t_" + frpIdentifier(definition.ID), Type: definition.Protocol,
		LocalIP: definition.LocalHost, LocalPort: definition.LocalPort,
		Transport:   buildFRPTransport(definition.Options.Transport),
		HealthCheck: buildFRPHealthCheck(definition.Options.HealthCheck),
	}
	switch definition.Protocol {
	case TunnelProtocolTCP, TunnelProtocolUDP:
		if definition.ServerPort == nil {
			return frpProxyTOML{}, fmt.Errorf("%w: %s tunnel %q has no server port", ErrInvalidFRPConfiguration, definition.Protocol, definition.ID)
		}
		proxy.RemotePort = definition.ServerPort
		return proxy, nil
	case TunnelProtocolHTTP:
		if definition.Options.HTTP == nil {
			return frpProxyTOML{}, fmt.Errorf("%w: HTTP tunnel %q has no HTTP options", ErrInvalidFRPConfiguration, definition.ID)
		}
		proxy.CustomDomains = append([]string(nil), definition.CustomDomains...)
		if definition.Location != nil {
			proxy.Locations = []string{*definition.Location}
		}
		http := definition.Options.HTTP
		if http.BasicAuth != nil {
			proxy.HTTPUser = http.BasicAuth.Username
			proxy.HTTPPassword = http.BasicAuth.Password
		}
		if http.HostHeaderRewrite != nil {
			proxy.HostHeaderRewrite = *http.HostHeaderRewrite
		}
		proxy.RequestHeaders = buildFRPHeaderSet(http.RequestHeaders)
		proxy.ResponseHeaders = buildFRPHeaderSet(http.ResponseHeaders)
		return proxy, nil
	default:
		return frpProxyTOML{}, fmt.Errorf("%w: unsupported proxy type %q", ErrInvalidFRPConfiguration, definition.Protocol)
	}
}

func buildFRPTransport(options TunnelTransportOptions) *frpTransportTOML {
	transport := &frpTransportTOML{
		UseEncryption: options.UseEncryption, UseCompression: options.UseCompression,
	}
	if options.BandwidthLimit != nil {
		transport.BandwidthLimit = fmt.Sprintf("%g%s", options.BandwidthLimit.Value, options.BandwidthLimit.Unit)
		transport.BandwidthLimitMode = options.BandwidthLimit.Mode
	}
	if options.ProxyProtocolVersion != nil {
		transport.ProxyProtocolVersion = *options.ProxyProtocolVersion
	}
	if transport.BandwidthLimit == "" && !transport.UseEncryption && !transport.UseCompression && transport.ProxyProtocolVersion == "" {
		return nil
	}
	return transport
}

func buildFRPHealthCheck(check *TunnelHealthCheck) *frpHealthCheckTOML {
	if check == nil {
		return nil
	}
	result := &frpHealthCheckTOML{
		Type: check.Type, TimeoutSeconds: check.TimeoutSeconds, MaxFailed: check.MaxFailed, IntervalSeconds: check.IntervalSeconds,
	}
	if check.Path != nil {
		result.Path = *check.Path
	}
	if len(check.Headers) > 0 {
		result.HTTPHeaders = make([]frpHeaderTOML, 0, len(check.Headers))
		for _, header := range check.Headers {
			result.HTTPHeaders = append(result.HTTPHeaders, frpHeaderTOML{Name: header.Name, Value: header.Value})
		}
	}
	return result
}

func buildFRPHeaderSet(headers []TunnelHeader) *frpHeaderSetTOML {
	if len(headers) == 0 {
		return nil
	}
	set := make(map[string]string, len(headers))
	for _, header := range headers {
		set[header.Name] = header.Value
	}
	return &frpHeaderSetTOML{Set: set}
}

func defaultFRPLogLevel(level string) string {
	if level == "" {
		return "warn"
	}
	return level
}

func frpIdentifier(value string) string {
	return strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			return character
		}
		return '_'
	}, value)
}
