package appconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
)

type document struct {
	Salt   string
	Fork   forkDocument
	CM     *cmDocument
	Tunnel *tunnelDocument
}

type forkDocument struct {
	Instances map[string]forkDocumentInstance
	order     []string
}

type forkDocumentInstance struct {
	Host   string
	Scheme string
	Type   string
	Token  string
}

type cmDocument struct {
	DefaultProfile string
	Profiles       map[string]cmDocumentProfile
	order          []string
}

type cmDocumentProfile struct {
	BaseURL         string
	Model           string
	APIKey          string
	Temperature     *float64
	TimeoutMS       *int
	MaxOutputTokens *int
}

type tunnelDocument struct {
	Connections map[string]tunnelDocumentConnection
	order       []string
}

type tunnelDocumentConnection struct {
	Server              string
	Token               string
	LastAuthenticatedAt string
}

func (store *Store) readDocument() (document, bool, error) {
	contents, err := os.ReadFile(store.configPath())
	if errors.Is(err, os.ErrNotExist) {
		document, emptyErr := store.emptyDocument()
		return document, false, emptyErr
	}
	if err != nil {
		return document{}, false, fmt.Errorf("read ycy configuration: %w", err)
	}

	var raw any
	if err := json.Unmarshal(contents, &raw); err != nil {
		return document{}, true, fmt.Errorf("parse ycy configuration: %w", err)
	}
	root, ok := raw.(map[string]any)
	if !ok {
		normalized, err := store.emptyDocument()
		return normalized, true, err
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(contents, &rawFields); err != nil {
		return document{}, true, fmt.Errorf("parse ycy configuration fields: %w", err)
	}
	normalized, err := store.normalizeDocument(root, rawFields)
	if err != nil {
		return document{}, true, err
	}
	return normalized, true, nil
}

func (store *Store) normalizeDocument(root map[string]any, rawFields map[string]json.RawMessage) (document, error) {
	empty, err := store.emptyDocument()
	if err != nil {
		return document{}, err
	}

	normalized := empty
	if salt, ok := stringValue(root["salt"]); ok && salt != "" {
		normalized.Salt = salt
	}

	legacyInstances, hasLegacyInstances := objectValue(root["instances"])
	fork, hasFork := objectValue(root["fork"])
	forkInstances, hasForkInstances := objectValue(fork["instances"])
	forkFields := rawObjectFields(rawFields["fork"])
	switch {
	case hasFork && hasForkInstances:
		normalized.Fork.Instances, normalized.Fork.order, err = normalizeForkInstances(forkInstances, orderedObjectKeys(forkFields["instances"]))
	case hasLegacyInstances:
		normalized.Fork.Instances, normalized.Fork.order, err = normalizeForkInstances(legacyInstances, orderedObjectKeys(rawFields["instances"]))
	}
	if err != nil {
		return document{}, err
	}

	if cm, ok := objectValue(root["cm"]); ok {
		cmFields := rawObjectFields(rawFields["cm"])
		normalized.CM = normalizeCM(cm, orderedObjectKeys(cmFields["profiles"]))
	} else if legacyCM, ok := objectValue(root["ai"]); ok {
		legacyCMFields := rawObjectFields(rawFields["ai"])
		normalized.CM = normalizeCM(legacyCM, orderedObjectKeys(legacyCMFields["profiles"]))
	}
	if tunnel, ok := objectValue(root["tunnel"]); ok {
		if connections, ok := objectValue(tunnel["connections"]); ok {
			tunnelFields := rawObjectFields(rawFields["tunnel"])
			normalized.Tunnel = normalizeTunnel(connections, orderedObjectKeys(tunnelFields["connections"]))
		}
	}
	return normalized, nil
}

func normalizeForkInstances(raw map[string]any, order []string) (map[string]forkDocumentInstance, []string, error) {
	instances := make(map[string]forkDocumentInstance, len(raw))
	for name, value := range raw {
		instance, ok := objectValue(value)
		if !ok {
			continue
		}
		normalized := forkDocumentInstance{
			Host:   optionalString(instance["host"]),
			Scheme: optionalString(instance["scheme"]),
			Type:   optionalString(instance["type"]),
			Token:  optionalString(instance["token"]),
		}
		if containsScheme(normalized.Host) {
			parsed, err := url.ParseRequestURI(normalized.Host)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				if err == nil {
					err = errors.New("missing URL scheme or host")
				}
				return nil, nil, fmt.Errorf("normalize Fork instance %q URL host: %w", name, err)
			}
			normalized.Scheme = parsed.Scheme
			normalized.Host = parsed.Host
		}
		instances[name] = normalized
	}
	return instances, normalizedOrder(order, instances), nil
}

func containsScheme(value string) bool {
	for index := 0; index+2 < len(value); index++ {
		if value[index:index+3] == "://" {
			return true
		}
	}
	return false
}

func normalizeCM(raw map[string]any, order []string) *cmDocument {
	profiles, _ := objectValue(raw["profiles"])
	normalized := &cmDocument{
		DefaultProfile: optionalString(raw["defaultProfile"]),
		Profiles:       make(map[string]cmDocumentProfile, len(profiles)),
	}
	for name, value := range profiles {
		profile, ok := objectValue(value)
		if !ok {
			continue
		}
		normalized.Profiles[name] = cmDocumentProfile{
			BaseURL:         optionalString(profile["baseURL"]),
			Model:           optionalString(profile["model"]),
			APIKey:          optionalString(profile["apiKey"]),
			Temperature:     optionalFiniteFloat(profile["temperature"]),
			TimeoutMS:       optionalInteger(profile["timeoutMs"]),
			MaxOutputTokens: optionalInteger(profile["maxOutputTokens"]),
		}
	}
	normalized.order = normalizedOrder(order, normalized.Profiles)
	return normalized
}

func normalizeTunnel(raw map[string]any, order []string) *tunnelDocument {
	normalized := &tunnelDocument{Connections: make(map[string]tunnelDocumentConnection, len(raw))}
	for id, value := range raw {
		connection, ok := objectValue(value)
		if !ok {
			continue
		}
		normalized.Connections[id] = tunnelDocumentConnection{
			Server:              optionalString(connection["server"]),
			Token:               optionalString(connection["token"]),
			LastAuthenticatedAt: optionalString(connection["lastAuthenticatedAt"]),
		}
	}
	normalized.order = normalizedOrder(order, normalized.Connections)
	return normalized
}

func objectValue(value any) (map[string]any, bool) {
	record, ok := value.(map[string]any)
	return record, ok
}

func rawObjectFields(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

func stringValue(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func optionalString(value any) string {
	text, _ := stringValue(value)
	return text
}

func optionalFiniteFloat(value any) *float64 {
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	return &number
}

func optionalInteger(value any) *int {
	number, ok := value.(float64)
	if !ok || math.Trunc(number) != number || number > float64(maxInt()) || number < float64(minInt()) {
		return nil
	}
	integer := int(number)
	return &integer
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func minInt() int {
	return -maxInt() - 1
}
