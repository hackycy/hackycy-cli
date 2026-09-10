package appconfig

import (
	"bytes"
	"encoding/json"
	"sort"
)

func (document document) MarshalJSON() ([]byte, error) {
	type serializedDocument struct {
		Salt   string          `json:"salt"`
		Fork   forkDocument    `json:"fork"`
		CM     *cmDocument     `json:"cm,omitempty"`
		Tunnel *tunnelDocument `json:"tunnel,omitempty"`
	}
	return json.Marshal(serializedDocument{
		Salt:   document.Salt,
		Fork:   document.Fork,
		CM:     document.CM,
		Tunnel: document.Tunnel,
	})
}

func (document forkDocument) MarshalJSON() ([]byte, error) {
	type serializedFork struct {
		Instances orderedJSONMap[forkDocumentInstance] `json:"instances"`
	}
	return json.Marshal(serializedFork{Instances: orderedJSONMap[forkDocumentInstance]{order: document.order, values: document.Instances}})
}

func (instance forkDocumentInstance) MarshalJSON() ([]byte, error) {
	type serializedInstance struct {
		Host   string `json:"host"`
		Scheme string `json:"scheme,omitempty"`
		Type   string `json:"type"`
		Token  string `json:"token"`
	}
	return json.Marshal(serializedInstance{Host: instance.Host, Scheme: instance.Scheme, Type: instance.Type, Token: instance.Token})
}

func (document cmDocument) MarshalJSON() ([]byte, error) {
	type serializedCM struct {
		DefaultProfile string                            `json:"defaultProfile,omitempty"`
		Profiles       orderedJSONMap[cmDocumentProfile] `json:"profiles"`
	}
	return json.Marshal(serializedCM{DefaultProfile: document.DefaultProfile, Profiles: orderedJSONMap[cmDocumentProfile]{order: document.order, values: document.Profiles}})
}

func (profile cmDocumentProfile) MarshalJSON() ([]byte, error) {
	type serializedProfile struct {
		BaseURL         string   `json:"baseURL"`
		Model           string   `json:"model"`
		APIKey          string   `json:"apiKey"`
		Temperature     *float64 `json:"temperature,omitempty"`
		TimeoutMS       *int     `json:"timeoutMs,omitempty"`
		MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	}
	return json.Marshal(serializedProfile{
		BaseURL:         profile.BaseURL,
		Model:           profile.Model,
		APIKey:          profile.APIKey,
		Temperature:     profile.Temperature,
		TimeoutMS:       profile.TimeoutMS,
		MaxOutputTokens: profile.MaxOutputTokens,
	})
}

func (document tunnelDocument) MarshalJSON() ([]byte, error) {
	type serializedTunnel struct {
		Connections orderedJSONMap[tunnelDocumentConnection] `json:"connections"`
	}
	return json.Marshal(serializedTunnel{Connections: orderedJSONMap[tunnelDocumentConnection]{order: document.order, values: document.Connections}})
}

func (connection tunnelDocumentConnection) MarshalJSON() ([]byte, error) {
	type serializedConnection struct {
		Server              string `json:"server"`
		Token               string `json:"token"`
		LastAuthenticatedAt string `json:"lastAuthenticatedAt"`
	}
	return json.Marshal(serializedConnection{
		Server:              connection.Server,
		Token:               connection.Token,
		LastAuthenticatedAt: connection.LastAuthenticatedAt,
	})
}

type orderedJSONMap[V any] struct {
	order  []string
	values map[string]V
}

func (ordered orderedJSONMap[V]) MarshalJSON() ([]byte, error) {
	var output bytes.Buffer
	output.WriteByte('{')
	first := true
	for _, name := range normalizedOrder(ordered.order, ordered.values) {
		value := ordered.values[name]
		encodedName, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		encodedValue, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if !first {
			output.WriteByte(',')
		}
		output.Write(encodedName)
		output.WriteByte(':')
		output.Write(encodedValue)
		first = false
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

func orderedObjectKeys(raw json.RawMessage) []string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil
	}
	delimiter, ok := opening.(json.Delim)
	if !ok || delimiter != '{' {
		return nil
	}
	var names []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil
		}
		name, ok := token.(string)
		if !ok {
			return nil
		}
		var ignored json.RawMessage
		if err := decoder.Decode(&ignored); err != nil {
			return nil
		}
		names = append(names, name)
	}
	if _, err := decoder.Token(); err != nil {
		return nil
	}
	return names
}

func normalizedOrder[V any](order []string, values map[string]V) []string {
	names := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, name := range order {
		if _, exists := values[name]; !exists {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		names = append(names, name)
		seen[name] = struct{}{}
	}
	var remaining []string
	for name := range values {
		if _, exists := seen[name]; !exists {
			remaining = append(remaining, name)
		}
	}
	sort.Strings(remaining)
	return append(names, remaining...)
}
