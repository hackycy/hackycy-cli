package server

import (
	"encoding/json"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

// serverHTTPTunnelPatchInput keeps JSON omitted-versus-null semantics at the
// HTTP boundary before the control plane applies the field-aware patch.
type serverHTTPTunnelPatchInput struct {
	Patch TunnelPatchInput
}

func (input *serverHTTPTunnelPatchInput) UnmarshalJSON(source []byte) error {
	patch, err := parseServerHTTPTunnelPatch(source)
	if err != nil {
		return err
	}
	input.Patch = patch
	return nil
}

func parseServerHTTPTunnelPatch(source []byte) (TunnelPatchInput, error) {
	object, err := serverTunnelJSONObject(source, "label", "protocol", "customDomains", "hostname", "location", "serverPort", "localHost", "localPort", "enabled", "options")
	if err != nil {
		return TunnelPatchInput{}, err
	}
	protocol, err := serverTunnelPatchProtocol(object)
	if err != nil {
		return TunnelPatchInput{}, err
	}
	customDomains, err := serverTunnelPatchStrings(object, "customDomains")
	if err != nil {
		return TunnelPatchInput{}, err
	}
	hostname, err := serverTunnelPatchNullableString(object, "hostname")
	if err != nil {
		return TunnelPatchInput{}, err
	}
	location, err := serverTunnelPatchNullableString(object, "location")
	if err != nil {
		return TunnelPatchInput{}, err
	}
	serverPort, err := serverTunnelPatchNullableInteger(object, "serverPort")
	if err != nil {
		return TunnelPatchInput{}, err
	}
	localHost, err := serverTunnelOptionalString(object, "localHost", false)
	if err != nil {
		return TunnelPatchInput{}, err
	}
	localPort, err := serverTunnelOptionalInteger(object, "localPort", false)
	if err != nil {
		return TunnelPatchInput{}, err
	}
	enabled, err := serverTunnelOptionalBoolean(object, "enabled")
	if err != nil {
		return TunnelPatchInput{}, err
	}
	label, err := serverTunnelOptionalString(object, "label", false)
	if err != nil {
		return TunnelPatchInput{}, err
	}
	options, err := serverTunnelPatchOptions(object)
	if err != nil {
		return TunnelPatchInput{}, err
	}
	return TunnelPatchInput{
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

func serverTunnelPatchProtocol(parent map[string]json.RawMessage) (*tunnelruntime.TunnelProtocol, error) {
	value, err := serverTunnelOptionalString(parent, "protocol", false)
	if err != nil || value == nil {
		return nil, err
	}
	protocol := tunnelruntime.TunnelProtocol(*value)
	if protocol != tunnelruntime.TunnelProtocolHTTP && protocol != tunnelruntime.TunnelProtocolTCP && protocol != tunnelruntime.TunnelProtocolUDP {
		return nil, fmt.Errorf("protocol must be http, tcp, or udp")
	}
	return &protocol, nil
}

func serverTunnelPatchStrings(parent map[string]json.RawMessage, key string) (*[]string, error) {
	if _, found := parent[key]; !found {
		return nil, nil
	}
	values, err := serverTunnelOptionalStrings(parent, key)
	if err != nil {
		return nil, err
	}
	return &values, nil
}

func serverTunnelPatchNullableString(parent map[string]json.RawMessage, key string) (*TunnelPatchValue[*string], error) {
	raw, found := parent[key]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return &TunnelPatchValue[*string]{}, nil
	}
	value, err := serverTunnelOptionalString(parent, key, false)
	if err != nil {
		return nil, err
	}
	return &TunnelPatchValue[*string]{Value: value}, nil
}

func serverTunnelPatchNullableInteger(parent map[string]json.RawMessage, key string) (*TunnelPatchValue[*int64], error) {
	raw, found := parent[key]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return &TunnelPatchValue[*int64]{}, nil
	}
	value, err := serverTunnelOptionalInteger(parent, key, false)
	if err != nil {
		return nil, err
	}
	return &TunnelPatchValue[*int64]{Value: value}, nil
}

func serverTunnelPatchOptions(parent map[string]json.RawMessage) (*TunnelOptionsPatchInput, error) {
	object, present, err := serverTunnelOptionalObject(parent, "options", false)
	if err != nil || !present {
		return nil, err
	}
	if err := serverTunnelAllowedKeys(object, "transport", "healthCheck", "http"); err != nil {
		return nil, err
	}
	transport, err := serverTunnelPatchTransportOptions(object)
	if err != nil {
		return nil, err
	}
	healthCheck, err := serverTunnelPatchHealthCheck(object)
	if err != nil {
		return nil, err
	}
	httpOptions, err := serverTunnelPatchHTTPOptions(object)
	if err != nil {
		return nil, err
	}
	return &TunnelOptionsPatchInput{Transport: transport, HealthCheck: healthCheck, HTTP: httpOptions}, nil
}

func serverTunnelPatchTransportOptions(parent map[string]json.RawMessage) (*TunnelTransportOptionsPatchInput, error) {
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
	bandwidthLimit, err := serverTunnelPatchBandwidthLimit(object)
	if err != nil {
		return nil, err
	}
	proxyProtocolVersion, err := serverTunnelPatchNullableString(object, "proxyProtocolVersion")
	if err != nil {
		return nil, err
	}
	if proxyProtocolVersion != nil && proxyProtocolVersion.Value != nil && *proxyProtocolVersion.Value != "v1" && *proxyProtocolVersion.Value != "v2" {
		return nil, fmt.Errorf("proxyProtocolVersion must be v1 or v2")
	}
	return &TunnelTransportOptionsPatchInput{
		UseEncryption:        useEncryption,
		UseCompression:       useCompression,
		BandwidthLimit:       bandwidthLimit,
		ProxyProtocolVersion: proxyProtocolVersion,
	}, nil
}

func serverTunnelPatchBandwidthLimit(parent map[string]json.RawMessage) (*TunnelPatchValue[*tunnelruntime.TunnelBandwidthLimit], error) {
	raw, found := parent["bandwidthLimit"]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return &TunnelPatchValue[*tunnelruntime.TunnelBandwidthLimit]{}, nil
	}
	value, err := serverTunnelBandwidthLimit(map[string]json.RawMessage{"bandwidthLimit": raw})
	if err != nil {
		return nil, err
	}
	return &TunnelPatchValue[*tunnelruntime.TunnelBandwidthLimit]{Value: value}, nil
}

