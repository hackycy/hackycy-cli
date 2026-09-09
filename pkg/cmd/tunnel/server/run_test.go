package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestResolveServerConfigPrefersExplicitOptionsOverEnvironment(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "relative-server-state")
	input := ServerOptionInput{
		Address:          serverOption(" \t127.0.0.1 \n"),
		ControlPort:      serverOption("0x1d4c"),
		FRPPort:          serverOption("7001"),
		HTTPPort:         serverOption("8081"),
		PortRange:        serverOption("20000-20100"),
		AdvertiseFRPAddr: serverOption(" tunnels.example.test:7443"),
		DataDir:          serverOption(dataDirectory),
		SessionIdleDays:  serverOption("8"),
	}
	config, err := ResolveServerConfig(input, serverEnvironment(map[string]string{
		"YCY_TUNNEL_ADDRESS":            "0.0.0.0",
		"YCY_TUNNEL_CONTROL_PORT":       "7500",
		"YCY_TUNNEL_FRP_PORT":           "7000",
		"YCY_TUNNEL_HTTP_PORT":          "8080",
		"YCY_TUNNEL_PORT_RANGE":         "21000-21100",
		"YCY_TUNNEL_ADVERTISE_FRP_ADDR": "environment.example.test:7555",
		"YCY_TUNNEL_DATA_DIR":           t.TempDir(),
		"YCY_TUNNEL_SESSION_IDLE_DAYS":  "7",
		"YCY_TUNNEL_ADMIN_USER":         "ops-admin",
		"YCY_TUNNEL_ADMIN_PASSWORD":     "environment-password",
		"YCY_TUNNEL_FRP_TOKEN":          " configured-token ",
	}))
	if err != nil {
		t.Fatalf("ResolveServerConfig() error = %v", err)
	}
	resolvedDataDirectory, err := filepath.Abs(dataDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if config.Settings.Address != "127.0.0.1" || config.Settings.ControlPort != 7500 || config.Settings.FRPPort != 7001 || config.Settings.HTTPPort != 8081 || config.Settings.PortRange != (ServerHTTPPortRange{Start: 20000, End: 20100}) || config.Settings.AdvertiseFRPAddr == nil || *config.Settings.AdvertiseFRPAddr != (ServerHTTPFRPAddress{Host: "tunnels.example.test", Port: 7443}) || config.Settings.DataDir != resolvedDataDirectory || config.Settings.AdminUser != "ops-admin" || config.AdminPassword != "environment-password" || config.FRPToken != "configured-token" || config.SessionIdleLifetime != 8*24*time.Hour {
		t.Fatalf("resolved server config = %#v", config)
	}
}

func TestResolveServerConfigUsesEnvironmentAndDefaults(t *testing.T) {
	defaultDirectory := filepath.Join(t.TempDir(), "server")
	config, err := resolveServerConfig(ServerOptionInput{}, serverEnvironment(map[string]string{
		"YCY_TUNNEL_ADDRESS":           " 127.0.0.2 ",
		"YCY_TUNNEL_CONTROL_PORT":      "7501",
		"YCY_TUNNEL_FRP_PORT":          "7001",
		"YCY_TUNNEL_HTTP_PORT":         "8081",
		"YCY_TUNNEL_PORT_RANGE":        "20001-20100",
		"YCY_TUNNEL_SESSION_IDLE_DAYS": "9",
		"YCY_TUNNEL_ADMIN_PASSWORD":    "environment-password",
	}), func() (string, error) {
		return defaultDirectory, nil
	})
	if err != nil {
		t.Fatalf("resolveServerConfig() error = %v", err)
	}
	if config.Settings.Address != "127.0.0.2" || config.Settings.ControlPort != 7501 || config.Settings.FRPPort != 7001 || config.Settings.HTTPPort != 8081 || config.Settings.PortRange != (ServerHTTPPortRange{Start: 20001, End: 20100}) || config.Settings.AdvertiseFRPAddr != nil || config.Settings.DataDir != defaultDirectory || config.Settings.AdminUser != "admin" || config.AdminPassword != "environment-password" || config.FRPToken != "" || config.SessionIdleLifetime != 9*24*time.Hour {
		t.Fatalf("environment/default config = %#v", config)
	}
}

