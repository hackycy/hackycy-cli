package appconfig

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type configLockOwner struct {
	ID        string `json:"id"`
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
}

type configLock struct {
	path  string
	owner configLockOwner
}

func (store *Store) acquireConfigLock() (*configLock, error) {
	if err := os.MkdirAll(store.configDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create configuration directory: %w", err)
	}
	ownerID, err := store.newLockID()
	if err != nil {
		return nil, fmt.Errorf("create configuration lock owner ID: %w", err)
	}
	owner := configLockOwner{
		ID:        ownerID,
		PID:       store.pid,
		StartedAt: store.now().UTC().Format(time.RFC3339Nano),
	}
	deadline := store.now().Add(store.lockTimeout)

	for !store.now().After(deadline) {
		err := os.Mkdir(store.lockPath(), 0o700)
		if err == nil {
			if err := writeConfigLockOwner(store.lockPath(), owner); err != nil {
				_ = os.RemoveAll(store.lockPath())
				return nil, fmt.Errorf("write configuration lock owner: %w", err)
			}
			return &configLock{path: store.lockPath(), owner: owner}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create configuration lock: %w", err)
		}

		current, hasOwner := readConfigLockOwner(store.lockPath())
		if !hasOwner {
			if store.hasOwnerPublicationGrace() {
				store.sleep(store.lockRetry)
				continue
			}
			if err := store.removeStaleConfigLock(); err != nil {
				return nil, err
			}
			continue
		}
		alive, err := store.processAlive(current.PID)
		if err != nil {
			return nil, fmt.Errorf("inspect configuration lock owner: %w", err)
		}
		if !alive {
			if err := store.removeStaleConfigLock(); err != nil {
				return nil, err
			}
			continue
		}
		store.sleep(store.lockRetry)
	}

	return nil, fmt.Errorf("Could not lock ycy configuration within %s", formatLockTimeout(store.lockTimeout))
}

func (store *Store) hasOwnerPublicationGrace() bool {
	info, err := os.Stat(store.lockPath())
	if err != nil {
		return false
	}
	return store.now().Sub(info.ModTime()) < store.lockGrace
}

func (store *Store) removeStaleConfigLock() error {
	id, err := store.newLockID()
	if err != nil {
		return fmt.Errorf("create stale configuration lock ID: %w", err)
	}
	stalePath := store.lockPath() + ".stale-" + id
	if err := os.Rename(store.lockPath(), stalePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("rename stale configuration lock: %w", err)
	}
	if err := os.RemoveAll(stalePath); err != nil {
		return fmt.Errorf("remove stale configuration lock: %w", err)
	}
	return nil
}

func (lock *configLock) release() error {
	current, hasOwner := readConfigLockOwner(lock.path)
	if !hasOwner || current.ID != lock.owner.ID {
		return nil
	}
	if err := os.RemoveAll(lock.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release configuration lock: %w", err)
	}
	return nil
}

func writeConfigLockOwner(lockPath string, owner configLockOwner) error {
	contents, err := json.MarshalIndent(owner, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	file, err := os.OpenFile(filepath.Join(lockPath, "owner.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(contents)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func readConfigLockOwner(lockPath string) (configLockOwner, bool) {
	contents, err := os.ReadFile(filepath.Join(lockPath, "owner.json"))
	if err != nil {
		return configLockOwner{}, false
	}
	var owner configLockOwner
	if err := json.Unmarshal(contents, &owner); err != nil || owner.ID == "" || owner.StartedAt == "" {
		return configLockOwner{}, false
	}
	return owner, true
}

func randomUUID(random io.Reader) (string, error) {
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(random, bytes); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func formatLockTimeout(timeout time.Duration) string {
	if timeout%time.Second == 0 {
		return fmt.Sprintf("%d seconds", timeout/time.Second)
	}
	return timeout.String()
}