func serverTunnelPatchHealthCheck(parent map[string]json.RawMessage) (*TunnelPatchValue[*TunnelHealthCheckInput], error) {
	raw, found := parent["healthCheck"]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return &TunnelPatchValue[*TunnelHealthCheckInput]{}, nil
	}
	value, err := serverTunnelHealthCheck(map[string]json.RawMessage{"healthCheck": raw})
	if err != nil {
		return nil, err
	}
	return &TunnelPatchValue[*TunnelHealthCheckInput]{Value: value}, nil
}

func serverTunnelPatchHTTPOptions(parent map[string]json.RawMessage) (*TunnelPatchValue[*TunnelHTTPOptionsPatchInput], error) {
	raw, found := parent["http"]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return &TunnelPatchValue[*TunnelHTTPOptionsPatchInput]{}, nil
	}
	object, err := serverTunnelJSONObject(raw)
	if err != nil {
		return nil, err
	}
	if err := serverTunnelAllowedKeys(object, "basicAuth", "hostHeaderRewrite", "requestHeaders", "responseHeaders"); err != nil {
		return nil, err
	}
	basicAuth, err := serverTunnelPatchBasicAuth(object)
	if err != nil {
		return nil, err
	}
	hostHeaderRewrite, err := serverTunnelPatchNullableString(object, "hostHeaderRewrite")
	if err != nil {
		return nil, err
	}
	requestHeaders, err := serverTunnelPatchHeaders(object, "requestHeaders")
	if err != nil {
		return nil, err
	}
	responseHeaders, err := serverTunnelPatchHeaders(object, "responseHeaders")
	if err != nil {
		return nil, err
	}
	return &TunnelPatchValue[*TunnelHTTPOptionsPatchInput]{Value: &TunnelHTTPOptionsPatchInput{
		BasicAuth:         basicAuth,
		HostHeaderRewrite: hostHeaderRewrite,
		RequestHeaders:    requestHeaders,
		ResponseHeaders:   responseHeaders,
	}}, nil
}

func serverTunnelPatchBasicAuth(parent map[string]json.RawMessage) (*TunnelPatchValue[*TunnelBasicAuthPatchInput], error) {
	raw, found := parent["basicAuth"]
	if !found {
		return nil, nil
	}
	if serverTunnelNull(raw) {
		return &TunnelPatchValue[*TunnelBasicAuthPatchInput]{}, nil
	}
	object, err := serverTunnelJSONObject(raw)
	if err != nil {
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
	return &TunnelPatchValue[*TunnelBasicAuthPatchInput]{Value: &TunnelBasicAuthPatchInput{Username: username, Password: password}}, nil
}

func serverTunnelPatchHeaders(parent map[string]json.RawMessage, key string) (*[]tunnelruntime.TunnelHeader, error) {
	if _, found := parent[key]; !found {
		return nil, nil
	}
	values, err := serverTunnelOptionalHeaders(parent, key)
	if err != nil {
		return nil, err
	}
	return &values, nil
}