func TestResolveServerConfigRejectsInvalidOrConflictingSettings(t *testing.T) {
	base := func() ServerOptionInput {
		return ServerOptionInput{
			Address:         serverOption("127.0.0.1"),
			ControlPort:     serverOption("7500"),
			FRPPort:         serverOption("7000"),
			HTTPPort:        serverOption("8080"),
			PortRange:       serverOption("20000-20100"),
			DataDir:         serverOption(t.TempDir()),
			SessionIdleDays: serverOption("7"),
		}
	}
	environment := serverEnvironment(map[string]string{"YCY_TUNNEL_ADMIN_PASSWORD": "environment-password"})
	tests := []struct {
		name   string
		mutate func(*ServerOptionInput)
	}{
		{name: "empty explicit address", mutate: func(input *ServerOptionInput) { input.Address = serverOption(" \t") }},
		{name: "zero control port", mutate: func(input *ServerOptionInput) { input.ControlPort = serverOption("0") }},
		{name: "fractional FRP port", mutate: func(input *ServerOptionInput) { input.FRPPort = serverOption("7000.5") }},
		{name: "unsafe HTTP port", mutate: func(input *ServerOptionInput) { input.HTTPPort = serverOption("9007199254740992") }},
		{name: "malformed port range", mutate: func(input *ServerOptionInput) { input.PortRange = serverOption("20000 - 20100") }},
		{name: "reversed port range", mutate: func(input *ServerOptionInput) { input.PortRange = serverOption("20100-20000") }},
		{name: "duplicate listeners", mutate: func(input *ServerOptionInput) { input.FRPPort = serverOption("7500") }},
		{name: "listener inside pool", mutate: func(input *ServerOptionInput) { input.PortRange = serverOption("7000-20100") }},
		{name: "invalid advertised address", mutate: func(input *ServerOptionInput) {
			input.AdvertiseFRPAddr = serverOption("https://tunnels.example.test:7000")
		}},
		{name: "unbracketed IPv6", mutate: func(input *ServerOptionInput) { input.AdvertiseFRPAddr = serverOption("2001:db8::1:7000") }},
		{name: "invalid session lifetime", mutate: func(input *ServerOptionInput) { input.SessionIdleDays = serverOption("0") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base()
			test.mutate(&input)
			if _, err := ResolveServerConfig(input, environment); err == nil {
				t.Fatal("ResolveServerConfig() unexpectedly succeeded")
			}
		})
	}
}

func TestResolveServerConfigPreservesLegacyNumericAndCredentialSemantics(t *testing.T) {
	input := ServerOptionInput{
		Address:          serverOption("127.0.0.1"),
		ControlPort:      serverOption("7.5e3"),
		FRPPort:          serverOption("0x1b59"),
		HTTPPort:         serverOption("8081"),
		PortRange:        serverOption("20000-20100"),
		AdvertiseFRPAddr: serverOption("[2001:db8::1]:7001"),
		DataDir:          serverOption(t.TempDir()),
		SessionIdleDays:  serverOption("9007199254740991"),
	}
	config, err := ResolveServerConfig(input, serverEnvironment(map[string]string{
		"YCY_TUNNEL_ADMIN_USER":     "abc\U0001f600",
		"YCY_TUNNEL_ADMIN_PASSWORD": "abc\U0001f600",
		"YCY_TUNNEL_FRP_TOKEN":      " \t",
	}))
	if err == nil || !strings.Contains(err.Error(), "administrator username") {
		t.Fatalf("non-ASCII administrator username error = %v", err)
	}

	config, err = ResolveServerConfig(input, serverEnvironment(map[string]string{
		"YCY_TUNNEL_ADMIN_USER":     "ops-admin",
		"YCY_TUNNEL_ADMIN_PASSWORD": "abc\U0001f600",
		"YCY_TUNNEL_FRP_TOKEN":      " configured-token ",
	}))
	if err != nil {
		t.Fatalf("ResolveServerConfig() error = %v", err)
	}
	if config.Settings.ControlPort != 7500 || config.Settings.FRPPort != 7001 || config.Settings.AdvertiseFRPAddr == nil || *config.Settings.AdvertiseFRPAddr != (ServerHTTPFRPAddress{Host: "2001:db8::1", Port: 7001}) || config.FRPToken != "configured-token" {
		t.Fatalf("legacy numeric config = %#v", config)
	}

	if _, err := ResolveServerConfig(input, serverEnvironment(map[string]string{
		"YCY_TUNNEL_ADMIN_USER":     "ops-admin",
		"YCY_TUNNEL_ADMIN_PASSWORD": "abc\U0001f600",
		"YCY_TUNNEL_FRP_TOKEN":      " \t",
	})); err == nil {
		t.Fatal("ResolveServerConfig() accepted an empty configured FRP token")
	}
}

