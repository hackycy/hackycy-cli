package connect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

type clientConnectionReaderStub struct {
	connections []appconfig.TunnelConnection
	err         error
}

func (reader clientConnectionReaderStub) ReadTunnelConnections() ([]appconfig.TunnelConnection, error) {
	return reader.connections, reader.err
}

func TestResolveClientConfigUsesFieldWisePrecedence(t *testing.T) {
	explicitServer := "http://CONTROL.example.test:80/"
	explicitToken := "  explicit-token  "
	resolved, err := ResolveClientConfig(context.Background(), ClientOptionInput{
		Server: &explicitServer,
		Token:  &explicitToken,
	}, ClientResolutionOptions{
		Reader: clientConnectionReaderStub{connections: []appconfig.TunnelConnection{{
			ID:     "remembered",
			Server: "https://remembered.example.test",
			Token:  "remembered-token",
		}}},
		Environment: clientConfigTestEnvironment(map[string]string{
			"YCY_TUNNEL_SERVER": "https://environment.example.test",
			"YCY_TUNNEL_TOKEN":  "environment-token",
		}),
		DefaultServer: "https://default.example.test",
	})
	if err != nil {
		t.Fatalf("ResolveClientConfig() error = %v", err)
	}
	if resolved == nil {
		t.Fatal("ResolveClientConfig() = nil")
	}
	if got, want := resolved.Config.Server.String(), "http://control.example.test"; got != want {
		t.Errorf("server = %q, want %q", got, want)
	}
	if got, want := resolved.Config.Token, "explicit-token"; got != want {
		t.Errorf("token = %q, want %q", got, want)
	}
	if !resolved.RememberOnAuthentication {
		t.Error("RememberOnAuthentication = false, want true for an explicit CLI token")
	}
}

func TestResolveClientConfigReadsTokenFileWithoutRememberingNewSecret(t *testing.T) {
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token")
	if err := os.WriteFile(tokenPath, []byte("  file-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	resolved, err := ResolveClientConfig(context.Background(), ClientOptionInput{}, ClientResolutionOptions{
		Reader: clientConnectionReaderStub{},
		Environment: clientConfigTestEnvironment(map[string]string{
			"YCY_TUNNEL_SERVER":     "control.example.test",
			"YCY_TUNNEL_TOKEN_FILE": tokenPath,
		}),
	})
	if err != nil {
		t.Fatalf("ResolveClientConfig() error = %v", err)
	}
	if resolved == nil {
		t.Fatal("ResolveClientConfig() = nil")
	}
	if got, want := resolved.Config.Server.String(), "https://control.example.test"; got != want {
		t.Errorf("server = %q, want %q", got, want)
	}
	if got, want := resolved.Config.Token, "file-token"; got != want {
		t.Errorf("token = %q, want %q", got, want)
	}
	if resolved.RememberOnAuthentication {
		t.Error("RememberOnAuthentication = true, want false for a new token-file secret")
	}
}

func TestResolveClientConfigSelectsRememberedFieldsIndependently(t *testing.T) {
	connections := []appconfig.TunnelConnection{
		{ID: "one", Server: "https://one.example.test", Token: "one-token"},
		{ID: "two", Server: "https://two.example.test", Token: "two-token"},
	}

	t.Run("server selects matching token", func(t *testing.T) {
		server := "https://two.example.test"
		resolved, err := ResolveClientConfig(context.Background(), ClientOptionInput{Server: &server}, ClientResolutionOptions{
			Reader: clientConnectionReaderStub{connections: connections},
		})
		if err != nil {
			t.Fatalf("ResolveClientConfig() error = %v", err)
		}
		if resolved == nil || resolved.Config.Token != "two-token" || !resolved.RememberOnAuthentication {
			t.Fatalf("ResolveClientConfig() = %#v, want remembered two-token", resolved)
		}
	})

	t.Run("new CLI token selects a remembered origin as a rotation candidate", func(t *testing.T) {
		token := "replacement-token"
		var candidates []appconfig.TunnelConnection
		resolved, err := ResolveClientConfig(context.Background(), ClientOptionInput{Token: &token}, ClientResolutionOptions{
			Reader: clientConnectionReaderStub{connections: connections},
			SelectConnection: func(_ context.Context, values []appconfig.TunnelConnection) (string, bool, error) {
				candidates = append(candidates, values...)
				return values[1].ID, false, nil
			},
		})
		if err != nil {
			t.Fatalf("ResolveClientConfig() error = %v", err)
		}
		if len(candidates) != 2 || candidates[0].Token != "replacement-token" || candidates[1].Token != "replacement-token" {
			t.Fatalf("rotation candidates = %#v", candidates)
		}
		if resolved == nil || resolved.Config.Server.String() != "https://two.example.test" || resolved.Config.Token != token || !resolved.RememberOnAuthentication {
			t.Fatalf("ResolveClientConfig() = %#v, want selected rotated connection", resolved)
		}
	})
}

