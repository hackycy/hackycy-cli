package tunnelruntime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	stateLockPublicationGrace = time.Second
	stateLockRetry            = 25 * time.Millisecond
	stateLockOwnerFile        = "owner.json"
)

var (
	ErrInstanceActive  = errors.New("Tunnel instance is already active")
	ErrLockUnavailable = errors.New("Tunnel lock is unavailable")
)

type stateLockOwner struct {
	ID             string `json:"id"`
	PID            int    `json:"pid"`
	StartedAt      string `json:"startedAt"`
	StateDirectory string `json:"stateDirectory"`
}

// StateDirectoryLock reserves one Tunnel state directory for its owner's
// lifetime.
type StateDirectoryLock struct {
	path  string
	owner stateLockOwner
}

type stateLockKind uint8

const (
	stateLockForDirectory stateLockKind = iota
	stateLockForRegistry
)

type stateLockDependencies struct {
	now          func() time.Time
	sleep        func(time.Duration)
	pid          int
	processAlive func(int) (bool, error)
	newID        func() (string, error)
}

func defaultStateLockDependencies() stateLockDependencies {
	return stateLockDependencies{
		now:          time.Now,
		sleep:        time.Sleep,
		pid:          os.Getpid(),
		processAlive: nativeStateLockProcessAlive,
		newID:        newStateLockID,
	}
}

// AcquireStateDirectoryLock reserves one server or client instance directory
// for the caller's foreground lifetime.
func AcquireStateDirectoryLock(stateDirectory string) (*StateDirectoryLock, error) {
	return acquireStateLock(stateDirectory, stateLockForDirectory, 0, defaultStateLockDependencies())
}

// AcquireStateRegistryLock serializes brief client-instance creation and
// cleanup work beneath one state root.
func AcquireStateRegistryLock(stateRoot string) (*StateDirectoryLock, error) {
	return acquireStateLock(stateRoot, stateLockForRegistry, 10*time.Second, defaultStateLockDependencies())
}

func acquireStateLock(stateDirectory string, kind stateLockKind, wait time.Duration, dependencies stateLockDependencies) (*StateDirectoryLock, error) {
	if strings.TrimSpace(stateDirectory) == "" {
		return nil, fmt.Errorf("%w: state directory is required", ErrLockUnavailable)
	}
	if wait < 0 {
		return nil, fmt.Errorf("%w: lock wait must not be negative", ErrLockUnavailable)
	}
	dependencies = normalizeStateLockDependencies(dependencies)
	if err := os.MkdirAll(stateDirectory, 0o777); err != nil {
		return nil, fmt.Errorf("%w: create Tunnel state directory: %v", ErrLockUnavailable, err)
	}

	lockPath := filepath.Join(stateDirectory, stateLockName(kind))
	ownerID, err := dependencies.newID()
	if err != nil {
		return nil, fmt.Errorf("%w: create Tunnel lock owner ID: %v", ErrLockUnavailable, err)
	}
	owner := stateLockOwner{
		ID:             ownerID,
		PID:            dependencies.pid,
		StartedAt:      formatStateLockTimestamp(dependencies.now()),
		StateDirectory: stateDirectory,
	}
	deadline := dependencies.now().Add(max(wait, stateLockPublicationGrace))

	for !dependencies.now().After(deadline) {
		if err := os.Mkdir(lockPath, 0o777); err == nil {
			if err := writeStateLockOwner(lockPath, owner); err != nil {
				_ = os.RemoveAll(lockPath)
				return nil, fmt.Errorf("%w: write Tunnel lock owner: %v", ErrLockUnavailable, err)
			}
			return &StateDirectoryLock{path: lockPath, owner: owner}, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: create Tunnel state lock: %v", ErrLockUnavailable, err)
		}

		current, hasOwner := readStateLockOwner(lockPath)
		if hasOwner {
			alive, err := dependencies.processAlive(current.PID)
			if err != nil {
				return nil, fmt.Errorf("%w: inspect Tunnel lock owner: %v", ErrLockUnavailable, err)
			}
			if alive {
				if wait > 0 && dependencies.now().Before(deadline) {
					dependencies.sleep(stateLockRetry)
					continue
				}
				return nil, activeStateLockError(kind, current, stateDirectory, wait)
			}
		} else if stateLockRecentlyCreated(lockPath, dependencies.now()) {
			dependencies.sleep(stateLockRetry)
			continue
		}
		if err := removeStaleStateLock(lockPath, dependencies.newID); err != nil {
			return nil, fmt.Errorf("%w: remove stale Tunnel lock: %v", ErrLockUnavailable, err)
		}
	}

	return nil, fmt.Errorf("%w: could not acquire Tunnel state lock %s", ErrLockUnavailable, lockPath)
}

