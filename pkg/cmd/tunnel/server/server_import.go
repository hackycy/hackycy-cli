package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type TunnelImportNotice struct {
	Proxy  string `json:"proxy,omitempty"`
	Reason string `json:"reason"`
}

type TunnelImportCandidate struct {
	ID    string
	Input TunnelMutationInput
}

type FRPCTunnelImport struct {
	Candidates []TunnelImportCandidate
	Ignored    []TunnelImportNotice
}

type TunnelImportPreview struct {
	Candidates []TunnelImportPreviewCandidate `json:"candidates"`
	Ignored    []TunnelImportNotice           `json:"ignored"`
}

type TunnelImportPreviewCandidate struct {
	ID            string                        `json:"id"`
	Label         string                        `json:"label"`
	Protocol      tunnelruntime.TunnelProtocol  `json:"protocol"`
	CustomDomains []string                      `json:"customDomains,omitempty"`
	Location      *tunnelImportPreviewLocation  `json:"location,omitempty"`
	ServerPort    *int64                        `json:"serverPort,omitempty"`
	LocalHost     string                        `json:"localHost"`
	LocalPort     int64                         `json:"localPort"`
	BasicAuth     *TunnelImportPreviewBasicAuth `json:"basicAuth"`
}

type TunnelImportPreviewBasicAuth struct {
	Username           string `json:"username"`
	PasswordConfigured bool   `json:"passwordConfigured"`
}

type tunnelImportPreviewLocation struct {
	value *string
}

func (location tunnelImportPreviewLocation) MarshalJSON() ([]byte, error) {
	return json.Marshal(location.value)
}

