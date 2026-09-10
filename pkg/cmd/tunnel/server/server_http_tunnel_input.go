package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"math"
)

// serverHTTPTunnelCreateInput keeps JSON schema decoding in the HTTP adapter;
// the control plane remains responsible for normalized domain validation.
type serverHTTPTunnelCreateInput struct {
	Mutation TunnelMutationInput
}

func (input *serverHTTPTunnelCreateInput) UnmarshalJSON(source []byte) error {
	mutation, err := parseServerHTTPTunnelMutation(source)
	if err != nil {
		return err
	}
	input.Mutation = mutation
	return nil
}

func parseServerHTTPTunnelMutation(source []byte) (TunnelMutationInput, error) {
	object, err := serverTunnelJSONObject(source, "label", "protocol", "customDomains", "hostname", "location", "serverPort", "localHost", "localPort", "enabled", "options")
	if err != nil {
		return TunnelMutationInput{}, err
	}
	protocolValue, err := serverTunnelRequiredString(object, "protocol")
	if err != nil {
		return TunnelMutationInput{}, err
	}
	protocol := tunnelruntime.TunnelProtocol(protocolValue)
	if protocol != tunnelruntime.TunnelProtocolHTTP && protocol != tunnelruntime.TunnelProtocolTCP && protocol != tunnelruntime.TunnelProtocolUDP {
		return TunnelMutationInput{}, fmt.Errorf("protocol must be http, tcp, or udp")
	}
	localPort, err := serverTunnelRequiredInteger(object, "localPort")
	if err != nil {
		return TunnelMutationInput{}, err
	}
	label, err := serverTunnelOptionalString(object, "label", false)
	if err != nil {
		return TunnelMutationInput{}, err
	}
	customDomains, err := serverTunnelOptionalStrings(object, "customDomains")
	if err != nil {
		return TunnelMutationInput{}, err
	}
	hostname, err := serverTunnelOptionalString(object, "hostname", true)
	if err != nil {
		return TunnelMutationInput{}, err
	}
	location, err := serverTunnelOptionalString(object, "location", true)
	if err != nil {
		return TunnelMutationInput{}, err
	}
	serverPort, err := serverTunnelOptionalInteger(object, "serverPort", true)
	if err != nil {
		return TunnelMutationInput{}, err
	}
	localHost, err := serverTunnelOptionalString(object, "localHost", false)
	if err != nil {
		return TunnelMutationInput{}, err
	}
	enabled, err := serverTunnelOptionalBoolean(object, "enabled")
	if err != nil {
		return TunnelMutationInput{}, err
	}
	options, err := serverTunnelOptions(object)
	if err != nil {
		return TunnelMutationInput{}, err
	}
	return TunnelMutationInput{
		Protocol:       protocol,
		CustomDomains:  customDomains,
		LegacyHostname: hostname,
		Location:       location,
		ServerPort:     serverPort,
		LocalHost:      localHost,
		LocalPort:      localPort,
		Enabled:        enabled,
		Label:          label,
		Options:        options,
	}, nil
}

func serverTunnelOptions(parent map[string]json.RawMessage) (*TunnelOptionsInput, error) {
	object, present, err := serverTunnelOptionalObject(parent, "options", false)
	if err != nil || !present {
		return nil, err
	}
	if err := serverTunnelAllowedKeys(object, "transport", "healthCheck", "http"); err != nil {
		return nil, err
	}
	transport, err := serverTunnelTransportOptions(object)
	if err != nil {
		return nil, err
	}
	healthCheck, err := serverTunnelHealthCheck(object)
	if err != nil {
		return nil, err
	}
	httpOptions, err := serverTunnelHTTPOptions(object)
	if err != nil {
		return nil, err
	}
	return &TunnelOptionsInput{Transport: transport, HealthCheck: healthCheck, HTTP: httpOptions}, nil
}

func serverTunnelTransportOptions(parent map[string]json.RawMessage) (*TunnelTransportOptionsInput, error) {
	object, present, err := serverTunnelOptionalObject(parent, "transport", false)
	if err != nil || !present {
		return nil, err
	}
	if err := serverTunnelAllowedKeys(object, "useEncryption", "useCompression", "bandwidthLimit", "proxyProtocolVersion"); err != nil {
		return nil, err
	}
	useEncryption, err := serverTunnelOptionalBoolean(object, "useEncryption")
	if err != nil {
		return nil, err
	}
	useCompression, err := serverTunnelOptionalBoolean(object, "useCompression")
	if err != nil {
		return nil, err
	}
	bandwidth, err := serverTunnelBandwidthLimit(object)
	if err != nil {
		return nil, err
	}
	proxyProtocol, err := serverTunnelOptionalString(object, "proxyProtocolVersion", true)
	if err != nil {
		return nil, err
	}
	if proxyProtocol != nil && *proxyProtocol != "v1" && *proxyProtocol != "v2" {
		return nil, fmt.Errorf("proxyProtocolVersion must be v1 or v2")
	}
	return &TunnelTransportOptionsInput{
		UseEncryption:        useEncryption,
		UseCompression:       useCompression,
		BandwidthLimit:       bandwidth,
		ProxyProtocolVersion: proxyProtocol,
	}, nil
}

