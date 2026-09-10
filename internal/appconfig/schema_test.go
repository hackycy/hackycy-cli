package appconfig

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestReadDocumentNormalizesCurrentAndLegacyShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want document
	}{
		{
			name: "current shape",
			body: `{
  "salt": "c2FsdA==",
  "fork": {"instances": {"github": {"host": "github.com", "scheme": "https", "type": "github", "token": "ciphertext"}}},
  "cm": {"defaultProfile": "work", "profiles": {"work": {"baseURL": "https://provider.example/v1", "model": "gpt", "apiKey": "api-ciphertext", "temperature": 0.4, "timeoutMs": 9000, "maxOutputTokens": 77}}},
  "tunnel": {"connections": {"v1_example": {"server": "https://tunnel.example", "token": "tunnel-ciphertext", "lastAuthenticatedAt": "2026-01-01T00:00:00.000Z"}}
  }
}`,
			want: document{
				Salt: "c2FsdA==",
				Fork: forkDocument{Instances: map[string]forkDocumentInstance{
					"github": {Host: "github.com", Scheme: "https", Type: "github", Token: "ciphertext"},
				}},
				CM: &cmDocument{DefaultProfile: "work", Profiles: map[string]cmDocumentProfile{
					"work": {BaseURL: "https://provider.example/v1", Model: "gpt", APIKey: "api-ciphertext", Temperature: floatPointer(0.4), TimeoutMS: intPointer(9000), MaxOutputTokens: intPointer(77)},
				}},
				Tunnel: &tunnelDocument{Connections: map[string]tunnelDocumentConnection{
					"v1_example": {Server: "https://tunnel.example", Token: "tunnel-ciphertext", LastAuthenticatedAt: "2026-01-01T00:00:00.000Z"},
				}},
			},
		},
		{
			name: "legacy instances ai and URL host",
			body: `{
  "salt": "bGVnYWN5LXNhbHQ=",
  "instances": {"gitlab": {"host": "http://gitlab.example:8080/path", "type": "gitlab", "token": "ciphertext"}},
  "ai": {"profiles": {"legacy": {"baseURL": "https://provider.example/", "model": "model", "apiKey": "api-ciphertext"}}}
}`,
			want: document{
				Salt: "bGVnYWN5LXNhbHQ=",
				Fork: forkDocument{Instances: map[string]forkDocumentInstance{
					"gitlab": {Host: "gitlab.example:8080", Scheme: "http", Type: "gitlab", Token: "ciphertext"},
				}},
				CM: &cmDocument{Profiles: map[string]cmDocumentProfile{
					"legacy": {BaseURL: "https://provider.example/", Model: "model", APIKey: "api-ciphertext"},
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := testStore(t)
			writeConfigFixture(t, store, test.body)

			got, exists, err := store.readDocument()
			if err != nil {
				t.Fatalf("readDocument() returned an error: %v", err)
			}
			if !exists {
				t.Fatal("readDocument() reported a missing config")
			}
			if !equalDocument(got, test.want) {
				t.Fatalf("readDocument() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReadDocumentMissingFileDoesNotPublish(t *testing.T) {
	store := testStore(t)

	got, exists, err := store.readDocument()
	if err != nil {
		t.Fatalf("readDocument() returned an error: %v", err)
	}
	if exists {
		t.Fatal("readDocument() reported an existing config")
	}
	if got.Salt == "" {
		t.Fatal("readDocument() did not generate a salt")
	}
	if _, err := base64.StdEncoding.DecodeString(got.Salt); err != nil {
		t.Fatalf("generated salt is not base64: %v", err)
	}
	if _, err := os.Stat(store.configPath()); !os.IsNotExist(err) {
		t.Fatalf("config path stat error = %v, want not exist", err)
	}
}

func TestReadDocumentRejectsMalformedJSONAndNormalizesUnexpectedRoots(t *testing.T) {
	t.Run("malformed JSON", func(t *testing.T) {
		store := testStore(t)
		writeConfigFixture(t, store, `{`)

		if _, _, err := store.readDocument(); err == nil {
			t.Fatal("readDocument() returned nil error")
		}
	})

	for _, body := range []string{`[]`, `null`, `{"unknown": {"field": true}}`} {
		t.Run(body, func(t *testing.T) {
			store := testStore(t)
			writeConfigFixture(t, store, body)

			got, exists, err := store.readDocument()
			if err != nil {
				t.Fatalf("readDocument() returned an error: %v", err)
			}
			if !exists {
				t.Fatal("readDocument() reported a missing config")
			}
			if got.Salt == "" || len(got.Fork.Instances) != 0 || got.CM != nil || got.Tunnel != nil {
				t.Fatalf("readDocument() = %#v, want an empty normalized document", got)
			}
		})
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	home := t.TempDir()
	store, err := New(Dependencies{
		Environment: func(string) string { return "" },
		UserHomeDir: func() (string, error) { return home, nil },
	})
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}
	return store
}

func writeConfigFixture(t *testing.T, store *Store, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(store.configPath()), 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(store.configPath(), []byte(body), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
}

func floatPointer(value float64) *float64 { return &value }

func intPointer(value int) *int { return &value }

func equalDocument(left, right document) bool {
	return left.Salt == right.Salt && equalFork(left.Fork, right.Fork) && equalCM(left.CM, right.CM) && equalTunnel(left.Tunnel, right.Tunnel)
}

func equalFork(left, right forkDocument) bool {
	if len(left.Instances) != len(right.Instances) {
		return false
	}
	for name, leftInstance := range left.Instances {
		if rightInstance, ok := right.Instances[name]; !ok || leftInstance != rightInstance {
			return false
		}
	}
	return true
}

func equalCM(left, right *cmDocument) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.DefaultProfile != right.DefaultProfile || len(left.Profiles) != len(right.Profiles) {
		return false
	}
	for name, leftProfile := range left.Profiles {
		rightProfile, ok := right.Profiles[name]
		if !ok || leftProfile.BaseURL != rightProfile.BaseURL || leftProfile.Model != rightProfile.Model || leftProfile.APIKey != rightProfile.APIKey || !equalFloatPointer(leftProfile.Temperature, rightProfile.Temperature) || !equalIntPointer(leftProfile.TimeoutMS, rightProfile.TimeoutMS) || !equalIntPointer(leftProfile.MaxOutputTokens, rightProfile.MaxOutputTokens) {
			return false
		}
	}
	return true
}

func equalTunnel(left, right *tunnelDocument) bool {
	if left == nil || right == nil {
		return left == right
	}
	if len(left.Connections) != len(right.Connections) {
		return false
	}
	for name, leftConnection := range left.Connections {
		if rightConnection, ok := right.Connections[name]; !ok || leftConnection != rightConnection {
			return false
		}
	}
	return true
}

func equalFloatPointer(left, right *float64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func equalIntPointer(left, right *int) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
