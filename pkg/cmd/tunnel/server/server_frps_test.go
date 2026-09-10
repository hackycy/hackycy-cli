package server

import (
	"context"
	"encoding/json"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type serverFRPSAuthentication struct {
	Method string `toml:"method"`
	Token  string `toml:"token"`
}

type serverFRPSPortRange struct {
	Start int64 `toml:"start"`
	End   int64 `toml:"end"`
}

type serverFRPSLog struct {
	To    string `toml:"to"`
	Level string `toml:"level"`
}

type serverFRPSConfiguration struct {
	BindAddr      string                   `toml:"bindAddr"`
	BindPort      int64                    `toml:"bindPort"`
	VhostHTTPPort int64                    `toml:"vhostHTTPPort"`
	Custom404Page string                   `toml:"custom404Page"`
	Auth          serverFRPSAuthentication `toml:"auth"`
	AllowPorts    []serverFRPSPortRange    `toml:"allowPorts"`
	Log           serverFRPSLog            `toml:"log"`
}

func TestManagedFRPSComposesTypedConfigurationAndRedactedState(t *testing.T) {
	dataDirectory := t.TempDir()
	supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: "/managed/frps", Role: tunnelruntime.FRPRoleServer})
	if err != nil {
		t.Fatalf("NewFRPSupervisor() error = %v", err)
	}
	managed, err := NewManagedFRPS(ManagedFRPSOptions{
		Settings: ServerHTTPServerSettings{
			Address:          "127.0.0.1",
			ControlPort:      7500,
			FRPPort:          7000,
			HTTPPort:         8080,
			PortRange:        ServerHTTPPortRange{Start: 20000, End: 20100},
			AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "tunnel.example.test", Port: 7000},
			DataDir:          dataDirectory,
			AdminUser:        "admin",
		},
		InternalFRPToken: "internal-frp-token",
		Supervisor:       supervisor,
	})
	if err != nil {
		t.Fatalf("NewManagedFRPS() error = %v", err)
	}

	if got, want := managed.ConfigurationPath(), filepath.Join(dataDirectory, "frps.toml"); got != want {
		t.Fatalf("ConfigurationPath() = %q, want %q", got, want)
	}
	if got, want := managed.Custom404PagePath(), filepath.Join(dataDirectory, "404.html"); got != want {
		t.Fatalf("Custom404PagePath() = %q, want %q", got, want)
	}
	rendered, err := managed.RenderConfiguration()
	if err != nil {
		t.Fatalf("RenderConfiguration() error = %v", err)
	}
	var document serverFRPSConfiguration
	if err := toml.Unmarshal([]byte(rendered), &document); err != nil {
		t.Fatalf("decode rendered configuration: %v", err)
	}
	if document.BindAddr != "127.0.0.1" || document.BindPort != 7000 || document.VhostHTTPPort != 8080 || document.Custom404Page != managed.Custom404PagePath() || document.Auth.Method != "token" || document.Auth.Token != "internal-frp-token" || len(document.AllowPorts) != 1 || document.AllowPorts[0] != (serverFRPSPortRange{Start: 20000, End: 20100}) || document.Log != (serverFRPSLog{To: "console", Level: "warn"}) {
		t.Fatalf("rendered frps configuration = %#v", document)
	}
	for _, prohibited := range []string{"dashboard", "metrics", "admin", "plugin"} {
		if strings.Contains(rendered, prohibited) {
			t.Fatalf("rendered frps configuration unexpectedly contains %q:\n%s", prohibited, rendered)
		}
	}

	runtimeError := tunnelruntime.StructuredRuntimeError{Code: "FRPS_FAILED", Message: "address already in use"}
	if err := supervisor.ConfigurationFailed(runtimeError); err != nil {
		t.Fatalf("ConfigurationFailed() error = %v", err)
	}
	state := managed.State()
	settings := state.Settings
	if state.FRPS.State != tunnelruntime.FRPProcessConfigurationFailed || state.FRPS.Error == nil || *state.FRPS.Error != runtimeError || settings.Address != "127.0.0.1" || settings.ControlPort != 7500 || settings.FRPPort != 7000 || settings.HTTPPort != 8080 || settings.PortRange != (ServerHTTPPortRange{Start: 20000, End: 20100}) || settings.AdvertiseFRPAddr == nil || *settings.AdvertiseFRPAddr != (ServerHTTPFRPAddress{Host: "tunnel.example.test", Port: 7000}) || settings.DataDir != dataDirectory || settings.AdminUser != "admin" {
		t.Fatalf("State() = %#v", state)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal State(): %v", err)
	}
	for _, secret := range []string{"internal-frp-token", "adminPassword", "frpToken"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("State() leaked %q: %s", secret, encoded)
		}
	}
	state.Settings.AdvertiseFRPAddr.Host = "mutated.example.test"
	state.FRPS.Error.Message = "mutated"
	current := managed.State()
	if current.Settings.AdvertiseFRPAddr == nil || current.Settings.AdvertiseFRPAddr.Host != "tunnel.example.test" || current.FRPS.Error == nil || current.FRPS.Error.Message != "address already in use" {
		t.Fatalf("State() exposed mutable internals: %#v", current)
	}
}

