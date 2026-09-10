package connect

import (
	"encoding/json"
	"errors"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	clientInstanceExpiry       = 90 * 24 * time.Hour
	clientMaximumSafeInteger   = int64(9007199254740991)
	clientAppliedStateFilename = "last-applied.json"
	clientFRPCConfigFilename   = "frpc.toml"
)

var clientInstanceDirectoryPattern = regexp.MustCompile(`^v1_[A-Za-z0-9_-]{43}$`)

// ClientInstanceIdentity derives the opaque stable state-directory name from
// the remembered-configuration key without exposing that key to this package.
type ClientInstanceIdentity interface {
	TunnelInstanceID(*url.URL, string) (string, error)
}

// ClientInstanceOptions supplies the small amount of process state used while
// taking ownership of one foreground client instance.
type ClientInstanceOptions struct {
	StateRoot      string
	Now            func() time.Time
	OnCleanupError func(error)
}

// ClientInstance owns a Go-created per-connection state directory lock.
type ClientInstance struct {
	ID             string
	StateDirectory string
	lock           *tunnelruntime.StateDirectoryLock
}

// ClientDesiredConfiguration is the complete FRPC configuration received from
// an authenticated protocol-v3 welcome or desired-state frame.
type ClientDesiredConfiguration struct {
	AdvertisedFRPHost string                       `json:"advertisedFrpHost"`
	AdvertisedFRPPort int64                        `json:"advertisedFrpPort"`
	InternalFRPToken  string                       `json:"internalFrpToken"`
	Snapshot          tunnelruntime.TunnelSnapshot `json:"snapshot"`
}

// ClientAppliedState is the rollback cache. It is never authorization state.
type ClientAppliedState struct {
	ClientDesiredConfiguration
	Revision int64 `json:"revision"`
}

// AcquireClientInstance derives the stable Go-created instance directory,
// takes its exclusive foreground lock, and cleans only safe expired siblings.
func AcquireClientInstance(config ClientConfig, identity ClientInstanceIdentity, options ClientInstanceOptions) (*ClientInstance, error) {
	if config.Server == nil || strings.TrimSpace(config.Token) == "" {
		return nil, fmt.Errorf("Tunnel client server and token are required")
	}
	if identity == nil {
		return nil, fmt.Errorf("Tunnel client instance identity is required")
	}
	stateRoot := options.StateRoot
	if stateRoot == "" {
		var err error
		stateRoot, err = defaultClientStateRoot()
		if err != nil {
			return nil, err
		}
	}
	stateRoot, err := filepath.Abs(stateRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve Tunnel client state root: %w", err)
	}
	instanceID, err := identity.TunnelInstanceID(config.Server, config.Token)
	if err != nil {
		return nil, fmt.Errorf("derive Tunnel client instance identity: %w", err)
	}
	if !clientInstanceDirectoryPattern.MatchString(instanceID) {
		return nil, fmt.Errorf("Tunnel client instance identity is invalid")
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	stateDirectory := filepath.Join(stateRoot, instanceID)
	lock, cleanupErr, err := acquireClientInstanceState(stateDirectory, now())
	if err != nil {
		return nil, err
	}
	if cleanupErr != nil && options.OnCleanupError != nil {
		options.OnCleanupError(cleanupErr)
	}
	return &ClientInstance{ID: instanceID, StateDirectory: stateDirectory, lock: lock}, nil
}

// Release relinquishes the foreground instance lock. Its cache and generated
// configuration remain available only as unauthorizing rollback material.
func (instance *ClientInstance) Release() error {
	if instance == nil {
		return nil
	}
	return instance.lock.Release()
}

func defaultClientStateRoot() (string, error) {
	return clientStateRoot(os.Getenv, os.UserHomeDir, runtime.GOOS)
}

func clientStateRoot(environment func(string) string, userHomeDirectory func() (string, error), platform string) (string, error) {
	stateRoot, err := tunnelruntime.StateRoot(environment, userHomeDirectory, platform)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateRoot, "ycy", "tunnel", "client"), nil
}

