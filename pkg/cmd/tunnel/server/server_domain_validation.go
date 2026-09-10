package server

import (
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"

	"golang.org/x/net/idna"
)

// ServerDomainError preserves the stable code/message boundary between the
// server domain and its later HTTP adapter.
type ServerDomainError struct {
	Code    string
	Message string
}

func (err *ServerDomainError) Error() string { return err.Message }

var localEndpointHostPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

func normalizeExactHostname(input string) (string, error) {
	candidate := strings.TrimSuffix(strings.TrimSpace(input), ".")
	if candidate == "" || utf16CodeUnitCount(candidate) > 253 || strings.Contains(candidate, "*") || strings.Contains(candidate, "://") || strings.ContainsAny(candidate, "/?#@:[]") || containsWhitespace(candidate) || net.ParseIP(candidate) != nil {
		return "", serverDomainError("INVALID_HOSTNAME", "HTTP Tunnel hostname must be one exact DNS hostname without scheme, path, port, IP address, or wildcard")
	}
	ascii, err := idna.Lookup.ToASCII(candidate)
	ascii = strings.ToLower(ascii)
	if err != nil || ascii == "" || utf16CodeUnitCount(ascii) > 253 {
		return "", serverDomainError("INVALID_HOSTNAME", "HTTP Tunnel hostname is not a valid internationalized DNS hostname")
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", serverDomainError("INVALID_HOSTNAME", "HTTP Tunnel hostname must contain valid DNS labels and a suffix")
	}
	for _, label := range labels {
		if utf16CodeUnitCount(label) == 0 || utf16CodeUnitCount(label) > 63 || !validDNSLabel(label) {
			return "", serverDomainError("INVALID_HOSTNAME", "HTTP Tunnel hostname must contain valid DNS labels and a suffix")
		}
	}
	return ascii, nil
}

func normalizeCustomDomains(domains []string, legacyHostname *string) ([]string, error) {
	if domains != nil && legacyHostname != nil {
		return nil, serverDomainError("INVALID_TUNNEL", "Use customDomains instead of combining it with the legacy hostname field")
	}
	values := domains
	if values == nil && legacyHostname != nil && *legacyHostname != "" {
		values = []string{*legacyHostname}
	}
	if len(values) == 0 {
		return nil, serverDomainError("INVALID_HOSTNAME", "HTTP Tunnel requires at least one custom domain")
	}
	if len(values) > 32 {
		return nil, serverDomainError("INVALID_HOSTNAME", "HTTP Tunnel accepts at most 32 custom domains")
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		hostname, err := normalizeExactHostname(value)
		if err != nil {
			return nil, err
		}
		if _, found := seen[hostname]; found {
			continue
		}
		seen[hostname] = struct{}{}
		normalized = append(normalized, hostname)
	}
	return normalized, nil
}

func normalizeHTTPLocation(input *string) (*string, error) {
	if input == nil {
		return nil, nil
	}
	location := strings.TrimSpace(*input)
	if !strings.HasPrefix(location, "/") || utf16CodeUnitCount(location) > 2048 || strings.ContainsAny(location, "\\?#") || containsWhitespace(location) || containsControlCharacter(location) {
		return nil, serverDomainError("INVALID_HTTP_ROUTE", "HTTP Tunnel location must be a URL path beginning with / and must not contain spaces, query strings, or fragments")
	}
	return &location, nil
}

func normalizeTunnelLabel(input *string) (string, error) {
	label := ""
	if input != nil {
		label = strings.TrimSpace(*input)
	}
	if utf16CodeUnitCount(label) > 100 {
		return "", serverDomainError("INVALID_TUNNEL", "Tunnel display name must contain no more than 100 characters")
	}
	return label, nil
}

func normalizeClientRemark(input string) (string, error) {
	remark := strings.TrimSpace(input)
	if utf16CodeUnitCount(remark) > 100 {
		return "", serverDomainError("INVALID_CLIENT_REMARK", "Client Remark must contain no more than 100 characters")
	}
	return remark, nil
}

func normalizeLocalEndpoint(host *string, port int64) (string, int64, error) {
	localHost := "127.0.0.1"
	if host != nil && strings.TrimSpace(*host) != "" {
		localHost = strings.TrimSpace(*host)
	}
	if utf16CodeUnitCount(localHost) > 253 || !localEndpointHostPattern.MatchString(localHost) {
		return "", 0, serverDomainError("INVALID_LOCAL_ENDPOINT", "Local Endpoint host must be an IP address, hostname, or container service name")
	}
	if port < 1 || port > 65535 {
		return "", 0, serverDomainError("INVALID_LOCAL_ENDPOINT", "Local Endpoint port must be an integer from 1 through 65535")
	}
	return localHost, port, nil
}

func serverDomainError(code, message string) error {
	return &ServerDomainError{Code: code, Message: message}
}

func validDNSLabel(label string) bool {
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, character := range label {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func utf16CodeUnitCount(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func containsWhitespace(value string) bool {
	return strings.IndexFunc(value, unicode.IsSpace) >= 0
}

func containsControlCharacter(value string) bool {
	return strings.IndexFunc(value, func(character rune) bool {
		return character < 0x20 || character == 0x7f
	}) >= 0
}
