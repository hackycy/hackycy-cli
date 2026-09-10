package server

import (
	"context"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerRuntimeBackgroundFRPAcquisitionFailureKeepsControlPlaneAvailable(t *testing.T) {
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
	dataDirectory := t.TempDir()
	runtimeDirectory := filepath.Join(t.TempDir(), "frp", tunnelruntime.FRPVersion)
	type acquisitionCall struct {
		directory string
		artifact  tunnelruntime.FRPArtifact
	}
	calls := make(chan acquisitionCall, 1)
	options := ServerRuntimeOptions{
		Settings: ServerHTTPServerSettings{
			Address:     "127.0.0.1",
			ControlPort: 0,
			FRPPort:     7000,
			HTTPPort:    8080,
			PortRange:   ServerHTTPPortRange{Start: 20000, End: 20100},
			DataDir:     dataDirectory,
			AdminUser:   "admin",
		},
		AdminPassword:       "environment-password",
		frpArtifact:         &artifact,
		frpRuntimeDirectory: runtimeDirectory,
		ensureFRPRuntime: func(_ context.Context, directory string, received tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error) {
			calls <- acquisitionCall{directory: directory, artifact: received}
			return tunnelruntime.FRPRuntimePaths{}, errors.New("download fixture failed")
		},
	}

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
			if state.Error == nil || state.Error.Code != "CONFIGURATION_FAILED" || !strings.Contains(state.Error.Message, "download fixture failed") {
				t.Fatalf("acquisition failure state = %#v", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background FRPS state = %#v, want acquisition failure", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	call := <-calls
	if call.directory != runtimeDirectory || call.artifact != artifact {
		t.Fatalf("runtime acquisition input = %#v, want directory %q and current artifact %#v", call, runtimeDirectory, artifact)
	}

	response, err := http.Get(server.URL() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz after acquisition failure: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz after acquisition failure status = %d", response.StatusCode)
	}
}

func TestServerRuntimeRejectsPreparedPathsOutsidePinnedRuntime(t *testing.T) {
	artifact, err := tunnelruntime.CurrentFRPArtifact()
	if err != nil {
		t.Fatalf("CurrentFRPArtifact() error = %v", err)
	}
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
		AdminPassword:       "environment-password",
		frpArtifact:         &artifact,
		frpRuntimeDirectory: filepath.Join(t.TempDir(), "frp", tunnelruntime.FRPVersion),
		ensureFRPRuntime: func(context.Context, string, tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error) {
			return tunnelruntime.FRPRuntimePaths{Directory: t.TempDir(), FRPC: "/outside/frpc", FRPS: "/outside/frps"}, nil
		},
	}
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
			if state.Error == nil || !strings.Contains(state.Error.Message, "outside the pinned runtime directory") {
				t.Fatalf("untrusted prepared path state = %#v", state)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background FRPS state = %#v, want pinned-path failure", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