func TestDefaultServerDataDirectoryUsesTunnelStateRoot(t *testing.T) {
	home := func() (string, error) { return "/Users/proof", nil }
	macOS, err := serverDataDirectory(func(string) string { return "" }, home, "darwin")
	if err != nil {
		t.Fatalf("serverDataDirectory() error = %v", err)
	}
	if want := filepath.Join("/Users/proof", "Library", "Application Support", "ycy", "tunnel", "server"); macOS != want {
		t.Fatalf("macOS server data directory = %q, want %q", macOS, want)
	}
	linux, err := serverDataDirectory(func(key string) string {
		if key == "XDG_STATE_HOME" {
			return "/state"
		}
		return ""
	}, home, "linux")
	if err != nil {
		t.Fatalf("serverDataDirectory() error = %v", err)
	}
	if want := filepath.Join("/state", "ycy", "tunnel", "server"); linux != want {
		t.Fatalf("Linux server data directory = %q, want %q", linux, want)
	}
}

func TestRunServerOwnsTheForegroundUntilContextCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve control port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release control port: %v", err)
	}
	dataDirectory := t.TempDir()
	config := ServerConfig{
		Settings: ServerHTTPServerSettings{
			Address: "127.0.0.1", ControlPort: port, FRPPort: 17000, HTTPPort: 18080,
			PortRange: ServerHTTPPortRange{Start: 20000, End: 20100}, DataDir: dataDirectory, AdminUser: "admin",
		},
		AdminPassword:       "environment-password",
		SessionIdleLifetime: time.Hour,
	}
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var diagnostics bytes.Buffer
	logRuntime := logging.NewRuntime(logging.Options{Writer: &diagnostics})
	finished := make(chan error, 1)
	go func() {
		finished <- RunServer(ctx, config, ServerRunOptions{
			Logger: logRuntime.Logger("tunnel.server"),
			newRuntime: func(ctx context.Context, options ServerRuntimeOptions) (*ServerRuntime, error) {
				options.frpArtifact = &artifact
				options.frpRuntimeDirectory = filepath.Join(t.TempDir(), "frp", tunnelruntime.FRPVersion)
				options.ensureFRPRuntime = func(context.Context, string, tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error) {
					return tunnelruntime.FRPRuntimePaths{}, errors.New("test runtime acquisition failure")
				}
				return NewServerRuntime(ctx, options)
			},
		})
	}()

	deadline := time.Now().Add(5 * time.Second)
	endpoint := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/healthz"
	for {
		response, requestErr := http.Get(endpoint)
		if requestErr == nil {
			if response.StatusCode != http.StatusOK {
				_ = response.Body.Close()
				t.Fatalf("GET /healthz status = %d, want %d", response.StatusCode, http.StatusOK)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatalf("close health response: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Tunnel server did not start at %s: %v", endpoint, requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("RunServer() error after cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunServer() did not return after cancellation")
	}
	if !strings.Contains(diagnostics.String(), "tunnel.server:") {
		t.Fatalf("Tunnel server diagnostics = %q, missing tunnel.server scope", diagnostics.String())
	}
	for _, expected := range []string{
		"Tunnel control plane started",
		"Tunnel server stopping",
		"Tunnel server stopped",
	} {
		if !strings.Contains(diagnostics.String(), expected) {
			t.Fatalf("Tunnel server diagnostics = %q, missing %q", diagnostics.String(), expected)
		}
	}

	reopened, err := NewServerRuntime(context.Background(), ServerRuntimeOptions{
		Settings:            config.Settings,
		AdminPassword:       config.AdminPassword,
		SessionIdleLifetime: config.SessionIdleLifetime,
		frpArtifact:         &artifact,
		frpRuntimeDirectory: filepath.Join(t.TempDir(), "frp", tunnelruntime.FRPVersion),
		ensureFRPRuntime: func(context.Context, string, tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error) {
			return tunnelruntime.FRPRuntimePaths{}, errors.New("test runtime acquisition failure")
		},
	})
	if err != nil {
		t.Fatalf("NewServerRuntime() after RunServer cancellation = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopened Runtime.Close() error = %v", err)
	}
}

func TestRunServerRichServiceBoundaryKeepsLifecycleLogOutsideConsole(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve control port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release control port: %v", err)
	}
	dataDirectory := filepath.Join(t.TempDir(), "server-state")
	config := ServerConfig{
		Settings: ServerHTTPServerSettings{
			Address: "127.0.0.1", ControlPort: port, FRPPort: 17000, HTTPPort: 18080,
			PortRange: ServerHTTPPortRange{Start: 20000, End: 20100}, DataDir: dataDirectory, AdminUser: "admin",
		},
		AdminPassword:       "server-admin-password",
		SessionIdleLifetime: time.Hour,
		FRPToken:            "server-frp-token",
	}
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{
			Interaction: terminal.RichInteractive,
			Stdout:      terminal.StreamCapability{Terminal: true, Color: true},
			Stderr:      terminal.StreamCapability{Terminal: true, Color: true},
		},
		Output:      &stdout,
		Diagnostics: &stderr,
	})
	logRuntime := logging.NewRuntime(logging.Options{
		Writer: experience.DiagnosticWriter(),
		Format: logging.JSONFormat,
		Color:  true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- RunServer(ctx, config, ServerRunOptions{
			Logger: logRuntime.Logger("tunnel.server"),
			newRuntime: func(ctx context.Context, options ServerRuntimeOptions) (*ServerRuntime, error) {
				options.frpArtifact = &artifact
				options.frpRuntimeDirectory = filepath.Join(t.TempDir(), "frp", tunnelruntime.FRPVersion)
				options.ensureFRPRuntime = func(context.Context, string, tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error) {
					return tunnelruntime.FRPRuntimePaths{}, errors.New("test runtime acquisition failure")
				}
				return NewServerRuntime(ctx, options)
			},
		})
	}()

	deadline := time.Now().Add(5 * time.Second)
	endpoint := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/healthz"
	for {
		response, requestErr := http.Get(endpoint)
		if requestErr == nil {
			if response.StatusCode != http.StatusOK {
				_ = response.Body.Close()
				t.Fatalf("GET /healthz status = %d, want %d", response.StatusCode, http.StatusOK)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatalf("close health response: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Tunnel server did not start at %s: %v", endpoint, requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("RunServer() error after cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunServer() did not return after cancellation")
	}

	if stdout.Len() != 0 {
		t.Fatalf("Tunnel server wrote stdout: %q", stdout.String())
	}
	if terminaltest.ContainsTerminalControl(stdout.Bytes()) || terminaltest.ContainsTerminalControl(stderr.Bytes()) {
		t.Fatalf("Tunnel server service boundary emitted terminal control: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, secret := range []string{config.AdminPassword, config.FRPToken, config.Settings.DataDir} {
		if strings.Contains(stderr.String(), secret) {
			t.Fatalf("Tunnel server Lifecycle Log leaked %q: %q", secret, stderr.String())
		}
	}

	var records []struct {
		Scope   string         `json:"scope"`
		Context map[string]any `json:"context"`
	}
	decoder := json.NewDecoder(strings.NewReader(stderr.String()))
	for {
		var record struct {
			Scope   string         `json:"scope"`
			Context map[string]any `json:"context"`
		}
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode tunnel server Lifecycle Log: %v\n%s", err, stderr.String())
		}
		records = append(records, record)
	}
	if len(records) < 6 {
		t.Fatalf("Tunnel server Lifecycle Log records = %#v", records)
	}
	for index, event := range []string{"server.starting", "state.opened", "control.listening", "server.started"} {
		if records[index].Scope != "tunnel.server" || records[index].Context["event"] != event {
			t.Fatalf("Lifecycle record %d = %#v, want event %q", index, records[index], event)
		}
	}
	last := records[len(records)-1]
	if last.Context["event"] != "server.stopped" || last.Context["outcome"] != "cancelled" {
		t.Fatalf("last Lifecycle Log record = %#v, want cancelled server.stopped", last)
	}
	shutdown := false
	for _, record := range records {
		if record.Context["event"] == "shutdown.requested" {
			shutdown = true
		}
		if record.Context["event"] == "server.failed" {
			t.Fatalf("cancellation emitted server.failed: %#v", records)
		}
	}
	if !shutdown {
		t.Fatalf("Lifecycle Log omitted shutdown.requested: %#v", records)
	}
}

func TestRunServerSkipsConstructionWhenItsContextIsAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := RunServer(ctx, ServerConfig{}, ServerRunOptions{
		newRuntime: func(context.Context, ServerRuntimeOptions) (*ServerRuntime, error) {
			called = true
			return nil, errors.New("unexpected runtime construction")
		},
	})
	if err != nil {
		t.Fatalf("RunServer() error = %v", err)
	}
	if called {
		t.Fatal("RunServer() constructed a runtime after cancellation")
	}
}

func serverOption(value string) *string {
	return &value
}

func serverEnvironment(values map[string]string) ServerEnvironment {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
