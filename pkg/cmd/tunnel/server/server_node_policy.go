package server

import (
	"encoding/json"
	"fmt"
)

type nodeFieldPolicy struct {
	Mode  string `json:"mode"`
	Value string `json:"value,omitempty"`
}

type nodeConfigurationPolicy struct {
	Version int                        `json:"version"`
	Fields  map[string]nodeFieldPolicy `json:"fields"`
}

func defaultNodeConfigurationPolicy() nodeConfigurationPolicy {
	return nodeConfigurationPolicy{Version: 1, Fields: map[string]nodeFieldPolicy{
		"custom404Page": {Mode: "inherit"},
	}}
}

func parseNodeConfigurationPolicy(raw string) (nodeConfigurationPolicy, error) {
	var policy nodeConfigurationPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil || policy.Version != 1 || policy.Fields == nil {
		return policy, fmt.Errorf("invalid Node configuration policy")
	}
	if _, err := resolveNodeField(policy.Fields["custom404Page"], ""); err != nil {
		return policy, err
	}
	return policy, nil
}

func resolveNodeField(field nodeFieldPolicy, inherited string) (string, error) {
	switch field.Mode {
	case "inherit":
		if field.Value != "" {
			return "", fmt.Errorf("inherited Node field has an override")
		}
		return inherited, nil
	case "custom":
		return field.Value, nil
	case "default":
		if field.Value != "" {
			return "", fmt.Errorf("default Node field has an override")
		}
		return "", nil
	default:
		return "", fmt.Errorf("invalid Node field mode %q", field.Mode)
	}
}

func policyForNodeSettings(settings serverNodeSettings) (nodeConfigurationPolicy, error) {
	mode := settings.Custom404PageMode
	if mode == "" {
		mode = "inherit"
		if settings.Custom404Page != "" {
			mode = "custom"
		}
	}
	field := nodeFieldPolicy{Mode: mode}
	if mode == "custom" {
		field.Value = settings.Custom404Page
	} else if settings.Custom404Page != "" {
		return nodeConfigurationPolicy{}, serverDomainError("INVALID_NODE_SETTINGS", "Custom 404 content requires custom mode")
	}
	if _, err := resolveNodeField(field, ""); err != nil {
		return nodeConfigurationPolicy{}, serverDomainError("INVALID_NODE_SETTINGS", "Custom 404 mode is invalid")
	}
	policy := defaultNodeConfigurationPolicy()
	policy.Fields["custom404Page"] = field
	return policy, nil
}
