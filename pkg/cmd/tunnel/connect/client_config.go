package connect

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"golang.org/x/net/idna"
)

// DefaultTunnelServer is intentionally empty in migration builds.
const DefaultTunnelServer = ""

var explicitControlPlaneScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// ClientOptionInput retains whether each future CLI option was provided. A nil
// field defers to its environment source, while an explicit empty value fails.
type ClientOptionInput struct {
	Server *string
	Token  *string
}

// ClientEnvironment distinguishes an absent environment variable from an
// explicitly empty value.
type ClientEnvironment func(string) (string, bool)

// ClientConnectionReader is the narrow configuration dependency needed while
// resolving the remembered Tunnel connection catalog.
type ClientConnectionReader interface {
	ReadTunnelConnections() ([]appconfig.TunnelConnection, error)
}

// ClientConnectionSelector presents ambiguous remembered candidates. A true
// cancelled result ends resolution normally without creating instance state.
type ClientConnectionSelector func(context.Context, []appconfig.TunnelConnection) (selectedID string, cancelled bool, err error)

// ClientResolutionOptions supplies process dependencies without making the
// unregistered client own terminal presentation or instance resources yet.
type ClientResolutionOptions struct {
	Reader           ClientConnectionReader
	Environment      ClientEnvironment
	DefaultServer    string
	ReadFile         func(string) ([]byte, error)
	AbsolutePath     func(string) (string, error)
	SelectConnection ClientConnectionSelector
}

// ClientConfig is the resolved connection pair. Instance identity and state
// ownership are intentionally added by the next Tunnel client slice.
type ClientConfig struct {
	Server *url.URL
	Token  string
}

// ResolvedClientConfig records whether a successful welcome should update the
// remembered catalog. Resolution itself does not write configuration.
type ResolvedClientConfig struct {
	Config                   ClientConfig
	RememberOnAuthentication bool
}

// ResolveClientConfig applies the retained field-aware server/token precedence
// and remembered-pair selection without starting a client instance.
func ResolveClientConfig(ctx context.Context, input ClientOptionInput, options ClientResolutionOptions) (*ResolvedClientConfig, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Reader == nil {
		return nil, fmt.Errorf("Tunnel connection catalog is required")
	}
	if options.Environment == nil {
		options.Environment = os.LookupEnv
	}
	if options.ReadFile == nil {
		options.ReadFile = os.ReadFile
	}
	if options.AbsolutePath == nil {
		options.AbsolutePath = filepath.Abs
	}

	connections, err := options.Reader.ReadTunnelConnections()
	if err != nil {
		return nil, fmt.Errorf("read remembered Tunnel connections: %w", err)
	}

	rawServer, serverProvided := clientOptionValue(input.Server, options.Environment, "YCY_TUNNEL_SERVER")
	if serverProvided && strings.TrimSpace(rawServer) == "" {
		return nil, fmt.Errorf("Control plane must not be empty")
	}
	var server *url.URL
	if serverProvided {
		server, err = normalizeControlPlaneURL(strings.TrimSpace(rawServer))
		if err != nil {
			return nil, err
		}
	}

	rawToken, tokenProvided := clientOptionValue(input.Token, options.Environment, "YCY_TUNNEL_TOKEN")
	if !tokenProvided {
		if tokenPath, configured := options.Environment("YCY_TUNNEL_TOKEN_FILE"); configured && tokenPath != "" {
			absolutePath, pathErr := options.AbsolutePath(tokenPath)
			if pathErr != nil {
				return nil, fmt.Errorf("Could not read Client Token file: %w", pathErr)
			}
			contents, readErr := options.ReadFile(absolutePath)
			if readErr != nil {
				return nil, fmt.Errorf("Could not read Client Token file: %w", readErr)
			}
			rawToken = string(contents)
			tokenProvided = true
		}
	}
	token := strings.TrimSpace(rawToken)
	if tokenProvided && token == "" {
		return nil, fmt.Errorf("Client Token must not be empty")
	}

	if server != nil && token == "" {
		selected, cancelled, selectErr := selectClientConnection(ctx, matchingServerConnections(connections, server.String()), options.SelectConnection)
		if selectErr != nil {
			return nil, selectErr
		}
		if cancelled {
			return nil, nil
		}
		if selected != nil {
			token = selected.Token
		}
	} else if server == nil && token != "" {
		candidates := matchingTokenConnections(connections, token)
		if len(candidates) == 0 {
			candidates = tokenRotationCandidates(connections, token)
		}
		if len(candidates) > 0 {
			selected, cancelled, selectErr := selectClientConnection(ctx, candidates, options.SelectConnection)
			if selectErr != nil {
				return nil, selectErr
			}
			if cancelled {
				return nil, nil
			}
			if selected != nil {
				server, err = normalizeControlPlaneURL(selected.Server)
				if err != nil {
					return nil, fmt.Errorf("remembered Tunnel connection is invalid: %w", err)
				}
			}
		} else if strings.TrimSpace(options.DefaultServer) != "" {
			server, err = normalizeControlPlaneURL(strings.TrimSpace(options.DefaultServer))
			if err != nil {
				return nil, err
			}
		}
	} else if server == nil && token == "" && len(connections) > 0 {
		selected, cancelled, selectErr := selectClientConnection(ctx, connections, options.SelectConnection)
		if selectErr != nil {
			return nil, selectErr
		}
		if cancelled {
			return nil, nil
		}
		if selected != nil {
			server, err = normalizeControlPlaneURL(selected.Server)
			if err != nil {
				return nil, fmt.Errorf("remembered Tunnel connection is invalid: %w", err)
			}
			token = selected.Token
		}
	}

	if server == nil && strings.TrimSpace(options.DefaultServer) != "" {
		server, err = normalizeControlPlaneURL(strings.TrimSpace(options.DefaultServer))
		if err != nil {
			return nil, err
		}
	}
	if server == nil {
		return nil, fmt.Errorf("Control plane is required through --server, YCY_TUNNEL_SERVER, a remembered connection, or DEFAULT_TUNNEL_SERVER")
	}
	if token == "" {
		return nil, fmt.Errorf("Client Token is required through --token, YCY_TUNNEL_TOKEN, YCY_TUNNEL_TOKEN_FILE, or a matching remembered connection")
	}

	remembered := false
	for _, connection := range connections {
		if connection.Server == server.String() && connection.Token == token {
			remembered = true
			break
		}
	}
	return &ResolvedClientConfig{
		Config:                   ClientConfig{Server: server, Token: token},
		RememberOnAuthentication: input.Token != nil || remembered,
	}, nil
}