// StateDirectoryIsActive reports whether a state directory has a live or
// still-publishing foreground owner. It reclaims stale lock directories.
func StateDirectoryIsActive(stateDirectory string) (bool, error) {
	return stateLockIsActive(filepath.Join(stateDirectory, stateLockName(stateLockForDirectory)), defaultStateLockDependencies())
}

func stateLockIsActive(lockPath string, dependencies stateLockDependencies) (bool, error) {
	dependencies = normalizeStateLockDependencies(dependencies)
	owner, hasOwner := readStateLockOwner(lockPath)
	if hasOwner {
		alive, err := dependencies.processAlive(owner.PID)
		if err != nil {
			return false, fmt.Errorf("inspect Tunnel lock owner: %w", err)
		}
		if alive {
			return true, nil
		}
	} else if stateLockRecentlyCreated(lockPath, dependencies.now()) {
		return true, nil
	}
	if err := removeStaleStateLock(lockPath, dependencies.newID); err != nil {
		return false, fmt.Errorf("remove stale Tunnel lock: %w", err)
	}
	return false, nil
}

// Release relinquishes the lock only if it is still owned by this handle.
func (lock *StateDirectoryLock) Release() error {
	if lock == nil {
		return nil
	}
	current, hasOwner := readStateLockOwner(lock.path)
	if !hasOwner || current.ID != lock.owner.ID {
		return nil
	}
	if err := os.RemoveAll(lock.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release Tunnel state lock: %w", err)
	}
	return nil
}

func normalizeStateLockDependencies(dependencies stateLockDependencies) stateLockDependencies {
	defaults := defaultStateLockDependencies()
	if dependencies.now == nil {
		dependencies.now = defaults.now
	}
	if dependencies.sleep == nil {
		dependencies.sleep = defaults.sleep
	}
	if dependencies.pid <= 0 {
		dependencies.pid = defaults.pid
	}
	if dependencies.processAlive == nil {
		dependencies.processAlive = defaults.processAlive
	}
	if dependencies.newID == nil {
		dependencies.newID = defaults.newID
	}
	return dependencies
}

func stateLockName(kind stateLockKind) string {
	if kind == stateLockForRegistry {
		return ".instances.lock"
	}
	return ".lock"
}

func activeStateLockError(kind stateLockKind, owner stateLockOwner, stateDirectory string, wait time.Duration) error {
	if kind == stateLockForDirectory {
		return fmt.Errorf("%w: Tunnel supervisor process %d already owns state directory %s", ErrInstanceActive, owner.PID, stateDirectory)
	}
	return fmt.Errorf("%w: could not acquire Tunnel state registry %s within %s", ErrLockUnavailable, stateDirectory, formatStateLockTimeout(wait))
}

func stateLockRecentlyCreated(lockPath string, now time.Time) bool {
	info, err := os.Stat(lockPath)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) < stateLockPublicationGrace
}

func removeStaleStateLock(lockPath string, newID func() (string, error)) error {
	id, err := newID()
	if err != nil {
		return err
	}
	stalePath := lockPath + ".stale-" + id
	if err := os.Rename(lockPath, stalePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.RemoveAll(stalePath)
}

func writeStateLockOwner(lockPath string, owner stateLockOwner) error {
	contents, err := json.MarshalIndent(owner, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	file, err := os.OpenFile(filepath.Join(lockPath, stateLockOwnerFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(contents)
	if writeErr == nil && written != len(contents) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Close()
	} else {
		_ = file.Close()
	}
	return writeErr
}

func readStateLockOwner(lockPath string) (stateLockOwner, bool) {
	contents, err := os.ReadFile(filepath.Join(lockPath, stateLockOwnerFile))
	if err != nil {
		return stateLockOwner{}, false
	}
	var owner stateLockOwner
	if err := json.Unmarshal(contents, &owner); err != nil || owner.ID == "" || owner.PID <= 0 || owner.StartedAt == "" || owner.StateDirectory == "" {
		return stateLockOwner{}, false
	}
	return owner, true
}

func newStateLockID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func formatStateLockTimestamp(timestamp time.Time) string {
	return timestamp.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z07:00")
}

func formatStateLockTimeout(timeout time.Duration) string {
	if timeout%time.Second == 0 {
		return fmt.Sprintf("%d seconds", timeout/time.Second)
	}
	return timeout.String()
}