func TestResolveClientConfigHandlesSelectionCancellationAndNonInteractiveAmbiguity(t *testing.T) {
	connections := []appconfig.TunnelConnection{
		{ID: "one", Server: "https://one.example.test", Token: "one-token"},
		{ID: "two", Server: "https://two.example.test", Token: "two-token"},
	}

	t.Run("cancellation returns no resolved client", func(t *testing.T) {
		resolved, err := ResolveClientConfig(context.Background(), ClientOptionInput{}, ClientResolutionOptions{
			Reader: clientConnectionReaderStub{connections: connections},
			SelectConnection: func(context.Context, []appconfig.TunnelConnection) (string, bool, error) {
				return "", true, nil
			},
		})
		if err != nil {
			t.Fatalf("ResolveClientConfig() error = %v", err)
		}
		if resolved != nil {
			t.Fatalf("ResolveClientConfig() = %#v, want nil after cancellation", resolved)
		}
	})

	t.Run("non-interactive multiple candidates fail instead of guessing", func(t *testing.T) {
		_, err := ResolveClientConfig(context.Background(), ClientOptionInput{}, ClientResolutionOptions{
			Reader: clientConnectionReaderStub{connections: connections},
		})
		if err == nil || !strings.Contains(err.Error(), "Multiple remembered tunnel connections match") {
			t.Fatalf("ResolveClientConfig() error = %v, want non-interactive ambiguity error", err)
		}
	})
}

func TestResolveClientConfigRejectsInvalidOrIncompleteInputs(t *testing.T) {
	t.Run("empty explicit values", func(t *testing.T) {
		empty := "  "
		_, err := ResolveClientConfig(context.Background(), ClientOptionInput{Server: &empty}, ClientResolutionOptions{Reader: clientConnectionReaderStub{}})
		if err == nil || !strings.Contains(err.Error(), "Control plane must not be empty") {
			t.Fatalf("empty server error = %v", err)
		}
		_, err = ResolveClientConfig(context.Background(), ClientOptionInput{Token: &empty}, ClientResolutionOptions{Reader: clientConnectionReaderStub{}})
		if err == nil || !strings.Contains(err.Error(), "Client Token must not be empty") {
			t.Fatalf("empty token error = %v", err)
		}
	})

	t.Run("invalid server and missing fields", func(t *testing.T) {
		server := "https://control.example.test/path"
		_, err := ResolveClientConfig(context.Background(), ClientOptionInput{Server: &server}, ClientResolutionOptions{Reader: clientConnectionReaderStub{}})
		if err == nil || !strings.Contains(err.Error(), "must not include a path") {
			t.Fatalf("path server error = %v", err)
		}

		_, err = ResolveClientConfig(context.Background(), ClientOptionInput{}, ClientResolutionOptions{Reader: clientConnectionReaderStub{}})
		if err == nil || !strings.Contains(err.Error(), "Control plane is required") {
			t.Fatalf("missing server error = %v", err)
		}

		_, err = ResolveClientConfig(context.Background(), ClientOptionInput{}, ClientResolutionOptions{
			Reader:        clientConnectionReaderStub{},
			DefaultServer: "default.example.test",
		})
		if err == nil || !strings.Contains(err.Error(), "Client Token is required") {
			t.Fatalf("missing token error = %v", err)
		}
	})

	t.Run("token-file read failure is surfaced", func(t *testing.T) {
		_, err := ResolveClientConfig(context.Background(), ClientOptionInput{}, ClientResolutionOptions{
			Reader: clientConnectionReaderStub{},
			Environment: clientConfigTestEnvironment(map[string]string{
				"YCY_TUNNEL_TOKEN_FILE": "missing-token-file",
			}),
			ReadFile: func(string) ([]byte, error) { return nil, errors.New("not found") },
		})
		if err == nil || !strings.Contains(err.Error(), "Could not read Client Token file: not found") {
			t.Fatalf("token-file error = %v", err)
		}
	})
}

func clientConfigTestEnvironment(values map[string]string) ClientEnvironment {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