func serverTunnelBandwidthLimit(parent map[string]json.RawMessage) (*tunnelruntime.TunnelBandwidthLimit, error) {
	object, present, err := serverTunnelOptionalObject(parent, "bandwidthLimit", true)
	if err != nil || !present || object == nil {
		return nil, err
	}
	if err := serverTunnelAllowedKeys(object, "value", "unit", "mode"); err != nil {
		return nil, err
	}
	value, err := serverTunnelRequiredNumber(object, "value")
	if err != nil {
		return nil, err
	}
	unit, err := serverTunnelRequiredString(object, "unit")
	if err != nil {
		return nil, err
	}
	mode, err := serverTunnelRequiredString(object, "mode")
	if err != nil {
		return nil, err
	}
	if unit != "KB" && unit != "MB" {
		return nil, fmt.Errorf("bandwidth unit must be KB or MB")
	}
	if mode != "client" && mode != "server" {
		return nil, fmt.Errorf("bandwidth mode must be client or server")
	}
	return &tunnelruntime.TunnelBandwidthLimit{Value: value, Unit: unit, Mode: mode}, nil
}

func serverTunnelHealthCheck(parent map[string]json.RawMessage) (*TunnelHealthCheckInput, error) {
	object, present, err := serverTunnelOptionalObject(parent, "healthCheck", true)
	if err != nil || !present || object == nil {
		return nil, err
	}
	typeValue, err := serverTunnelRequiredString(object, "type")
	if err != nil {
		return nil, err
	}
	if typeValue != "tcp" && typeValue != "http" {
		return nil, fmt.Errorf("healthCheck type must be tcp or http")
	}
	allowed := []string{"type", "intervalSeconds", "timeoutSeconds", "maxFailed"}
	if typeValue == "http" {
		allowed = append(allowed, "path", "headers")
	}
	if len(allowed) != 0 {
		if err := serverTunnelAllowedKeys(object, allowed...); err != nil {
			return nil, err
		}
	}
	interval, err := serverTunnelRequiredInteger(object, "intervalSeconds")
	if err != nil {
		return nil, err
	}
	timeout, err := serverTunnelRequiredInteger(object, "timeoutSeconds")
	if err != nil {
		return nil, err
	}
	maxFailed, err := serverTunnelRequiredInteger(object, "maxFailed")
	if err != nil {
		return nil, err
	}
	input := &TunnelHealthCheckInput{Type: typeValue, IntervalSeconds: interval, TimeoutSeconds: timeout, MaxFailed: maxFailed}
	if typeValue == "http" {
		path, err := serverTunnelRequiredString(object, "path")
		if err != nil {
			return nil, err
		}
		headers, err := serverTunnelOptionalHeaders(object, "headers")
		if err != nil {
			return nil, err
		}
		input.Path = &path
		input.Headers = headers
	}
	return input, nil
}

func serverTunnelHTTPOptions(parent map[string]json.RawMessage) (*TunnelHTTPOptionsInput, error) {
	object, present, err := serverTunnelOptionalObject(parent, "http", true)
	if err != nil || !present || object == nil {
		return nil, err
	}
	if err := serverTunnelAllowedKeys(object, "basicAuth", "hostHeaderRewrite", "requestHeaders", "responseHeaders"); err != nil {
		return nil, err
	}
	basicAuth, err := serverTunnelBasicAuth(object)
	if err != nil {
		return nil, err
	}
	hostHeaderRewrite, err := serverTunnelOptionalString(object, "hostHeaderRewrite", true)
	if err != nil {
		return nil, err
	}
	requestHeaders, err := serverTunnelOptionalHeaders(object, "requestHeaders")
	if err != nil {
		return nil, err
	}
	responseHeaders, err := serverTunnelOptionalHeaders(object, "responseHeaders")
	if err != nil {
		return nil, err
	}
	return &TunnelHTTPOptionsInput{
		BasicAuth:         basicAuth,
		HostHeaderRewrite: hostHeaderRewrite,
		RequestHeaders:    requestHeaders,
		ResponseHeaders:   responseHeaders,
	}, nil
}