func acquireClientInstanceState(stateDirectory string, now time.Time) (*tunnelruntime.StateDirectoryLock, error, error) {
	if !clientInstanceDirectoryPattern.MatchString(filepath.Base(stateDirectory)) {
		lock, err := tunnelruntime.AcquireStateDirectoryLock(stateDirectory)
		return lock, nil, err
	}
	stateRoot := filepath.Dir(stateDirectory)
	registry, err := tunnelruntime.AcquireStateRegistryLock(stateRoot)
	if err != nil {
		return nil, nil, err
	}
	instance, err := tunnelruntime.AcquireStateDirectoryLock(stateDirectory)
	if err != nil {
		_ = registry.Release()
		return nil, nil, err
	}
	cleanupErr := cleanupExpiredClientInstances(stateRoot, stateDirectory, now)
	if releaseErr := registry.Release(); releaseErr != nil {
		_ = instance.Release()
		return nil, cleanupErr, releaseErr
	}
	return instance, cleanupErr, nil
}

func cleanupExpiredClientInstances(stateRoot, currentStateDirectory string, now time.Time) error {
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		return fmt.Errorf("inspect Tunnel client state root: %w", err)
	}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || !clientInstanceDirectoryPattern.MatchString(entry.Name()) {
			continue
		}
		stateDirectory := filepath.Join(stateRoot, entry.Name())
		if stateDirectory == currentStateDirectory {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			failures = append(failures, fmt.Errorf("inspect tunnel state directory %s: %w", stateDirectory, statErr))
			continue
		}
		if now.Sub(info.ModTime()) < clientInstanceExpiry {
			continue
		}
		active, activeErr := tunnelruntime.StateDirectoryIsActive(stateDirectory)
		if activeErr != nil {
			failures = append(failures, fmt.Errorf("inspect tunnel state lock %s: %w", stateDirectory, activeErr))
			continue
		}
		if active {
			continue
		}
		if removeErr := os.RemoveAll(stateDirectory); removeErr != nil {
			failures = append(failures, fmt.Errorf("remove expired tunnel state directory %s: %w", stateDirectory, removeErr))
		}
	}
	return errors.Join(failures...)
}

func clientAppliedStatePath(stateDirectory string) string {
	return filepath.Join(stateDirectory, clientAppliedStateFilename)
}

func clientActiveFRPCConfigPath(stateDirectory string) string {
	return filepath.Join(stateDirectory, clientFRPCConfigFilename)
}

// ReadClientAppliedState treats missing, interrupted, malformed, and stale
// cache files as absent so they cannot authorize a cold client activation.
func ReadClientAppliedState(stateDirectory string) (*ClientAppliedState, bool) {
	contents, err := os.ReadFile(clientAppliedStatePath(stateDirectory))
	if err != nil {
		return nil, false
	}
	var state ClientAppliedState
	if err := json.Unmarshal(contents, &state); err != nil || !validClientAppliedState(state) {
		return nil, false
	}
	return &state, true
}

// WriteClientAppliedState atomically publishes one complete successful cache.
func WriteClientAppliedState(stateDirectory string, state ClientAppliedState) error {
	if !validClientAppliedState(state) {
		return fmt.Errorf("Tunnel client applied state is invalid")
	}
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Tunnel client applied state: %w", err)
	}
	contents = append(contents, '\n')
	return writeClientFileAtomically(clientAppliedStatePath(stateDirectory), contents)
}

func validClientAppliedState(state ClientAppliedState) bool {
	return state.Revision >= 0 && state.Revision <= clientMaximumSafeInteger && state.Snapshot.Revision == state.Revision
}

func writeClientFileAtomically(path string, contents []byte) (result error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o777); err != nil {
		return fmt.Errorf("create Tunnel client state directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create Tunnel client temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		if result != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := tunnelruntime.ProtectPrivateFile(temporaryPath, 0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect Tunnel client file: %w", err)
	}
	written, writeErr := temporary.Write(contents)
	if writeErr == nil && written != len(contents) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	if closeErr := temporary.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("write Tunnel client file: %w", writeErr)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish Tunnel client file: %w", err)
	}
	return nil
}
