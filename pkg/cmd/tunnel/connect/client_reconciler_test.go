package connect

import (
	"context"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type clientFRPRuntimeStub struct {
	calls       []string
	verifyErr   error
	startErrors []error
	verified    []string
	started     []string
}

func (runtime *clientFRPRuntimeStub) Verify(_ context.Context, configurationPath string) error {
	runtime.calls = append(runtime.calls, "verify")
	contents, err := os.ReadFile(configurationPath)
	if err != nil {
		return err
	}
	runtime.verified = append(runtime.verified, string(contents))
	return runtime.verifyErr
}

func (runtime *clientFRPRuntimeStub) Start(configurationPath string) error {
	runtime.calls = append(runtime.calls, "start")
	contents, err := os.ReadFile(configurationPath)
	if err != nil {
		return err
	}
	runtime.started = append(runtime.started, string(contents))
	if len(runtime.startErrors) == 0 {
		return nil
	}
	err = runtime.startErrors[0]
	runtime.startErrors = runtime.startErrors[1:]
	return err
}

func (runtime *clientFRPRuntimeStub) Stop() error {
	runtime.calls = append(runtime.calls, "stop")
	return nil
}

func TestClientReconcilerVerifiesPublishesStartsAndCachesOneDesiredRevision(t *testing.T) {
	directory := t.TempDir()
	runtime := &clientFRPRuntimeStub{}
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: runtime, LogLevel: "info"})
	if err != nil {
		t.Fatalf("NewClientReconciler() error = %v", err)
	}
	desired := clientDesiredState(2, true)
	if err := reconciler.Apply(context.Background(), desired); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := strings.Join(runtime.calls, ","), "verify,stop,start"; got != want {
		t.Fatalf("runtime calls = %q, want %q", got, want)
	}
	if len(runtime.verified) != 1 || !strings.Contains(runtime.verified[0], "t_tunnel-2") || len(runtime.started) != 1 || runtime.started[0] != runtime.verified[0] {
		t.Fatalf("verified/started configuration = %#v/%#v", runtime.verified, runtime.started)
	}
	if _, err := os.Stat(filepath.Join(directory, "frpc.revision-2.candidate.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate remains after Apply(): %v", err)
	}
	if cache, ok := ReadClientAppliedState(directory); !ok || cache.Revision != 2 || cache.Snapshot.Revision != 2 {
		t.Fatalf("ReadClientAppliedState() = (%#v, %t)", cache, ok)
	}
}

func TestClientReconcilerLeavesPreviousStateUntouchedOnVerificationFailure(t *testing.T) {
	directory := t.TempDir()
	previous := clientDesiredState(1, true)
	if err := writeClientFileAtomically(clientActiveFRPCConfigPath(directory), []byte("previous configuration")); err != nil {
		t.Fatalf("write previous config: %v", err)
	}
	if err := WriteClientAppliedState(directory, ClientAppliedState{ClientDesiredConfiguration: previous, Revision: 1}); err != nil {
		t.Fatalf("write previous state: %v", err)
	}
	runtime := &clientFRPRuntimeStub{verifyErr: errors.New("candidate rejected")}
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: runtime})
	if err != nil {
		t.Fatalf("NewClientReconciler() error = %v", err)
	}
	err = reconciler.Apply(context.Background(), clientDesiredState(2, true))
	if clientReconciliationErrorCode(err) != "CONFIGURATION_FAILED" {
		t.Fatalf("Apply() error = %v, want CONFIGURATION_FAILED", err)
	}
	if got, want := strings.Join(runtime.calls, ","), "verify"; got != want {
		t.Fatalf("runtime calls = %q, want %q", got, want)
	}
	contents, readErr := os.ReadFile(clientActiveFRPCConfigPath(directory))
	if readErr != nil || string(contents) != "previous configuration" {
		t.Fatalf("active config = (%q, %v)", contents, readErr)
	}
	if cache, ok := ReadClientAppliedState(directory); !ok || cache.Revision != 1 {
		t.Fatalf("cache after verification failure = (%#v, %t)", cache, ok)
	}
}