func serverTunnelBasicAuth(parent map[string]json.RawMessage) (*tunnelruntime.TunnelBasicAuth, error) {
	object, present, err := serverTunnelOptionalObject(parent, "basicAuth", true)
	if err != nil || !present || object == nil {
		return nil, err
	}
	if err := serverTunnelAllowedKeys(object, "username", "password"); err != nil {
		return nil, err
	}
	username, err := serverTunnelRequiredString(object, "username")
	if err != nil {
		return nil, err
	}
	password, err := serverTunnelOptionalString(object, "password", false)
	if err != nil {
		return nil, err
	}
	value := ""
	if password != nil {
		value = *password
	}
	return &tunnelruntime.TunnelBasicAuth{Username: username, Password: value}, nil
}

func serverTunnelOptionalHeaders(parent map[string]json.RawMessage, key string) ([]tunnelruntime.TunnelHeader, error) {
	raw, found := parent[key]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return nil, fmt.Errorf("%s must be an array", key)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("%s must be an array: %w", key, err)
	}
	headers := make([]tunnelruntime.TunnelHeader, 0, len(values))
	for index, value := range values {
		object, err := serverTunnelJSONObject(value, "name", "value")
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", key, index, err)
		}
		name, err := serverTunnelRequiredString(object, "name")
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", key, index, err)
		}
		contents, err := serverTunnelRequiredString(object, "value")
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", key, index, err)
		}
		headers = append(headers, tunnelruntime.TunnelHeader{Name: name, Value: contents})
	}
	return headers, nil
}

func serverTunnelJSONObject(source []byte, allowed ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(source, &object); err != nil || object == nil {
		if err == nil {
			err = fmt.Errorf("expected JSON object")
		}
		return nil, err
	}
	if len(allowed) != 0 {
		if err := serverTunnelAllowedKeys(object, allowed...); err != nil {
			return nil, err
		}
	}
	return object, nil
}

func serverTunnelAllowedKeys(object map[string]json.RawMessage, allowed ...string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		known[key] = struct{}{}
	}
	for key := range object {
		if _, found := known[key]; !found {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return nil
}

func serverTunnelRequiredString(object map[string]json.RawMessage, key string) (string, error) {
	raw, found := object[key]
	if !found || serverTunnelNull(raw) {
		return "", fmt.Errorf("%s is required", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%s must be a string: %w", key, err)
	}
	return value, nil
}

func serverTunnelOptionalString(object map[string]json.RawMessage, key string, nullable bool) (*string, error) {
	raw, found := object[key]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		if nullable {
			return nil, nil
		}
		return nil, fmt.Errorf("%s must be a string", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s must be a string: %w", key, err)
	}
	return &value, nil
}

func serverTunnelOptionalStrings(object map[string]json.RawMessage, key string) ([]string, error) {
	raw, found := object[key]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return nil, fmt.Errorf("%s must be an array", key)
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("%s must be an array of strings: %w", key, err)
	}
	return values, nil
}

func serverTunnelOptionalBoolean(object map[string]json.RawMessage, key string) (*bool, error) {
	raw, found := object[key]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return nil, fmt.Errorf("%s must be a boolean", key)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return &value, nil
}

func serverTunnelRequiredInteger(object map[string]json.RawMessage, key string) (int64, error) {
	raw, found := object[key]
	if !found || serverTunnelNull(raw) {
		return 0, fmt.Errorf("%s is required", key)
	}
	return serverTunnelInteger(raw, key)
}

func serverTunnelOptionalInteger(object map[string]json.RawMessage, key string, nullable bool) (*int64, error) {
	raw, found := object[key]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		if nullable {
			return nil, nil
		}
		return nil, fmt.Errorf("%s must be an integer", key)
	}
	value, err := serverTunnelInteger(raw, key)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func serverTunnelInteger(source []byte, key string) (int64, error) {
	var value float64
	if err := json.Unmarshal(source, &value); err != nil || math.IsInf(value, 0) || math.IsNaN(value) || math.Trunc(value) != value || value < math.MinInt64 || value > math.MaxInt64 {
		if err == nil {
			err = fmt.Errorf("not an integer")
		}
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return int64(value), nil
}

func serverTunnelRequiredNumber(object map[string]json.RawMessage, key string) (float64, error) {
	raw, found := object[key]
	if !found || serverTunnelNull(raw) {
		return 0, fmt.Errorf("%s is required", key)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		if err == nil {
			err = fmt.Errorf("not a number")
		}
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return value, nil
}

func serverTunnelOptionalObject(parent map[string]json.RawMessage, key string, nullable bool) (map[string]json.RawMessage, bool, error) {
	raw, found := parent[key]
	if !found {
		return nil, false, nil
	}
	if serverTunnelNull(raw) {
		if nullable {
			return nil, true, nil
		}
		return nil, true, fmt.Errorf("%s must be an object", key)
	}
	object, err := serverTunnelJSONObject(raw)
	return object, true, err
}

func serverTunnelNull(source []byte) bool {
	return bytes.Equal(bytes.TrimSpace(source), []byte("null"))
}