func clientOptionValue(value *string, environment ClientEnvironment, name string) (string, bool) {
	if value != nil {
		return *value, true
	}
	return environment(name)
}

func matchingServerConnections(connections []appconfig.TunnelConnection, server string) []appconfig.TunnelConnection {
	matches := make([]appconfig.TunnelConnection, 0, len(connections))
	for _, connection := range connections {
		if connection.Server == server {
			matches = append(matches, connection)
		}
	}
	return matches
}

func matchingTokenConnections(connections []appconfig.TunnelConnection, token string) []appconfig.TunnelConnection {
	matches := make([]appconfig.TunnelConnection, 0, len(connections))
	for _, connection := range connections {
		if connection.Token == token {
			matches = append(matches, connection)
		}
	}
	return matches
}

func tokenRotationCandidates(connections []appconfig.TunnelConnection, token string) []appconfig.TunnelConnection {
	seenServers := make(map[string]struct{}, len(connections))
	candidates := make([]appconfig.TunnelConnection, 0, len(connections))
	for _, connection := range connections {
		if _, seen := seenServers[connection.Server]; seen {
			continue
		}
		seenServers[connection.Server] = struct{}{}
		connection.ID = fmt.Sprintf("rotation:%d", len(candidates))
		connection.Token = token
		candidates = append(candidates, connection)
	}
	return candidates
}

func selectClientConnection(ctx context.Context, connections []appconfig.TunnelConnection, selectConnection ClientConnectionSelector) (*appconfig.TunnelConnection, bool, error) {
	if len(connections) == 0 {
		return nil, false, nil
	}
	if len(connections) == 1 {
		return &connections[0], false, nil
	}
	if selectConnection == nil {
		return nil, false, fmt.Errorf("Multiple remembered tunnel connections match; provide both --server and --token in a non-interactive environment")
	}
	selectedID, cancelled, err := selectConnection(ctx, connections)
	if err != nil {
		return nil, false, err
	}
	if cancelled {
		return nil, true, nil
	}
	for index := range connections {
		if connections[index].ID == selectedID {
			return &connections[index], false, nil
		}
	}
	return nil, false, fmt.Errorf("The selected remembered tunnel connection is unavailable")
}

func normalizeControlPlaneURL(value string) (*url.URL, error) {
	if !explicitControlPlaneScheme.MatchString(value) {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("Control plane must be a valid HTTP or HTTPS address")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || parsed.Opaque != "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, fmt.Errorf("Control plane must be an HTTP or HTTPS origin without credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("Control plane must not include a path")
	}
	host, err := normalizeControlPlaneHost(parsed, scheme)
	if err != nil {
		return nil, fmt.Errorf("Control plane must be a valid HTTP or HTTPS address")
	}
	return &url.URL{Scheme: scheme, Host: host}, nil
}

func normalizeControlPlaneHost(parsed *url.URL, scheme string) (string, error) {
	hostname := parsed.Hostname()
	if hostname == "" {
		return "", fmt.Errorf("missing host")
	}
	port := parsed.Port()
	if controlPlaneHostHasInvalidPort(parsed.Host, hostname, port) {
		return "", fmt.Errorf("invalid port")
	}
	if port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return "", err
		}
	}

	normalizedHost := ""
	isIPv6 := false
	if ip := net.ParseIP(hostname); ip != nil {
		normalizedHost = ip.String()
		isIPv6 = strings.Contains(normalizedHost, ":")
	} else {
		asciiHost, err := idna.Lookup.ToASCII(hostname)
		if err != nil || asciiHost == "" {
			return "", fmt.Errorf("invalid host")
		}
		normalizedHost = strings.ToLower(asciiHost)
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(normalizedHost, port), nil
	}
	if isIPv6 {
		return "[" + normalizedHost + "]", nil
	}
	return normalizedHost, nil
}

func controlPlaneHostHasInvalidPort(host, hostname, port string) bool {
	if strings.HasPrefix(host, "[") {
		closing := strings.LastIndex(host, "]")
		if closing < 1 {
			return true
		}
		rest := host[closing+1:]
		return rest != "" && (rest[0] != ':' || port == "")
	}
	return host != hostname && port == ""
}
