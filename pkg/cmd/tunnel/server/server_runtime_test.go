package server

import (
	"context"
	"database/sql"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestServerRuntimeComposesOwnedResourcesAndReleasesThem(t *testing.T) {
	options := ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address:     "127.0.0.1",
			ControlPort: 7500,
			FRPPort:     7000,
			HTTPPort:    8080,
			PortRange:   ServerHTTPPortRange{Start: 20000, End: 20100},
			DataDir:     t.TempDir(),
			AdminUser:   "admin",
		},
		AdminPassword:       "environment-password",
		SessionIdleLifetime: time.Hour,
	}
	configureServerRuntimeTestFRP(t, &options)

	runtime, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}
	if runtime.state == nil || runtime.accounts == nil || runtime.sessions == nil || runtime.controlPlane == nil || runtime.supervisor == nil || runtime.frps == nil || runtime.gateway == nil || runtime.handler == nil {
		t.Fatalf("NewServerRuntime() did not compose every owned resource: %#v", runtime)
	}
	if state := runtime.frps.FRPSState(); state.State != tunnelruntime.FRPProcessStopped || state.Error != nil {
		t.Fatalf("initial managed FRPS state = %#v, want stopped without error", state)
	}

	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("runtime health response = (%d, %q)", response.Code, response.Body.String())
	}

	firstToken := serverRuntimeInternalFRPToken(t, runtime)
	if len(firstToken) != 43 {
		t.Fatalf("generated Internal FRP Token length = %d, want 43", len(firstToken))
	}
	if competing, competingErr := NewServerRuntime(context.Background(), options); competing != nil || !errors.Is(competingErr, tunnelruntime.ErrInstanceActive) {
		if competing != nil {
			_ = competing.Close()
		}
		t.Fatalf("second NewServerRuntime() = (%v, %v), want ErrInstanceActive", competing, competingErr)
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Runtime.Close() error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Runtime.Close() error = %v", err)
	}

	reopened, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() after Close error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if got := serverRuntimeInternalFRPToken(t, reopened); got != firstToken {
		t.Fatalf("reopened Internal FRP Token = %q, want %q", got, firstToken)
	}
}

func TestServerRuntimeClosesStateWhenLaterCompositionFails(t *testing.T) {
	options := ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address:     "127.0.0.1",
			ControlPort: 7500,
			FRPPort:     7000,
			HTTPPort:    8080,
			PortRange:   ServerHTTPPortRange{Start: 20000, End: 20100},
			DataDir:     t.TempDir(),
			AdminUser:   "admin",
		},
		AdminPassword: "bad",
	}
	configureServerRuntimeTestFRP(t, &options)

	if runtime, err := NewServerRuntime(context.Background(), options); runtime != nil || err == nil {
		if runtime != nil {
			_ = runtime.Close()
		}
		t.Fatalf("NewServerRuntime() = (%v, %v), want composition error", runtime, err)
	}

	options.AdminPassword = "environment-password"
	runtime, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() after failure error = %v", err)
	}
	defer func() { _ = runtime.Close() }()
}

func TestServerRuntimeUsesConfiguredFRPTokenWithoutPersistingIt(t *testing.T) {
	options := ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address:     "127.0.0.1",
			ControlPort: 7500,
			FRPPort:     7000,
			HTTPPort:    8080,
			PortRange:   ServerHTTPPortRange{Start: 20000, End: 20100},
			DataDir:     t.TempDir(),
			AdminUser:   "admin",
		},
		AdminPassword: "environment-password",
		FRPToken:      "configured-frp-token",
	}
	configureServerRuntimeTestFRP(t, &options)

	runtime, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}
	defer func() { _ = runtime.Close() }()
	if settings := runtime.frps.AgentWelcomeSettings("frp.example.test"); settings.InternalFRPToken != options.FRPToken {
		t.Fatalf("managed Internal FRP Token = %q, want %q", settings.InternalFRPToken, options.FRPToken)
	}
	var persisted string
	if err := runtime.state.database.QueryRow(`SELECT value FROM meta WHERE key = 'internal_frp_token'`).Scan(&persisted); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("configured token persistence error = %v, want sql.ErrNoRows", err)
	}
}

