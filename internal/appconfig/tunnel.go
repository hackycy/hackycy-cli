package appconfig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	tunnelInstanceIDPrefix = "v1_"
	tunnelInstanceIDDomain = "ycy:tunnel-client-instance:v1\x00"
	maxTunnelConnections   = 32
)

var tunnelInstanceIDPattern = regexp.MustCompile(`^v1_[A-Za-z0-9_-]{43}$`)

// TunnelConnection is a decrypted remembered connection for the Tunnel owner.
type TunnelConnection struct {
	ID                  string
	Server              string
	Token               string
	LastAuthenticatedAt string
}

// TunnelConnectionSummary is the secret-safe projection used by configuration
// management commands.
type TunnelConnectionSummary struct {
	ID                  string
	Server              string
	LastAuthenticatedAt string
}

type validTunnelConnection struct {
	TunnelConnection
	stored tunnelDocumentConnection
}

// ReadTunnelConnections returns valid remembered connections in newest-first order.
func (store *Store) ReadTunnelConnections() ([]TunnelConnection, error) {
	document, err := store.ensureDocument()
	if err != nil {
		return nil, err
	}
	key, err := store.keyForSalt(document.Salt)
	if err != nil {
		return nil, err
	}
	valid := validTunnelConnections(document, key)
	connections := make([]TunnelConnection, 0, len(valid))
	for _, connection := range valid {
		connections = append(connections, connection.TunnelConnection)
	}
	return connections, nil
}

// ListTunnelConnections returns remembered connections without exposing their
// Client Tokens.
func (store *Store) ListTunnelConnections() ([]TunnelConnectionSummary, error) {
	document, _, err := store.readDocument()
	if err != nil {
		return nil, err
	}
	if document.Tunnel == nil || len(document.Tunnel.Connections) == 0 {
		return nil, nil
	}
	key, err := store.keyForSalt(document.Salt)
	if err != nil {
		return nil, err
	}
	connections := validTunnelConnections(document, key)
	summaries := make([]TunnelConnectionSummary, len(connections))
	for index, connection := range connections {
		summaries[index] = TunnelConnectionSummary{
			ID:                  connection.ID,
			Server:              connection.Server,
			LastAuthenticatedAt: connection.LastAuthenticatedAt,
		}
	}
	return summaries, nil
}

// TunnelInstanceID derives the opaque stable directory identifier for one connection.
func (store *Store) TunnelInstanceID(server *url.URL, token string) (string, error) {
	origin, err := tunnelOriginFromURL(server)
	if err != nil {
		return "", err
	}
	document, err := store.ensureDocument()
	if err != nil {
		return "", err
	}
	key, err := store.keyForSalt(document.Salt)
	if err != nil {
		return "", err
	}
	return tunnelConnectionID(origin, token, key), nil
}

// RememberTunnelConnection updates one remembered connection and retains only the newest 32 valid entries.
func (store *Store) RememberTunnelConnection(server *url.URL, token string, authenticatedAt time.Time) error {
	origin, err := tunnelOriginFromURL(server)
	if err != nil {
		return err
	}
	return store.updateDocument(func(document *document) error {
		key, err := store.keyForSalt(document.Salt)
		if err != nil {
			return err
		}
		id := tunnelConnectionID(origin, token, key)
		entries := validTunnelConnections(*document, key)
		filtered := make([]validTunnelConnection, 0, len(entries)+1)
		for _, entry := range entries {
			if entry.ID != id {
				filtered = append(filtered, entry)
			}
		}
		encryptedToken, err := encryptValue(token, key, store.random)
		if err != nil {
			return err
		}
		stored := tunnelDocumentConnection{
			Server:              origin,
			Token:               encryptedToken,
			LastAuthenticatedAt: formatTunnelTimestamp(authenticatedAt),
		}
		filtered = append(filtered, validTunnelConnection{
			TunnelConnection: TunnelConnection{ID: id, Server: origin, Token: token, LastAuthenticatedAt: stored.LastAuthenticatedAt},
			stored:           stored,
		})
		sortValidTunnelConnections(filtered)
		if len(filtered) > maxTunnelConnections {
			filtered = filtered[:maxTunnelConnections]
		}
		document.Tunnel = &tunnelDocument{Connections: make(map[string]tunnelDocumentConnection, len(filtered)), order: make([]string, 0, len(filtered))}
		for _, entry := range filtered {
			document.Tunnel.Connections[entry.ID] = entry.stored
			document.Tunnel.order = append(document.Tunnel.order, entry.ID)
		}
		return nil
	})
}

// RemoveTunnelConnection removes one remembered connection and reports whether
// it existed.
func (store *Store) RemoveTunnelConnection(id string) (bool, error) {
	removed := false
	err := store.updateDocument(func(document *document) error {
		if document.Tunnel == nil {
			return nil
		}
		if _, exists := document.Tunnel.Connections[id]; !exists {
			return nil
		}
		delete(document.Tunnel.Connections, id)
		document.Tunnel.order = removeOrderedName(document.Tunnel.order, id)
		removed = true
		return nil
	})
	return removed, err
}

func validTunnelConnections(document document, key []byte) []validTunnelConnection {
	if document.Tunnel == nil {
		return nil
	}
	valid := make([]validTunnelConnection, 0, len(document.Tunnel.Connections))
	for _, id := range normalizedOrder(document.Tunnel.order, document.Tunnel.Connections) {
		stored := document.Tunnel.Connections[id]
		if !tunnelInstanceIDPattern.MatchString(id) {
			continue
		}
		origin, ok := storedTunnelOrigin(stored.Server)
		if !ok {
			continue
		}
		if _, err := time.Parse(time.RFC3339, stored.LastAuthenticatedAt); err != nil {
			continue
		}
		token, err := decryptValue(stored.Token, key)
		if err != nil {
			continue
		}
		token = strings.TrimSpace(token)
		if token == "" || tunnelConnectionID(origin, token, key) != id {
			continue
		}
		valid = append(valid, validTunnelConnection{
			TunnelConnection: TunnelConnection{ID: id, Server: origin, Token: token, LastAuthenticatedAt: stored.LastAuthenticatedAt},
			stored:           stored,
		})
	}
	sortValidTunnelConnections(valid)
	return valid
}

func sortValidTunnelConnections(connections []validTunnelConnection) {
	sort.Slice(connections, func(left, right int) bool {
		if connections[left].LastAuthenticatedAt == connections[right].LastAuthenticatedAt {
			return connections[left].ID < connections[right].ID
		}
		return connections[left].LastAuthenticatedAt > connections[right].LastAuthenticatedAt
	})
}

func tunnelConnectionID(origin, token string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(tunnelInstanceIDDomain))
	_, _ = mac.Write([]byte(origin))
	_, _ = mac.Write([]byte("\x00"))
	_, _ = mac.Write([]byte(token))
	return tunnelInstanceIDPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func tunnelOriginFromURL(server *url.URL) (string, error) {
	if server == nil {
		return "", errors.New("tunnel server is required")
	}
	if (server.Scheme != "http" && server.Scheme != "https") || server.Host == "" || server.User != nil || server.RawQuery != "" || server.Fragment != "" || (server.Path != "" && server.Path != "/") {
		return "", fmt.Errorf("invalid remembered tunnel server %q", server.String())
	}
	return server.Scheme + "://" + server.Host, nil
}

func storedTunnelOrigin(value string) (string, bool) {
	server, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	origin, err := tunnelOriginFromURL(server)
	if err != nil || origin != value {
		return "", false
	}
	return origin, true
}

func formatTunnelTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}