func TestClientReconcilerRollsBackFileAndChildAfterActivationFailure(t *testing.T) {
	directory := t.TempDir()
	previous := clientDesiredState(1, true)
	if err := writeClientFileAtomically(clientActiveFRPCConfigPath(directory), []byte("previous configuration")); err != nil {
		t.Fatalf("write previous config: %v", err)
	}
	if err := WriteClientAppliedState(directory, ClientAppliedState{ClientDesiredConfiguration: previous, Revision: 1}); err != nil {
		t.Fatalf("write previous state: %v", err)
	}
	runtime := &clientFRPRuntimeStub{startErrors: []error{errors.New("candidate exited")}}
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: runtime})
	if err != nil {
		t.Fatalf("NewClientReconciler() error = %v", err)
	}
	err = reconciler.Apply(context.Background(), clientDesiredState(2, true))
	if clientReconciliationErrorCode(err) != "ACTIVATION_FAILED" {
		t.Fatalf("Apply() error = %v, want ACTIVATION_FAILED", err)
	}
	if got, want := strings.Join(runtime.calls, ","), "verify,stop,start,stop,start"; got != want {
		t.Fatalf("runtime calls = %q, want %q", got, want)
	}
	contents, readErr := os.ReadFile(clientActiveFRPCConfigPath(directory))
	if readErr != nil || string(contents) != "previous configuration" {
		t.Fatalf("active config after rollback = (%q, %v)", contents, readErr)
	}
	if cache, ok := ReadClientAppliedState(directory); !ok || cache.Revision != 1 {
		t.Fatalf("cache after rollback = (%#v, %t)", cache, ok)
	}
}

func TestClientReconcilerSkipsVerificationAndChildStartForAnEmptyEnabledSet(t *testing.T) {
	directory := t.TempDir()
	runtime := &clientFRPRuntimeStub{}
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: runtime})
	if err != nil {
		t.Fatalf("NewClientReconciler() error = %v", err)
	}
	desired := clientDesiredState(3, false)
	if err := reconciler.Apply(context.Background(), desired); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := strings.Join(runtime.calls, ","), "stop"; got != want {
		t.Fatalf("runtime calls = %q, want %q", got, want)
	}
	if cache, ok := ReadClientAppliedState(directory); !ok || cache.Revision != 3 {
		t.Fatalf("cache = (%#v, %t)", cache, ok)
	}
}

func TestClientReconcilerReactivatesACompatibleCacheOncePerProcess(t *testing.T) {
	directory := t.TempDir()
	desired := clientDesiredState(4, true)
	if err := WriteClientAppliedState(directory, ClientAppliedState{ClientDesiredConfiguration: desired, Revision: 4}); err != nil {
		t.Fatalf("WriteClientAppliedState() error = %v", err)
	}
	runtime := &clientFRPRuntimeStub{}
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: runtime})
	if err != nil {
		t.Fatalf("NewClientReconciler() error = %v", err)
	}
	if err := reconciler.Apply(context.Background(), desired); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if err := reconciler.Apply(context.Background(), desired); err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if got, want := strings.Join(runtime.calls, ","), "verify,stop,start"; got != want {
		t.Fatalf("runtime calls = %q, want %q", got, want)
	}
}

func clientDesiredState(revision int64, enabled bool) ClientDesiredConfiguration {
	port := int64(20000 + revision)
	return ClientDesiredConfiguration{
		AdvertisedFRPHost: "frp.example.test",
		AdvertisedFRPPort: 7000,
		InternalFRPToken:  "internal-token",
		Snapshot: tunnelruntime.TunnelSnapshot{
			ClientKey: "client-key",
			Revision:  revision,
			Tunnels: []tunnelruntime.TunnelDefinition{{
				ID: "tunnel-" + strconv.FormatInt(revision, 10), Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port,
				LocalHost: "127.0.0.1", LocalPort: 3000, Enabled: enabled,
			}},
		},
	}
}