func TestManagedFRPSRequiresCompositionDependencies(t *testing.T) {
	supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: "/managed/frps", Role: tunnelruntime.FRPRoleServer})
	if err != nil {
		t.Fatalf("NewFRPSupervisor() error = %v", err)
	}
	base := ManagedFRPSOptions{
		Settings:         ServerHTTPServerSettings{DataDir: t.TempDir()},
		InternalFRPToken: "internal-frp-token",
		Supervisor:       supervisor,
	}
	for _, test := range []struct {
		name    string
		options ManagedFRPSOptions
	}{
		{name: "missing supervisor", options: func() ManagedFRPSOptions { value := base; value.Supervisor = nil; return value }()},
		{name: "missing data directory", options: func() ManagedFRPSOptions { value := base; value.Settings.DataDir = "  "; return value }()},
		{name: "missing internal token", options: func() ManagedFRPSOptions { value := base; value.InternalFRPToken = "  "; return value }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewManagedFRPS(test.options)
			if !errors.Is(err, ErrManagedFRPSConfiguration) {
				t.Fatalf("NewManagedFRPS() error = %v", err)
			}
		})
	}
}

func TestManagedFRPSProvidesAgentWelcomeSettings(t *testing.T) {
	newManaged := func(t *testing.T, settings ServerHTTPServerSettings) *ManagedFRPS {
		t.Helper()
		settings.DataDir = t.TempDir()
		supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: "/managed/frps", Role: tunnelruntime.FRPRoleServer})
		if err != nil {
			t.Fatalf("NewFRPSupervisor() error = %v", err)
		}
		managed, err := NewManagedFRPS(ManagedFRPSOptions{Settings: settings, InternalFRPToken: "agent-only-token", Supervisor: supervisor})
		if err != nil {
			t.Fatalf("NewManagedFRPS() error = %v", err)
		}
		return managed
	}

	defaultSettings := newManaged(t, ServerHTTPServerSettings{FRPPort: 7000})
	if got := defaultSettings.AgentWelcomeSettings("request.example.test"); got != (ServerAgentWelcomeSettings{AdvertisedFRPHost: "request.example.test", AdvertisedFRPPort: 7000, InternalFRPToken: "agent-only-token"}) {
		t.Fatalf("default AgentWelcomeSettings() = %#v", got)
	}
	overriddenSettings := newManaged(t, ServerHTTPServerSettings{FRPPort: 7000, AdvertiseFRPAddr: &ServerHTTPFRPAddress{Host: "public.example.test", Port: 7443}})
	if got := overriddenSettings.AgentWelcomeSettings("request.example.test"); got != (ServerAgentWelcomeSettings{AdvertisedFRPHost: "public.example.test", AdvertisedFRPPort: 7443, InternalFRPToken: "agent-only-token"}) {
		t.Fatalf("overridden AgentWelcomeSettings() = %#v", got)
	}
}

func TestManagedFRPSReadsCustom404Page(t *testing.T) {
	newManaged := func(t *testing.T, dataDirectory string) *ManagedFRPS {
		t.Helper()
		supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: "/managed/frps", Role: tunnelruntime.FRPRoleServer})
		if err != nil {
			t.Fatalf("NewFRPSupervisor() error = %v", err)
		}
		managed, err := NewManagedFRPS(ManagedFRPSOptions{
			Settings:         ServerHTTPServerSettings{DataDir: dataDirectory},
			InternalFRPToken: "internal-frp-token",
			Supervisor:       supervisor,
		})
		if err != nil {
			t.Fatalf("NewManagedFRPS() error = %v", err)
		}
		return managed
	}

	managed := newManaged(t, t.TempDir())
	content, err := managed.ReadCustom404Page()
	if err != nil || content != "" {
		t.Fatalf("ReadCustom404Page() for missing page = (%q, %v)", content, err)
	}
	missingParent := filepath.Join(t.TempDir(), "missing", "nested")
	content, err = newManaged(t, filepath.Dir(missingParent)).ReadCustom404Page()
	if err != nil || content != "" {
		t.Fatalf("ReadCustom404Page() for missing parent = (%q, %v)", content, err)
	}
	if err := os.WriteFile(managed.Custom404PagePath(), []byte("<main>custom page</main>"), 0o600); err != nil {
		t.Fatalf("write custom 404 fixture: %v", err)
	}
	content, err = managed.ReadCustom404Page()
	if err != nil || content != "<main>custom page</main>" {
		t.Fatalf("ReadCustom404Page() = (%q, %v)", content, err)
	}

	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocked fixture: %v", err)
	}
	_, err = newManaged(t, blocked).ReadCustom404Page()
	var domainError *ServerDomainError
	if !errors.As(err, &domainError) || domainError.Code != "CONFIGURATION_FAILED" || !strings.Contains(domainError.Message, "Could not read custom 404 page") {
		t.Fatalf("ReadCustom404Page() blocked error = %v", err)
	}
}