func TestServerRuntimeStartsControlHTTPAndReleasesResourcesOnClose(t *testing.T) {
	options := ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address:     "127.0.0.1",
			ControlPort: 0,
			FRPPort:     7000,
			HTTPPort:    8080,
			PortRange:   ServerHTTPPortRange{Start: 20000, End: 20100},
			DataDir:     t.TempDir(),
			AdminUser:   "admin",
		},
		AdminPassword: "environment-password",
	}
	configureServerRuntimeTestFRP(t, &options)
	runtime, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}
	server, err := runtime.Start()
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("Runtime.Start() error = %v", err)
	}
	if server.Port() == 0 || server.URL() == "" {
		t.Fatalf("running control listener = (%q, %d)", server.URL(), server.Port())
	}
	response, err := http.Get(server.URL() + "/healthz")
	if err != nil {
		_ = server.Close()
		t.Fatalf("GET /healthz: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_ = server.Close()
		t.Fatalf("GET /healthz status = %d", response.StatusCode)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("RunningServer.Close() error = %v", err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("RunningServer.Wait() error = %v", err)
	}
	if _, err := http.Get(server.URL() + "/healthz"); err == nil {
		t.Fatal("control listener still accepted a connection after Close")
	}

	reopened, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() after listener Close error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
}

func TestServerRuntimeStartReleasesResourcesWhenControlPortIsUnavailable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve control port: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	options := ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address:     "127.0.0.1",
			ControlPort: port,
			FRPPort:     7000,
			HTTPPort:    8080,
			PortRange:   ServerHTTPPortRange{Start: 20000, End: 20100},
			DataDir:     t.TempDir(),
			AdminUser:   "admin",
		},
		AdminPassword: "environment-password",
	}
	configureServerRuntimeTestFRP(t, &options)
	runtime, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}
	if server, startErr := runtime.Start(); server != nil || startErr == nil {
		if server != nil {
			_ = server.Close()
		}
		t.Fatalf("Runtime.Start() = (%v, %v), want listener error", server, startErr)
	}

	options.Settings.ControlPort = 0
	reopened, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() after listener failure error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
}

func TestServerRuntimeBackgroundFRPSFailureKeepsControlPlaneAvailable(t *testing.T) {
	options := ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address:     "127.0.0.1",
			ControlPort: 0,
			FRPPort:     7000,
			HTTPPort:    8080,
			PortRange:   ServerHTTPPortRange{Start: 20000, End: 20100},
			DataDir:     t.TempDir(),
			AdminUser:   "admin",
		},
		AdminPassword: "environment-password",
	}
	configureServerRuntimeTestFRP(t, &options)
	runtime, err := NewServerRuntime(context.Background(), options)
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}
	server, err := runtime.Start()
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("Runtime.Start() error = %v", err)
	}
	defer func() { _ = server.Close() }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		state := runtime.frps.FRPSState()
		if state.State == tunnelruntime.FRPProcessConfigurationFailed {
			if state.Error == nil || state.Error.Code != "CONFIGURATION_FAILED" {
				t.Fatalf("background FRPS failure = %#v, want CONFIGURATION_FAILED", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background FRPS state = %#v, want configuration failure", state)
		}
		time.Sleep(10 * time.Millisecond)
	}

	response, err := http.Get(server.URL() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz after FRPS failure: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz after FRPS failure status = %d", response.StatusCode)
	}
}

func serverRuntimeInternalFRPToken(t *testing.T, runtime *ServerRuntime) string {
	t.Helper()
	var token string
	if err := runtime.state.database.QueryRow(`SELECT value FROM meta WHERE key = 'internal_frp_token'`).Scan(&token); err != nil {
		t.Fatalf("read persisted Internal FRP Token: %v", err)
	}
	return token
}

func configureServerRuntimeTestFRP(t *testing.T, options *ServerRuntimeOptions) {
	t.Helper()
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	directory := filepath.Join(t.TempDir(), "frp", tunnelruntime.FRPVersion)
	options.frpArtifact = &artifact
	options.frpRuntimeDirectory = directory
	options.ensureFRPRuntime = func(context.Context, string, tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error) {
		return tunnelruntime.FRPRuntimePathsFor(directory, artifact.Target), nil
	}
}