var tunnelImportBandwidthPattern = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*(KB|MB)\s*$`)

// ParseFRPCTunnelImport maps the supported FRP v1 proxy subset into typed
// candidates. It performs no persistence; callers must reparse before a later
// selected import transaction.
func ParseFRPCTunnelImport(source string) (FRPCTunnelImport, error) {
	var document map[string]any
	if err := toml.Unmarshal([]byte(source), &document); err != nil {
		return FRPCTunnelImport{}, serverDomainError("INVALID_CONFIG", "Tunnel configuration must be valid TOML")
	}
	proxies, ok := tunnelImportArray(document["proxies"])
	if !ok {
		return FRPCTunnelImport{}, serverDomainError("INVALID_CONFIG", "Tunnel configuration must contain a proxies array")
	}
	result := FRPCTunnelImport{Candidates: []TunnelImportCandidate{}, Ignored: []TunnelImportNotice{}}
	for _, field := range []string{"serverAddr", "serverPort", "user", "loginFailExit", "auth", "log"} {
		if _, found := document[field]; found {
			result.Ignored = append(result.Ignored, TunnelImportNotice{Reason: "Client connection settings are not imported"})
			break
		}
	}
	for index, value := range proxies {
		proxy, ok := tunnelImportTable(value)
		if !ok {
			result.Ignored = append(result.Ignored, TunnelImportNotice{Proxy: tunnelImportFallbackLabel(index), Reason: "Ignored proxy because it is not a TOML table"})
			continue
		}
		result.Candidates = append(result.Candidates, tunnelImportProxyCandidates(proxy, index, &result.Ignored)...)
	}
	return result, nil
}

// PreviewFRPCTunnelImport projects parsed candidates without their recoverable
// Basic Auth passwords. It never reparses source text or mutates state.
func PreviewFRPCTunnelImport(imported FRPCTunnelImport) TunnelImportPreview {
	preview := TunnelImportPreview{
		Candidates: make([]TunnelImportPreviewCandidate, 0, len(imported.Candidates)),
		Ignored:    append([]TunnelImportNotice(nil), imported.Ignored...),
	}
	for _, candidate := range imported.Candidates {
		input := candidate.Input
		view := TunnelImportPreviewCandidate{ID: candidate.ID, Protocol: input.Protocol, LocalPort: input.LocalPort}
		if input.Label != nil {
			view.Label = *input.Label
		}
		if input.LocalHost != nil {
			view.LocalHost = *input.LocalHost
		} else {
			view.LocalHost = "127.0.0.1"
		}
		if input.Protocol == tunnelruntime.TunnelProtocolHTTP {
			view.CustomDomains = append([]string(nil), input.CustomDomains...)
			location := tunnelImportPreviewLocation{}
			if input.Location != nil {
				value := *input.Location
				location.value = &value
			}
			view.Location = &location
		} else if input.ServerPort != nil {
			serverPort := *input.ServerPort
			view.ServerPort = &serverPort
		}
		if input.Options != nil && input.Options.HTTP != nil && input.Options.HTTP.BasicAuth != nil {
			view.BasicAuth = &TunnelImportPreviewBasicAuth{Username: input.Options.HTTP.BasicAuth.Username, PasswordConfigured: true}
		}
		preview.Candidates = append(preview.Candidates, view)
	}
	return preview
}

func (plane *ServerControlPlane) ImportFRPCTunnels(ctx context.Context, clientID, source string, candidateIDs []string) ([]tunnelruntime.TunnelDefinition, error) {
	imported, err := ParseFRPCTunnelImport(source)
	if err != nil {
		return nil, err
	}
	inputs, err := selectFRPCTunnelImportCandidates(imported, candidateIDs)
	if err != nil {
		return nil, err
	}
	result, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (struct {
		tunnels []tunnelruntime.TunnelDefinition
		owner   string
	}, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return struct {
				tunnels []tunnelruntime.TunnelDefinition
				owner   string
			}{}, err
		}
		created := make([]tunnelruntime.TunnelDefinition, 0, len(inputs))
		for _, input := range inputs {
			if input.Protocol != tunnelruntime.TunnelProtocolHTTP && input.ServerPort == nil {
				return struct {
					tunnels []tunnelruntime.TunnelDefinition
					owner   string
				}{}, serverDomainError("INVALID_TUNNEL", "Imported TCP and UDP tunnels require a server port")
			}
			tunnelID, err := randomUUID(plane.random)
			if err != nil {
				return struct {
					tunnels []tunnelruntime.TunnelDefinition
					owner   string
				}{}, fmt.Errorf("generate imported Tunnel Definition ID: %w", err)
			}
			disabled := false
			input.Enabled = &disabled
			values, err := plane.tunnelValues(ctx, connection, input)
			if err != nil {
				return struct {
					tunnels []tunnelruntime.TunnelDefinition
					owner   string
				}{}, err
			}
			if err := insertTunnel(ctx, connection, tunnelID, clientID, values, formatServerTimestamp(plane.now())); err != nil {
				return struct {
					tunnels []tunnelruntime.TunnelDefinition
					owner   string
				}{}, mapTunnelConstraintError(err)
			}
			tunnel, err := selectTunnel(ctx, connection, tunnelID)
			if err != nil {
				return struct {
					tunnels []tunnelruntime.TunnelDefinition
					owner   string
				}{}, err
			}
			created = append(created, tunnel.TunnelDefinition)
		}
		if err := incrementDesiredRevision(ctx, connection, clientID); err != nil {
			return struct {
				tunnels []tunnelruntime.TunnelDefinition
				owner   string
			}{}, err
		}
		return struct {
			tunnels []tunnelruntime.TunnelDefinition
			owner   string
		}{tunnels: created, owner: client.OwnerAccountID}, nil
	})
	if err != nil {
		return nil, err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverDesiredState, ClientID: clientID, OwnerAccountID: result.owner})
	return result.tunnels, nil
}

func selectFRPCTunnelImportCandidates(imported FRPCTunnelImport, candidateIDs []string) ([]TunnelMutationInput, error) {
	if len(candidateIDs) == 0 {
		return nil, serverDomainError("INVALID_TUNNEL", "Select at least one tunnel configuration to import")
	}
	candidates := make(map[string]TunnelImportCandidate, len(imported.Candidates))
	for _, candidate := range imported.Candidates {
		candidates[candidate.ID] = candidate
	}
	selected := make(map[string]struct{}, len(candidateIDs))
	inputs := make([]TunnelMutationInput, 0, len(candidateIDs))
	for _, candidateID := range candidateIDs {
		if _, found := selected[candidateID]; found {
			return nil, serverDomainError("INVALID_TUNNEL", "Tunnel configuration selection contains duplicates")
		}
		selected[candidateID] = struct{}{}
		candidate, found := candidates[candidateID]
		if !found {
			return nil, serverDomainError("INVALID_TUNNEL", "Tunnel configuration selection is no longer valid")
		}
		inputs = append(inputs, candidate.Input)
	}
	return inputs, nil
}

func tunnelImportProxyCandidates(proxy map[string]any, index int, notices *[]TunnelImportNotice) []TunnelImportCandidate {
	label := tunnelImportProxyLabel(proxy, index)
	protocol, _ := tunnelImportString(proxy["type"])
	protocol = strings.ToLower(protocol)
	if protocol != string(tunnelruntime.TunnelProtocolHTTP) && protocol != string(tunnelruntime.TunnelProtocolTCP) && protocol != string(tunnelruntime.TunnelProtocolUDP) {
		reason := "Ignored unsupported proxy type"
		if protocol != "" {
			reason += ": " + protocol
		}
		tunnelImportNotice(notices, label, reason)
		return nil
	}
	if utf16CodeUnitCount(label) > 100 {
		tunnelImportNotice(notices, label, "Ignored proxy because its name is longer than 100 characters")
		return nil
	}
	localHost := "127.0.0.1"
	if value, found := proxy["localIP"]; found {
		localHost, _ = tunnelImportString(value)
		localHost = strings.TrimSpace(localHost)
	}
	localPort, validLocalPort := tunnelImportInteger(proxy["localPort"])
	if localHost == "" || !validLocalPort || localPort < 1 || localPort > 65535 {
		tunnelImportNotice(notices, label, "Ignored proxy because its local endpoint is incomplete or invalid")
		return nil
	}
	tunnelImportUnsupportedFields(proxy, label, notices)
	transport := tunnelImportTransport(proxy["transport"], tunnelImportHas(proxy, "transport"), label, notices)
	healthCheck := tunnelImportHealthCheck(proxy["healthCheck"], tunnelImportHas(proxy, "healthCheck"), label, notices)
	var httpOptions *TunnelHTTPOptionsInput
	if protocol == string(tunnelruntime.TunnelProtocolHTTP) {
		httpOptions = tunnelImportHTTPOptions(proxy, label, notices)
	}
	options := &TunnelOptionsInput{Transport: transport, HealthCheck: healthCheck, HTTP: httpOptions}
	if options.Transport == nil && options.HealthCheck == nil && options.HTTP == nil {
		options = nil
	}
	localHostValue := localHost
	enabled := false
	labelValue := label
	base := TunnelMutationInput{
		Protocol:  tunnelruntime.TunnelProtocol(protocol),
		LocalHost: &localHostValue,
		LocalPort: localPort,
		Enabled:   &enabled,
		Label:     &labelValue,
		Options:   options,
	}
	if protocol != string(tunnelruntime.TunnelProtocolHTTP) {
		serverPort, validServerPort := tunnelImportInteger(proxy["remotePort"])
		if !validServerPort || serverPort < 1 || serverPort > 65535 {
			tunnelImportNotice(notices, label, "Ignored proxy because its remote port is missing or invalid")
			return nil
		}
		base.ServerPort = &serverPort
		return []TunnelImportCandidate{{ID: "proxy-" + strconv.Itoa(index), Input: base}}
	}
	customDomains, validDomains := tunnelImportStrings(proxy["customDomains"])
	if !validDomains || len(customDomains) == 0 {
		tunnelImportNotice(notices, label, "Ignored HTTP proxy because it has no custom domains")
		return nil
	}
	locations, validLocations := tunnelImportLocations(proxy)
	if !validLocations {
		tunnelImportNotice(notices, label, "Ignored HTTP proxy because its locations are invalid")
		return nil
	}
	candidates := make([]TunnelImportCandidate, 0, len(locations))
	for locationIndex, location := range locations {
		input := base
		input.CustomDomains = append([]string(nil), customDomains...)
		input.Location = location
		candidates = append(candidates, TunnelImportCandidate{ID: "proxy-" + strconv.Itoa(index) + "-location-" + strconv.Itoa(locationIndex), Input: input})
	}
	return candidates
}

func tunnelImportTransport(value any, present bool, proxy string, notices *[]TunnelImportNotice) *TunnelTransportOptionsInput {
	if !present {
		return nil
	}
	source, ok := tunnelImportTable(value)
	if !ok {
		tunnelImportNotice(notices, proxy, "Transport settings were ignored because they are not a TOML table")
		return nil
	}
	result := &TunnelTransportOptionsInput{}
	for _, field := range []string{"useEncryption", "useCompression"} {
		if value, found := source[field]; found {
			boolean, ok := value.(bool)
			if !ok {
				tunnelImportNotice(notices, proxy, field+" was ignored because it is not a boolean")
				continue
			}
			if field == "useEncryption" {
				result.UseEncryption = &boolean
			} else {
				result.UseCompression = &boolean
			}
		}
	}
	if bandwidthLimit, found := source["bandwidthLimit"]; found {
		value, ok := tunnelImportString(bandwidthLimit)
		match := tunnelImportBandwidthPattern.FindStringSubmatch(value)
		if !ok || match == nil {
			tunnelImportNotice(notices, proxy, "Bandwidth limit was ignored because it is not a positive KB or MB value")
		} else {
			amount, err := strconv.ParseFloat(match[1], 64)
			if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
				tunnelImportNotice(notices, proxy, "Bandwidth limit was ignored because it is not a positive KB or MB value")
			} else {
				mode := "client"
				validMode := true
				if modeValue, configured := source["bandwidthLimitMode"]; configured {
					if configuredMode, recognizedMode := tunnelImportString(modeValue); recognizedMode && (configuredMode == "client" || configuredMode == "server") {
						mode = configuredMode
					} else {
						tunnelImportNotice(notices, proxy, "Bandwidth limit was ignored because its mode is not client or server")
						validMode = false
					}
				}
				if validMode {
					result.BandwidthLimit = &tunnelruntime.TunnelBandwidthLimit{Value: amount, Unit: match[2], Mode: mode}
				}
			}
		}
	} else if tunnelImportHas(source, "bandwidthLimitMode") {
		tunnelImportNotice(notices, proxy, "Bandwidth limit mode was ignored because no bandwidth limit is configured")
	}

	if proxyProtocolVersion, found := source["proxyProtocolVersion"]; found {
		version, validVersion := tunnelImportString(proxyProtocolVersion)
		if !validVersion || (version != "v1" && version != "v2") {
			tunnelImportNotice(notices, proxy, "Proxy Protocol version was ignored because it is not v1 or v2")
		} else {
			result.ProxyProtocolVersion = &version
		}
	}
	return tunnelImportEmptyTransport(result)
}

func tunnelImportEmptyTransport(input *TunnelTransportOptionsInput) *TunnelTransportOptionsInput {
	if input.UseEncryption == nil && input.UseCompression == nil && input.BandwidthLimit == nil && input.ProxyProtocolVersion == nil {
		return nil
	}
	return input
}

func tunnelImportHealthCheck(value any, present bool, proxy string, notices *[]TunnelImportNotice) *TunnelHealthCheckInput {
	if !present {
		return nil
	}
	source, ok := tunnelImportTable(value)
	if !ok {
		tunnelImportNotice(notices, proxy, "Health check was ignored because it is not a TOML table")
		return nil
	}
	intervalSeconds, validInterval := tunnelImportInteger(source["intervalSeconds"])
	timeoutSeconds, validTimeout := tunnelImportInteger(source["timeoutSeconds"])
	maxFailed, validMaxFailed := tunnelImportInteger(source["maxFailed"])
	typeValue, _ := tunnelImportString(source["type"])
	if !validInterval || !validTimeout || !validMaxFailed || intervalSeconds == 0 || timeoutSeconds == 0 || maxFailed == 0 || (typeValue != "tcp" && typeValue != "http") {
		tunnelImportNotice(notices, proxy, "Health check was ignored because it is incomplete or unsupported")
		return nil
	}
	if typeValue == "tcp" {
		return &TunnelHealthCheckInput{Type: typeValue, IntervalSeconds: intervalSeconds, TimeoutSeconds: timeoutSeconds, MaxFailed: maxFailed}
	}
	path, validPath := tunnelImportString(source["path"])
	if !validPath || !strings.HasPrefix(path, "/") {
		tunnelImportNotice(notices, proxy, "HTTP health check was ignored because it has no valid path")
		return nil
	}
	headers := tunnelImportHealthHeaders(source["httpHeaders"], tunnelImportHas(source, "httpHeaders"), proxy, notices)
	return &TunnelHealthCheckInput{Type: typeValue, Path: &path, IntervalSeconds: intervalSeconds, TimeoutSeconds: timeoutSeconds, MaxFailed: maxFailed, Headers: headers}
}

func tunnelImportHTTPOptions(source map[string]any, proxy string, notices *[]TunnelImportNotice) *TunnelHTTPOptionsInput {
	result := &TunnelHTTPOptionsInput{}
	username, usernameIsString := tunnelImportString(source["httpUser"])
	password, passwordIsString := tunnelImportString(source["httpPassword"])
	if tunnelImportHas(source, "httpUser") || tunnelImportHas(source, "httpPassword") {
		if usernameIsString && strings.TrimSpace(username) != "" && passwordIsString && password != "" {
			result.BasicAuth = &tunnelruntime.TunnelBasicAuth{Username: username, Password: password}
		} else {
			tunnelImportNotice(notices, proxy, "HTTP Basic Auth was ignored because both username and password are required")
		}
	}
	if hostHeaderRewrite, found := source["hostHeaderRewrite"]; found {
		value, validValue := tunnelImportString(hostHeaderRewrite)
		value = strings.TrimSpace(value)
		if !validValue || value == "" {
			tunnelImportNotice(notices, proxy, "Host Header Rewrite was ignored because it is not a non-empty string")
		} else {
			result.HostHeaderRewrite = &value
		}
	}
	if requestHeaders := tunnelImportHeaderSet(source["requestHeaders"], tunnelImportHas(source, "requestHeaders"), proxy, "Request headers", notices); requestHeaders != nil {
		result.RequestHeaders = requestHeaders
	}
	if responseHeaders := tunnelImportHeaderSet(source["responseHeaders"], tunnelImportHas(source, "responseHeaders"), proxy, "Response headers", notices); responseHeaders != nil {
		result.ResponseHeaders = responseHeaders
	}
	if result.BasicAuth == nil && result.HostHeaderRewrite == nil && result.RequestHeaders == nil && result.ResponseHeaders == nil {
		return nil
	}
	return result
}

func tunnelImportHeaderSet(value any, present bool, proxy, label string, notices *[]TunnelImportNotice) []tunnelruntime.TunnelHeader {
	if !present {
		return nil
	}
	container, ok := tunnelImportTable(value)
	if !ok {
		tunnelImportNotice(notices, proxy, label+" were ignored because they are not a string set")
		return nil
	}
	set, ok := tunnelImportTable(container["set"])
	if !ok {
		tunnelImportNotice(notices, proxy, label+" were ignored because they are not a string set")
		return nil
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	headers := make([]tunnelruntime.TunnelHeader, 0, len(names))
	ignored := false
	for _, name := range names {
		value, ok := tunnelImportString(set[name])
		if !ok {
			ignored = true
			continue
		}
		headers = append(headers, tunnelruntime.TunnelHeader{Name: name, Value: value})
	}
	if ignored {
		tunnelImportNotice(notices, proxy, "Some "+strings.ToLower(label)+" were ignored because their values are not strings")
	}
	return headers
}

func tunnelImportHealthHeaders(value any, present bool, proxy string, notices *[]TunnelImportNotice) []tunnelruntime.TunnelHeader {
	if !present {
		return nil
	}
	entries, ok := tunnelImportArray(value)
	if !ok {
		tunnelImportNotice(notices, proxy, "Health check headers were ignored because they are not an array")
		return nil
	}
	headers := make([]tunnelruntime.TunnelHeader, 0, len(entries))
	ignored := false
	for _, entry := range entries {
		header, ok := tunnelImportTable(entry)
		if !ok {
			ignored = true
			continue
		}
		name, validName := tunnelImportString(header["name"])
		value, validValue := tunnelImportString(header["value"])
		if !validName || !validValue {
			ignored = true
			continue
		}
		headers = append(headers, tunnelruntime.TunnelHeader{Name: name, Value: value})
	}
	if ignored {
		tunnelImportNotice(notices, proxy, "Some health check headers were ignored because they are invalid")
	}
	return headers
}

func tunnelImportUnsupportedFields(proxy map[string]any, label string, notices *[]TunnelImportNotice) {
	supported := map[string]struct{}{
		"name": {}, "type": {}, "localIP": {}, "localPort": {}, "remotePort": {}, "customDomains": {}, "locations": {}, "transport": {}, "healthCheck": {}, "httpUser": {}, "httpPassword": {}, "hostHeaderRewrite": {}, "requestHeaders": {}, "responseHeaders": {},
	}
	unsupported := make([]string, 0)
	for field := range proxy {
		if _, found := supported[field]; !found {
			unsupported = append(unsupported, field)
		}
	}
	if len(unsupported) == 0 {
		return
	}
	sort.Strings(unsupported)
	tunnelImportNotice(notices, label, "Ignored unsupported fields: "+strings.Join(unsupported, ", "))
}

func tunnelImportLocations(proxy map[string]any) ([]*string, bool) {
	value, present := proxy["locations"]
	if !present {
		return []*string{nil}, true
	}
	locations, ok := tunnelImportStrings(value)
	if !ok {
		return nil, false
	}
	if len(locations) == 0 {
		return []*string{nil}, true
	}
	result := make([]*string, 0, len(locations))
	for _, location := range locations {
		if !strings.HasPrefix(location, "/") || containsWhitespace(location) {
			return nil, false
		}
		value := location
		result = append(result, &value)
	}
	return result, true
}

func tunnelImportStrings(value any) ([]string, bool) {
	values, ok := tunnelImportArray(value)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		stringValue, ok := tunnelImportString(value)
		if !ok {
			return nil, false
		}
		result = append(result, stringValue)
	}
	return result, true
}

func tunnelImportTable(value any) (map[string]any, bool) {
	reflectValue := reflect.ValueOf(value)
	if !reflectValue.IsValid() || reflectValue.Kind() != reflect.Map || reflectValue.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	table := make(map[string]any, reflectValue.Len())
	iterator := reflectValue.MapRange()
	for iterator.Next() {
		table[iterator.Key().String()] = iterator.Value().Interface()
	}
	return table, true
}

func tunnelImportArray(value any) ([]any, bool) {
	reflectValue := reflect.ValueOf(value)
	if !reflectValue.IsValid() || reflectValue.Kind() != reflect.Slice {
		return nil, false
	}
	values := make([]any, reflectValue.Len())
	for index := range values {
		values[index] = reflectValue.Index(index).Interface()
	}
	return values, true
}

func tunnelImportInteger(value any) (int64, bool) {
	integer, ok := value.(int64)
	if !ok || integer < -9_007_199_254_740_991 || integer > 9_007_199_254_740_991 {
		return 0, false
	}
	return integer, true
}

func tunnelImportString(value any) (string, bool) {
	stringValue, ok := value.(string)
	return stringValue, ok
}

func tunnelImportHas(values map[string]any, key string) bool {
	_, found := values[key]
	return found
}

func tunnelImportProxyLabel(proxy map[string]any, index int) string {
	label, _ := tunnelImportString(proxy["name"])
	label = strings.TrimSpace(label)
	if label == "" {
		return tunnelImportFallbackLabel(index)
	}
	return label
}

func tunnelImportFallbackLabel(index int) string {
	return "Proxy " + strconv.Itoa(index+1)
}

func tunnelImportNotice(notices *[]TunnelImportNotice, proxy, reason string) {
	*notices = append(*notices, TunnelImportNotice{Proxy: proxy, Reason: reason})
}
