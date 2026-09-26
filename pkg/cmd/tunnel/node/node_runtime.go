package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

type nodeRuntime struct {
	state                *State
	mu                   sync.Mutex
	supervisor           *tunnelruntime.FRPSupervisor
	ensureBinary         func(context.Context) (string, error)
	verifyConfig         func(context.Context, string) error
	beforeCandidateStart func()
	beforeDisableStop    func()
	checkpoint           func(string)
	processInspector     tunnelruntime.ProcessInspector
	ownerUnknown         bool
	recoveryError        string
	activeConfig         string
}

func newNodeRuntime(state *State) *nodeRuntime {
	return &nodeRuntime{state: state, processInspector: tunnelruntime.DefaultProcessInspector(), ensureBinary: func(ctx context.Context) (string, error) {
		artifact, err := tunnelruntime.CurrentFRPArtifact()
		if err != nil {
			return "", err
		}
		directory, err := tunnelruntime.DefaultFRPRuntimeDirectory()
		if err != nil {
			return "", err
		}
		paths, err := tunnelruntime.EnsureFRPRuntimeAt(ctx, directory, artifact)
		if err != nil {
			return "", err
		}
		return paths.FRPS, nil
	}}
}

func (runtime *nodeRuntime) prepare(ctx context.Context) error {
	if runtime.supervisor != nil {
		return nil
	}
	binary, err := runtime.ensureBinary(ctx)
	if err != nil {
		return err
	}
	supervisor, err := tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{BinaryPath: binary, Role: tunnelruntime.FRPRoleServer})
	if err != nil {
		return err
	}
	runtime.supervisor = supervisor
	supervisor.Observe(func(state tunnelruntime.FRPSupervisorState) {
		if state.State == tunnelruntime.FRPProcessRunning && state.PID != nil {
			go runtime.trackRecovery(*state.PID)
		}
	})
	return nil
}

func (runtime *nodeRuntime) trackRecovery(pid int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.activeConfig == "" || runtime.supervisor == nil {
		return
	}
	state := runtime.supervisor.State()
	if state.PID == nil || *state.PID != pid {
		return
	}
	if err := runtime.rememberOwner(context.Background(), runtime.activeConfig); err != nil {
		if errors.Is(err, tunnelruntime.ErrProcessNotFound) {
			return
		}
		runtime.ownerUnknown = true
	}
}

func (runtime *nodeRuntime) processState() tunnelruntime.FRPSupervisorState {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.ownerUnknown {
		return tunnelruntime.FRPSupervisorState{State: "unknown"}
	}
	if runtime.supervisor == nil {
		return tunnelruntime.FRPSupervisorState{State: tunnelruntime.FRPProcessStopped}
	}
	return runtime.supervisor.State()
}

func (runtime *nodeRuntime) recoveryCode() string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.ownerUnknown {
		return "FRPS_OWNERSHIP_UNKNOWN"
	}
	return runtime.recoveryError
}