func TestManagedFRPSWritesAndRemovesCustom404Page(t *testing.T) {
	newManaged := func(t *testing.T, dataDirectory string) *ManagedFRPS {
		t.Helper()
		supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: "/managed/frps", Role: tunnelruntime.FRPRoleServer})
		if err != nil {
			t.Fatalf("NewFRPSupervisor() error = %v", err)
		}
		managed, err := NewManagedFRPS(ManagedFRPSOptions{
			Settings:         ServerHTTPServerSettings{DataDir: dataDirectory},
			InternalFRPToken: "internal-frp-token",
			Supervisor:       supervisor,
		})
		if err != nil {
			t.Fatalf("NewManagedFRPS() error = %v", err)
		}
		return managed
	}

	managed := newManaged(t, filepath.Join(t.TempDir(), "server-state"))
	if err := managed.WriteCustom404Page("<main>first version</main>"); err != nil {
		t.Fatalf("WriteCustom404Page(first) error = %v", err)
	}
	contents, err := os.ReadFile(managed.Custom404PagePath())
	if err != nil || string(contents) != "<main>first version</main>" {
		t.Fatalf("custom 404 file after first write = (%q, %v)", contents, err)
	}
	assertTunnelPrivateFile(t, managed.Custom404PagePath(), 0o600)
	if err := managed.WriteCustom404Page("<main>second version</main>"); err != nil {
		t.Fatalf("WriteCustom404Page(second) error = %v", err)
	}
	if candidates, err := filepath.Glob(managed.Custom404PagePath() + ".candidate-*"); err != nil || len(candidates) != 0 {
		t.Fatalf("custom 404 candidates = %v, glob error = %v", candidates, err)
	}

	err = managed.WriteCustom404Page(strings.Repeat("x", 512*1024+1))
	var oversized *ServerDomainError
	if !errors.As(err, &oversized) || oversized.Code != "INVALID_CUSTOM_404_PAGE" || oversized.Message != "Custom 404 page must not exceed 512 KiB" {
		t.Fatalf("WriteCustom404Page(oversized) error = %v", err)
	}
	contents, err = os.ReadFile(managed.Custom404PagePath())
	if err != nil || string(contents) != "<main>second version</main>" {
		t.Fatalf("custom 404 file after oversized write = (%q, %v)", contents, err)
	}

	if err := managed.WriteCustom404Page(""); err != nil {
		t.Fatalf("WriteCustom404Page(remove) error = %v", err)
	}
	if _, err := os.Stat(managed.Custom404PagePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("custom 404 file after removal = %v", err)
	}
	if err := managed.WriteCustom404Page(""); err != nil {
		t.Fatalf("WriteCustom404Page(repeated removal) error = %v", err)
	}

	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocked fixture: %v", err)
	}
	err = newManaged(t, blocked).WriteCustom404Page("<main>blocked</main>")
	var configurationFailure *ServerDomainError
	if !errors.As(err, &configurationFailure) || configurationFailure.Code != "CONFIGURATION_FAILED" || !strings.Contains(configurationFailure.Message, "Could not write custom 404 page") {
		t.Fatalf("WriteCustom404Page() blocked error = %v", err)
	}
}

