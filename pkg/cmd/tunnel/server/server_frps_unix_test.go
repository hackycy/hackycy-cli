//go:build darwin || linux

package server

import (
	"context"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedFRPSVerifiesPublishedConfiguration(t *testing.T) {
	root := t.TempDir()
	verifiedPath := filepath.Join(root, "verified-path")
	binary := writeFRPSupervisorScript(t, root, "verify", "#!/bin/sh\nif [ \"$1\" != verify ] || [ \"$2\" != -c ]; then exit 9; fi\nprintf '%s' \"$3\" > \"$FRP_VERIFIED_PATH\"\n")
	t.Setenv("FRP_VERIFIED_PATH", verifiedPath)
	newManaged := func(t *testing.T, binaryPath string) *ManagedFRPS {
		t.Helper()
		supervisor := newTestFRPSupervisor(t, tunnelruntime.FRPSupervisorOptions{BinaryPath: binaryPath, Role: tunnelruntime.FRPRoleServer})
		managed, err := NewManagedFRPS(ManagedFRPSOptions{
			Settings:         ServerHTTPServerSettings{DataDir: filepath.Join(root, filepath.Base(binaryPath)+"-state")},
			InternalFRPToken: "internal-frp-token",
			Supervisor:       supervisor,
		})
		if err != nil {
			t.Fatalf("NewManagedFRPS() error = %v", err)
		}
		if err := managed.PublishConfiguration(); err != nil {
			t.Fatalf("PublishConfiguration() error = %v", err)
		}
		return managed
	}

	managed := newManaged(t, binary)
	if err := managed.VerifyPublishedConfiguration(context.Background()); err != nil {
		t.Fatalf("VerifyPublishedConfiguration() error = %v", err)
	}
	contents, err := os.ReadFile(verifiedPath)
	if err != nil || string(contents) != managed.ConfigurationPath() {
		t.Fatalf("verified configuration path = (%q, %v), want %q", contents, err, managed.ConfigurationPath())
	}

	invalid := writeFRPSupervisorScript(t, root, "invalid", "#!/bin/sh\nprintf 'invalid generated configuration\\n' >&2\nexit 7\n")
	err = newManaged(t, invalid).VerifyPublishedConfiguration(context.Background())
	var configurationFailure *ServerDomainError
	if !errors.As(err, &configurationFailure) || configurationFailure.Code != "CONFIGURATION_FAILED" || !strings.Contains(configurationFailure.Message, "invalid generated configuration") {
		t.Fatalf("VerifyPublishedConfiguration() invalid error = %v", err)
	}

	timedOut := writeFRPSupervisorScript(t, root, "timeout", "#!/bin/sh\nsleep 1\n")
	err = verifyFRPSConfiguration(context.Background(), timedOut, managed.ConfigurationPath(), 20*time.Millisecond)
	if !errors.Is(err, ErrFRPSConfigurationVerificationTimeout) {
		t.Fatalf("verifyFRPSConfiguration() timeout error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err = verifyFRPSConfiguration(canceled, binary, managed.ConfigurationPath(), time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("verifyFRPSConfiguration() cancellation error = %v", err)
	}
}

func TestManagedFRPSStartsFromPublishedAndVerifiedConfiguration(t *testing.T) {
	root := t.TempDir()
	callsPath := filepath.Join(root, "calls")
	binary := writeFRPSupervisorScript(t, root, "managed", "#!/bin/sh\nif [ \"$1\" = verify ] && [ \"$2\" = -c ]; then\n  printf 'verify:%s\\n' \"$3\" >> \"$FRP_CALLS\"\n  exit 0\nfi\nif [ \"$1\" = -c ]; then\n  printf 'start:%s\\n' \"$2\" >> \"$FRP_CALLS\"\n  while :; do sleep 1; done\nfi\nexit 9\n")
	t.Setenv("FRP_CALLS", callsPath)
	newManaged := func(t *testing.T, binaryPath string) (*ManagedFRPS, *tunnelruntime.FRPSupervisor) {
		t.Helper()
		supervisor := newTestFRPSupervisor(t, tunnelruntime.FRPSupervisorOptions{BinaryPath: binaryPath, Role: tunnelruntime.FRPRoleServer, ActivationGrace: 20 * time.Millisecond})
		managed, err := NewManagedFRPS(ManagedFRPSOptions{
			Settings:         ServerHTTPServerSettings{DataDir: filepath.Join(root, filepath.Base(binaryPath)+"-state")},
			InternalFRPToken: "internal-frp-token",
			Supervisor:       supervisor,
		})
		if err != nil {
			t.Fatalf("NewManagedFRPS() error = %v", err)
		}
		return managed, supervisor
	}

	managed, supervisor := newManaged(t, binary)
	if err := managed.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	contents, err := os.ReadFile(callsPath)
	if err != nil || string(contents) != "verify:"+managed.ConfigurationPath()+"\nstart:"+managed.ConfigurationPath()+"\n" {
		t.Fatalf("managed FRPS calls = (%q, %v)", contents, err)
	}
	if state := supervisor.State(); state.State != tunnelruntime.FRPProcessRunning || state.PID == nil {
		t.Fatalf("Start() state = %#v", state)
	}

	invalid := writeFRPSupervisorScript(t, root, "invalid-start", "#!/bin/sh\nif [ \"$1\" = verify ]; then\n  printf 'candidate rejected\\n' >&2\n  exit 7\nfi\nprintf 'unexpected activation\\n' >> \"$FRP_CALLS\"\nexit 9\n")
	invalidManaged, invalidSupervisor := newManaged(t, invalid)
	err = invalidManaged.Start(context.Background())
	var configurationFailure *ServerDomainError
	if !errors.As(err, &configurationFailure) || configurationFailure.Code != "CONFIGURATION_FAILED" || !strings.Contains(configurationFailure.Message, "candidate rejected") {
		t.Fatalf("Start() verification error = %v", err)
	}
	state := invalidSupervisor.State()
	if state.State != tunnelruntime.FRPProcessConfigurationFailed || state.Error == nil || state.Error.Code != "CONFIGURATION_FAILED" || !strings.Contains(state.Error.Message, "candidate rejected") {
		t.Fatalf("Start() verification failure state = %#v", state)
	}
	contents, err = os.ReadFile(callsPath)
	if err != nil || strings.Contains(string(contents), "unexpected activation") {
		t.Fatalf("managed FRPS calls after verification failure = (%q, %v)", contents, err)
	}

	activationFailure := writeFRPSupervisorScript(t, root, "activation-failure", "#!/bin/sh\nif [ \"$1\" = verify ]; then exit 0; fi\nexit 23\n")
	failedManaged, failedSupervisor := newManaged(t, activationFailure)
	err = failedManaged.Start(context.Background())
	var activationError *ServerDomainError
	if !errors.As(err, &activationError) || activationError.Code != "ACTIVATION_FAILED" || !strings.Contains(activationError.Message, "Managed frps failed to start") {
		t.Fatalf("Start() activation error = %v", err)
	}
	state = failedSupervisor.State()
	if state.State != tunnelruntime.FRPProcessConfigurationFailed || state.Error == nil || state.Error.Code != "ACTIVATION_FAILED" {
		t.Fatalf("Start() activation failure state = %#v", state)
	}
}

func TestManagedFRPSStopsTheActiveChild(t *testing.T) {
	root := t.TempDir()
	callsPath := filepath.Join(root, "calls")
	binary := writeFRPSupervisorScript(t, root, "managed-stop", "#!/bin/sh\nif [ \"$1\" = verify ]; then\n  printf 'verify\\n' >> \"$FRP_CALLS\"\n  exit 0\nfi\nprintf 'start\\n' >> \"$FRP_CALLS\"\nwhile :; do sleep 1; done\n")
	t.Setenv("FRP_CALLS", callsPath)
	supervisor := newTestFRPSupervisor(t, tunnelruntime.FRPSupervisorOptions{BinaryPath: binary, Role: tunnelruntime.FRPRoleServer, ActivationGrace: 20 * time.Millisecond})
	managed, err := NewManagedFRPS(ManagedFRPSOptions{
		Settings:         ServerHTTPServerSettings{DataDir: filepath.Join(root, "state")},
		InternalFRPToken: "internal-frp-token",
		Supervisor:       supervisor,
	})
	if err != nil {
		t.Fatalf("NewManagedFRPS() error = %v", err)
	}
	if err := managed.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := managed.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if state := supervisor.State(); state.State != tunnelruntime.FRPProcessStopped || state.PID != nil || state.Error != nil {
		t.Fatalf("Stop() state = %#v", state)
	}
	if err := managed.Stop(); err != nil {
		t.Fatalf("Stop() after stop error = %v", err)
	}
	contents, err := os.ReadFile(callsPath)
	if err != nil || string(contents) != "verify\nstart\n" {
		t.Fatalf("managed FRPS calls = (%q, %v)", contents, err)
	}
}

func TestManagedFRPSRestartsThroughPublicationAndVerification(t *testing.T) {
	root := t.TempDir()
	callsPath := filepath.Join(root, "calls")
	binary := writeFRPSupervisorScript(t, root, "managed-restart", "#!/bin/sh\nif [ \"$1\" = verify ]; then\n  printf 'verify:%s\\n' \"$3\" >> \"$FRP_CALLS\"\n  exit 0\nfi\nprintf 'start:%s\\n' \"$2\" >> \"$FRP_CALLS\"\nwhile :; do sleep 1; done\n")
	t.Setenv("FRP_CALLS", callsPath)
	supervisor := newTestFRPSupervisor(t, tunnelruntime.FRPSupervisorOptions{BinaryPath: binary, Role: tunnelruntime.FRPRoleServer, ActivationGrace: 20 * time.Millisecond})
	managed, err := NewManagedFRPS(ManagedFRPSOptions{
		Settings:         ServerHTTPServerSettings{DataDir: filepath.Join(root, "state")},
		InternalFRPToken: "internal-frp-token",
		Supervisor:       supervisor,
	})
	if err != nil {
		t.Fatalf("NewManagedFRPS() error = %v", err)
	}
	if err := managed.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := managed.Restart(context.Background()); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	want := "verify:" + managed.ConfigurationPath() + "\nstart:" + managed.ConfigurationPath() + "\nverify:" + managed.ConfigurationPath() + "\nstart:" + managed.ConfigurationPath() + "\n"
	contents, err := os.ReadFile(callsPath)
	if err != nil || string(contents) != want {
		t.Fatalf("managed FRPS calls = (%q, %v), want %q", contents, err, want)
	}
	if state := supervisor.State(); state.State != tunnelruntime.FRPProcessRunning || state.PID == nil {
		t.Fatalf("Restart() state = %#v", state)
	}
}