func (runtime *nodeRuntime) applyAccepted(ctx context.Context) (runtimeRecord, string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	record, err := runtime.state.readRuntime(ctx)
	if err != nil {
		return runtimeRecord{}, "UNAVAILABLE"
	}
	if runtime.ownerUnknown {
		return record, "FRPS_OWNERSHIP_UNKNOWN"
	}
	if record.Phase != "accepted" && record.Phase != "interrupted" {
		return record, record.FailureCode
	}
	snapshot, err := decodeDesiredSnapshot(record.Candidate, runtime.state.nodeID)
	if err != nil {
		return record, "NODE_SNAPSHOT_INVALID"
	}
	if snapshot.State == "disabled" {
		return runtime.applyDisabled(ctx, record)
	}
	if err := runtime.prepare(ctx); err != nil {
		return runtime.fail(ctx, record, "NODE_CONFIG_REJECTED")
	}
	path, err := runtime.render(snapshot, record.HighestDigest)
	if err != nil {
		return runtime.fail(ctx, record, "NODE_CONFIG_REJECTED")
	}
	verify := runtime.verifyConfig
	if verify == nil {
		verify = runtime.verify
	}
	if err := verify(ctx, path); err != nil {
		return runtime.fail(ctx, record, "NODE_CONFIG_REJECTED")
	}
	if err := runtime.setPhase(ctx, "switching", ""); err != nil {
		return record, "UNAVAILABLE"
	}
	runtime.mark("switching")
	if err := runtime.supervisor.Stop(); err != nil {
		return runtime.rollback(ctx, record, "NODE_APPLY_FAILED")
	}
	if err := runtime.clearOwner(ctx); err != nil {
		return runtime.rollback(ctx, record, "NODE_APPLY_FAILED")
	}
	runtime.activeConfig = ""
	runtime.mark("old-stopped")
	if runtime.beforeCandidateStart != nil {
		runtime.beforeCandidateStart()
	}
	runtime.activeConfig = path
	if err := runtime.supervisor.Start(path); err != nil {
		return runtime.rollback(ctx, record, "NODE_APPLY_FAILED")
	}
	if err := runtime.rememberOwner(ctx, path); err != nil {
		return runtime.rollback(ctx, record, "NODE_APPLY_FAILED")
	}
	runtime.mark("candidate-started")
	if err := markNodeV1RunningApplied(ctx, runtime.state.client); err != nil {
		return runtime.rollback(ctx, record, "NODE_APPLY_FAILED")
	}
	runtime.mark("applied")
	result, err := runtime.state.readRuntime(ctx)
	if err != nil {
		return runtimeRecord{}, "UNAVAILABLE"
	}
	runtime.recoveryError = ""
	return result, ""
}

func (runtime *nodeRuntime) applyDisabled(ctx context.Context, record runtimeRecord) (runtimeRecord, string) {
	if err := markNodeV1Disabling(ctx, runtime.state.client); err != nil {
		return record, "UNAVAILABLE"
	}
	runtime.mark("disabling")
	if runtime.beforeDisableStop != nil {
		runtime.beforeDisableStop()
	}
	if runtime.supervisor != nil {
		if err := runtime.supervisor.Stop(); err != nil {
			return runtime.fail(ctx, record, "NODE_APPLY_FAILED")
		}
		if err := runtime.clearOwner(ctx); err != nil {
			return record, "UNAVAILABLE"
		}
		runtime.activeConfig = ""
	}
	runtime.mark("disabled-stopped")
	if err := runtime.removeRuntimeFiles(); err != nil {
		return runtime.fail(ctx, record, "NODE_APPLY_FAILED")
	}
	if err := markNodeV1Disabled(ctx, runtime.state.client); err != nil {
		return record, "UNAVAILABLE"
	}
	runtime.mark("disabled-complete")
	result, err := runtime.state.readRuntime(ctx)
	if err != nil {
		return runtimeRecord{}, "UNAVAILABLE"
	}
	runtime.recoveryError = ""
	return result, ""
}