func TestManagedFRPSPublishesConfiguration(t *testing.T) {
	newManaged := func(t *testing.T, dataDirectory string) *ManagedFRPS {
		t.Helper()
		supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: "/managed/frps", Role: tunnelruntime.FRPRoleServer})
		if err != nil {
			t.Fatalf("NewFRPSupervisor() error = %v", err)
		}
		managed, err := NewManagedFRPS(ManagedFRPSOptions{
			Settings: ServerHTTPServerSettings{
				Address:   "127.0.0.1",
				FRPPort:   7000,
				HTTPPort:  8080,
				PortRange: ServerHTTPPortRange{Start: 20000, End: 20100},
				DataDir:   dataDirectory,
			},
			InternalFRPToken: "internal-frp-token",
			Supervisor:       supervisor,
		})
		if err != nil {
			t.Fatalf("NewManagedFRPS() error = %v", err)
		}
		return managed
	}

	managed := newManaged(t, filepath.Join(t.TempDir(), "server-state"))
	if err := os.MkdirAll(filepath.Dir(managed.ConfigurationPath()), 0o700); err != nil {
		t.Fatalf("create configuration parent: %v", err)
	}
	if err := os.WriteFile(managed.ConfigurationPath(), []byte("old configuration"), 0o600); err != nil {
		t.Fatalf("write old configuration: %v", err)
	}
	if err := managed.PublishConfiguration(); err != nil {
		t.Fatalf("PublishConfiguration() error = %v", err)
	}
	contents, err := os.ReadFile(managed.ConfigurationPath())
	if err != nil {
		t.Fatalf("read published configuration: %v", err)
	}
	var document serverFRPSConfiguration
	if err := toml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("decode published configuration: %v", err)
	}
	if document.BindAddr != "127.0.0.1" || document.BindPort != 7000 || document.VhostHTTPPort != 8080 || document.Custom404Page != managed.Custom404PagePath() || document.Auth.Token != "internal-frp-token" || len(document.AllowPorts) != 1 || document.AllowPorts[0] != (serverFRPSPortRange{Start: 20000, End: 20100}) {
		t.Fatalf("published configuration = %#v", document)
	}
	assertTunnelPrivateFile(t, managed.ConfigurationPath(), 0o600)
	if candidates, err := filepath.Glob(managed.ConfigurationPath() + ".candidate-*"); err != nil || len(candidates) != 0 {
		t.Fatalf("configuration candidates = %v, glob error = %v", candidates, err)
	}

	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocked fixture: %v", err)
	}
	err = newManaged(t, blocked).PublishConfiguration()
	var configurationFailure *ServerDomainError
	if !errors.As(err, &configurationFailure) || configurationFailure.Code != "CONFIGURATION_FAILED" || !strings.Contains(configurationFailure.Message, "Could not write frps configuration") {
		t.Fatalf("PublishConfiguration() blocked error = %v", err)
	}
}

func TestManagedFRPSPublishesRuntimeAndCustomPageChanges(t *testing.T) {
	supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: "/managed/frps", Role: tunnelruntime.FRPRoleServer})
	if err != nil {
		t.Fatalf("NewFRPSupervisor() error = %v", err)
	}
	managed, err := NewManagedFRPS(ManagedFRPSOptions{
		Settings: ServerHTTPServerSettings{
			Address: "127.0.0.1", FRPPort: 7000, HTTPPort: 8080,
			PortRange: ServerHTTPPortRange{Start: 20000, End: 20100}, DataDir: t.TempDir(),
		},
		InternalFRPToken: "internal-frp-token",
		Supervisor:       supervisor,
	})
	if err != nil {
		t.Fatalf("NewManagedFRPS() error = %v", err)
	}

	changes := make(chan struct{}, 2)
	stop := managed.ObserveFRPSChanges(func() { changes <- struct{}{} })
	defer stop()
	select {
	case <-changes:
		t.Fatal("ObserveFRPSChanges() emitted an initial invalidation")
	default:
	}

	if err := supervisor.ConfigurationFailed(tunnelruntime.StructuredRuntimeError{Code: "CONFIGURATION_FAILED", Message: "fixture failure"}); err != nil {
		t.Fatalf("ConfigurationFailed() error = %v", err)
	}
	awaitManagedFRPSChange(t, changes)
	if err := managed.WriteCustom404Page("<main>custom 404</main>"); err != nil {
		t.Fatalf("WriteCustom404Page() error = %v", err)
	}
	awaitManagedFRPSChange(t, changes)
}

func TestManagedFRPSPreparesRuntimeOnStartAndRestart(t *testing.T) {
	supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: "/managed/frps", Role: tunnelruntime.FRPRoleServer})
	if err != nil {
		t.Fatalf("NewFRPSupervisor() error = %v", err)
	}
	preparations := 0
	managed, err := NewManagedFRPS(ManagedFRPSOptions{
		Settings:         ServerHTTPServerSettings{DataDir: t.TempDir()},
		InternalFRPToken: "internal-frp-token",
		Supervisor:       supervisor,
		Prepare: func(context.Context) error {
			preparations++
			return errors.New("fixture runtime acquisition failed")
		},
	})
	if err != nil {
		t.Fatalf("NewManagedFRPS() error = %v", err)
	}
	for _, operation := range []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "start", run: managed.Start},
		{name: "restart", run: managed.Restart},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run(context.Background())
			var failure *ServerDomainError
			if !errors.As(err, &failure) || failure.Code != "CONFIGURATION_FAILED" || !strings.Contains(failure.Message, "fixture runtime acquisition failed") {
				t.Fatalf("%s error = %v", operation.name, err)
			}
		})
	}
	if preparations != 2 {
		t.Fatalf("runtime preparations = %d, want 2", preparations)
	}
}

func awaitManagedFRPSChange(t *testing.T, changes <-chan struct{}) {
	t.Helper()
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for managed frps change")
	}
}
