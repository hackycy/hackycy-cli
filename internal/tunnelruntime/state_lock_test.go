package tunnelruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStateDirectoryLockPublishesOwnerAndReleasesOnlyItsOwnLock(t *testing.T) {
	stateDirectory := t.TempDir()
	clock := &stateLockTestClock{now: time.Date(2026, time.August, 24, 12, 34, 56, 789_123_000, time.UTC)}
	dependencies := newStateLockTestDependencies(clock, func(pid int) (bool, error) { return pid == 41, nil })

	first, err := acquireStateLock(stateDirectory, stateLockForDirectory, 0, dependencies)
	if err != nil {
		t.Fatalf("acquire first state lock: %v", err)
	}
	owner, valid := readStateLockOwner(first.path)
	if !valid || owner.PID != 41 || owner.StateDirectory != stateDirectory || owner.StartedAt != "2026-08-24T12:34:56.789Z" {
		t.Fatalf("owner = %#v, valid = %t", owner, valid)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(first.path, stateLockOwnerFile))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("owner mode = (%v, %v), want 0600", info.Mode(), err)
		}
	}
	if _, err := acquireStateLock(stateDirectory, stateLockForDirectory, 0, dependencies); !errors.Is(err, ErrInstanceActive) {
		t.Fatalf("second state lock error = %v, want ErrInstanceActive", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release first state lock: %v", err)
	}
	if _, err := os.Stat(first.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock stat error = %v", err)
	}

	second, err := acquireStateLock(stateDirectory, stateLockForDirectory, 0, dependencies)
	if err != nil {
		t.Fatalf("acquire replacement state lock: %v", err)
	}
	replacement := stateLockOwner{ID: "replacement", PID: 41, StartedAt: owner.StartedAt, StateDirectory: stateDirectory}
	if err := os.Remove(filepath.Join(second.path, stateLockOwnerFile)); err != nil {
		t.Fatalf("remove original owner: %v", err)
	}
	if err := writeStateLockOwner(second.path, replacement); err != nil {
		t.Fatalf("write replacement owner: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("release replaced state lock: %v", err)
	}
	if current, valid := readStateLockOwner(second.path); !valid || current.ID != replacement.ID {
		t.Fatalf("replacement owner after release = %#v, valid = %t", current, valid)
	}
}

func TestStateLockWaitsForOwnerPublicationAndReclaimsStaleLocks(t *testing.T) {
	stateDirectory := t.TempDir()
	lockPath := filepath.Join(stateDirectory, stateLockName(stateLockForDirectory))
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("create incomplete lock: %v", err)
	}
	clock := &stateLockTestClock{now: time.Date(2026, time.August, 24, 13, 0, 0, 0, time.UTC)}
	if err := os.Chtimes(lockPath, clock.now, clock.now); err != nil {
		t.Fatalf("set incomplete lock mtime: %v", err)
	}
	dependencies := newStateLockTestDependencies(clock, func(int) (bool, error) { return false, nil })
	lock, err := acquireStateLock(stateDirectory, stateLockForDirectory, 0, dependencies)
	if err != nil {
		t.Fatalf("acquire after publication grace: %v", err)
	}
	if len(clock.sleeps) == 0 || clock.sleeps[0] != stateLockRetry {
		t.Fatalf("publication grace sleeps = %#v", clock.sleeps)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release lock after publication grace: %v", err)
	}

	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("create stale lock: %v", err)
	}
	if err := writeStateLockOwner(lockPath, stateLockOwner{ID: "stale", PID: 99, StartedAt: "2020-01-01T00:00:00.000Z", StateDirectory: stateDirectory}); err != nil {
		t.Fatalf("write stale owner: %v", err)
	}
	lock, err = acquireStateLock(stateDirectory, stateLockForDirectory, 0, dependencies)
	if err != nil {
		t.Fatalf("reclaim stale lock: %v", err)
	}
	owner, valid := readStateLockOwner(lock.path)
	if !valid || owner.ID == "stale" {
		t.Fatalf("reclaimed owner = %#v, valid = %t", owner, valid)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release reclaimed lock: %v", err)
	}
}

func TestStateRegistryLockTimesOutForALiveOwner(t *testing.T) {
	stateDirectory := t.TempDir()
	lockPath := filepath.Join(stateDirectory, stateLockName(stateLockForRegistry))
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("create registry lock: %v", err)
	}
	if err := writeStateLockOwner(lockPath, stateLockOwner{ID: "live", PID: 51, StartedAt: "2026-08-24T14:00:00.000Z", StateDirectory: stateDirectory}); err != nil {
		t.Fatalf("write registry owner: %v", err)
	}
	clock := &stateLockTestClock{now: time.Date(2026, time.August, 24, 14, 0, 0, 0, time.UTC)}
	dependencies := newStateLockTestDependencies(clock, func(pid int) (bool, error) { return pid == 51, nil })
	_, err := acquireStateLock(stateDirectory, stateLockForRegistry, time.Second, dependencies)
	if !errors.Is(err, ErrLockUnavailable) || !strings.Contains(err.Error(), "state registry") || len(clock.sleeps) == 0 {
		t.Fatalf("registry lock error = %v, sleeps = %#v", err, clock.sleeps)
	}
}

func TestStateDirectoryIsActiveHonorsLiveAndPublicationGraceLocks(t *testing.T) {
	stateDirectory := t.TempDir()
	lockPath := filepath.Join(stateDirectory, stateLockName(stateLockForDirectory))
	clock := &stateLockTestClock{now: time.Date(2026, time.August, 24, 15, 0, 0, 0, time.UTC)}
	dependencies := newStateLockTestDependencies(clock, func(pid int) (bool, error) { return pid == 71, nil })
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("create state lock: %v", err)
	}
	if err := writeStateLockOwner(lockPath, stateLockOwner{ID: "live", PID: 71, StartedAt: "2026-08-24T15:00:00.000Z", StateDirectory: stateDirectory}); err != nil {
		t.Fatalf("write live owner: %v", err)
	}
	if active, err := stateLockIsActive(lockPath, dependencies); err != nil || !active {
		t.Fatalf("live state lock active = (%t, %v)", active, err)
	}

	dependencies.processAlive = func(int) (bool, error) { return false, nil }
	if active, err := stateLockIsActive(lockPath, dependencies); err != nil || active {
		t.Fatalf("stale state lock active = (%t, %v)", active, err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale state lock stat error = %v", err)
	}

	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("recreate incomplete lock: %v", err)
	}
	if err := os.Chtimes(lockPath, clock.now, clock.now); err != nil {
		t.Fatalf("set incomplete lock mtime: %v", err)
	}
	if active, err := stateLockIsActive(lockPath, dependencies); err != nil || !active {
		t.Fatalf("recent incomplete state lock active = (%t, %v)", active, err)
	}
	clock.now = clock.now.Add(stateLockPublicationGrace)
	if active, err := stateLockIsActive(lockPath, dependencies); err != nil || active {
		t.Fatalf("expired incomplete state lock active = (%t, %v)", active, err)
	}
}

type stateLockTestClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (clock *stateLockTestClock) sleep(duration time.Duration) {
	clock.sleeps = append(clock.sleeps, duration)
	clock.now = clock.now.Add(duration)
}

func newStateLockTestDependencies(clock *stateLockTestClock, alive func(int) (bool, error)) stateLockDependencies {
	sequence := 0
	return stateLockDependencies{
		now:          func() time.Time { return clock.now },
		sleep:        clock.sleep,
		pid:          41,
		processAlive: alive,
		newID: func() (string, error) {
			sequence++
			return fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence), nil
		},
	}
}