func (runtime *nodeRuntime) removeRuntimeFiles() error {
	entries, err := os.ReadDir(runtime.state.directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "frps-") && (strings.HasSuffix(entry.Name(), ".toml") || strings.HasSuffix(entry.Name(), "-404.html")) {
			if err := os.Remove(filepath.Join(runtime.state.directory, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (runtime *nodeRuntime) removeAbandonedTransfers() error {
	entries, err := os.ReadDir(runtime.state.directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && (strings.HasPrefix(entry.Name(), "snapshot-") || strings.HasPrefix(entry.Name(), ".frps-file-")) {
			if err := os.Remove(filepath.Join(runtime.state.directory, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (runtime *nodeRuntime) fail(ctx context.Context, record runtimeRecord, code string) (runtimeRecord, string) {
	if err := runtime.setPhase(ctx, "failed", code); err != nil {
		return record, "UNAVAILABLE"
	}
	result, err := runtime.state.readRuntime(ctx)
	if err != nil {
		return record, "UNAVAILABLE"
	}
	return result, code
}

func (runtime *nodeRuntime) rollback(ctx context.Context, record runtimeRecord, code string) (runtimeRecord, string) {
	if runtime.supervisor != nil {
		if err := runtime.supervisor.Stop(); err != nil {
			code = "NODE_ROLLBACK_FAILED"
		}
		if err := runtime.clearOwner(ctx); err != nil {
			return record, "UNAVAILABLE"
		}
		runtime.activeConfig = ""
	}
	if len(record.LastGood) != 0 && !record.BootDisabled && code != "NODE_ROLLBACK_FAILED" {
		old, err := decodeDesiredSnapshot(record.LastGood, runtime.state.nodeID)
		if err == nil {
			oldDigest := snapshotDigest(record.LastGood)
			var path string
			path, err = runtime.render(old, oldDigest)
			if err == nil {
				runtime.activeConfig = path
				err = runtime.supervisor.Start(path)
				if err == nil {
					err = runtime.rememberOwner(ctx, path)
				}
			}
		}
		if err != nil {
			code = "NODE_ROLLBACK_FAILED"
		}
	}
	return runtime.fail(ctx, record, code)
}

func (runtime *nodeRuntime) setPhase(ctx context.Context, phase, failure string) error {
	return setNodeV1Phase(ctx, runtime.state.client, phase, failure)
}

func (runtime *nodeRuntime) render(snapshot desiredSnapshot, digest string) (string, error) {
	page := ""
	if snapshot.Custom404Page != "" {
		page = filepath.Join(runtime.state.directory, "frps-"+digest+"-404.html")
		if err := writeNodeRuntimeFile(page, snapshot.Custom404Page); err != nil {
			return "", err
		}
	}
	contents, err := tunnelruntime.RenderFRPSConfig(tunnelruntime.FRPServerConfiguration{BindAddress: snapshot.BindAddress, BindPort: snapshot.BindPort, VhostHTTPPort: snapshot.VhostHTTPPort, Custom404Page: page, InternalFRPToken: snapshot.Token, PortRangeStart: snapshot.PortRangeStart, PortRangeEnd: snapshot.PortRangeEnd})
	if err != nil {
		return "", err
	}
	path := filepath.Join(runtime.state.directory, "frps-"+digest+".toml")
	return path, writeNodeRuntimeFile(path, contents)
}

func writeNodeRuntimeFile(target, contents string) error {
	file, err := os.CreateTemp(filepath.Dir(target), ".frps-file-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := tunnelruntime.ProtectPrivateFile(file.Name(), 0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.WriteString(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}

func (runtime *nodeRuntime) verify(ctx context.Context, path string) error {
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := exec.CommandContext(check, runtime.supervisor.BinaryPath(), "verify", "-c", path).CombinedOutput()
	if errors.Is(check.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("FRPS verify timed out")
	}
	return err
}

func snapshotDigest(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func (runtime *nodeRuntime) mark(phase string) {
	if runtime.checkpoint != nil {
		runtime.checkpoint(phase)
	}
}

func (runtime *nodeRuntime) rememberOwner(ctx context.Context, config string) error {
	state := runtime.supervisor.State()
	if state.PID == nil {
		return fmt.Errorf("FRPS activation has no process")
	}
	binary := runtime.supervisor.BinaryPath()
	owner, err := runtime.captureOwner(*state.PID, binary, config)
	if err != nil {
		return err
	}
	return setNodeV1Owner(ctx, runtime.state.client, owner.PID, owner.CreateTimeUnixMs, owner.BinaryPath, owner.ConfigPath)
}

func (runtime *nodeRuntime) captureOwner(pid int, binary, config string) (tunnelruntime.ProcessOwner, error) {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for {
		owner, err := tunnelruntime.CaptureFRPSOwner(runtime.processInspector, pid, binary, config)
		if err == nil {
			return owner, nil
		}
		lastErr = err
		if errors.Is(err, tunnelruntime.ErrProcessNotFound) || time.Now().After(deadline) {
			return tunnelruntime.ProcessOwner{}, lastErr
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (runtime *nodeRuntime) clearOwner(ctx context.Context) error {
	return clearNodeV1Owner(ctx, runtime.state.client)
}

func (runtime *nodeRuntime) recover(ctx context.Context) string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if err := runtime.removeAbandonedTransfers(); err != nil {
		return "UNAVAILABLE"
	}
	record, err := runtime.state.readRuntime(ctx)
	if err != nil {
		return "UNAVAILABLE"
	}
	bindings, err := runtime.state.client.ControllerBinding.Query().Count(ctx)
	if err != nil {
		return "UNAVAILABLE"
	}
	if bindings == 0 {
		if record.HighestRevision != 0 || record.AppliedRevision != 0 {
			return "UNAVAILABLE"
		}
		return ""
	}
	if record.OwnerPID != 0 {
		owner := tunnelruntime.ProcessOwner{PID: record.OwnerPID, CreateTimeUnixMs: record.OwnerCreateTime, BinaryPath: record.OwnerBinary, ConfigPath: record.OwnerConfig}
		if _, err := tunnelruntime.VerifyFRPSOwner(runtime.processInspector, owner); err != nil {
			if !errors.Is(err, tunnelruntime.ErrProcessNotFound) {
				runtime.ownerUnknown = true
				return "FRPS_OWNERSHIP_UNKNOWN"
			}
		} else if err := tunnelruntime.TerminateOwnedFRPS(owner); err != nil {
			runtime.ownerUnknown = true
			return "FRPS_OWNERSHIP_UNKNOWN"
		}
		if err := runtime.clearOwner(ctx); err != nil {
			return "UNAVAILABLE"
		}
	}
	if record.BootDisabled {
		if !record.DisabledComplete {
			candidate, err := decodeDesiredSnapshot(record.Candidate, runtime.state.nodeID)
			if err != nil {
				return "NODE_SNAPSHOT_INVALID"
			}
			if candidate.State == "running" {
				if err := runtime.setPhase(ctx, "interrupted", ""); err != nil {
					return "UNAVAILABLE"
				}
				return ""
			}
		}
		if err := runtime.removeRuntimeFiles(); err != nil {
			return "NODE_APPLY_FAILED"
		}
		if !record.DisabledComplete {
			if err := markNodeV1Disabled(ctx, runtime.state.client); err != nil {
				return "UNAVAILABLE"
			}
		}
		return ""
	}
	if record.Phase == "switching" || record.Phase == "accepted" {
		if err := runtime.setPhase(ctx, "interrupted", ""); err != nil {
			return "UNAVAILABLE"
		}
	}
	if len(record.LastGood) == 0 {
		return ""
	}
	if err := runtime.prepare(ctx); err != nil {
		return "NODE_APPLY_FAILED"
	}
	lastGood, err := decodeDesiredSnapshot(record.LastGood, runtime.state.nodeID)
	if err != nil {
		return "NODE_SNAPSHOT_INVALID"
	}
	path, err := runtime.render(lastGood, snapshotDigest(record.LastGood))
	if err != nil {
		return "NODE_APPLY_FAILED"
	}
	if err := runtime.verify(ctx, path); err != nil {
		return "NODE_CONFIG_REJECTED"
	}
	runtime.activeConfig = path
	if err := runtime.supervisor.Start(path); err != nil {
		return "NODE_APPLY_FAILED"
	}
	if err := runtime.rememberOwner(ctx, path); err != nil {
		_ = runtime.supervisor.Stop()
		return "UNAVAILABLE"
	}
	return ""
}
