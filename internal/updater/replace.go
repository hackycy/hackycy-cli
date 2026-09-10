package updater

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	fileRetryCount    = 100
	fileRetryInterval = 50 * time.Millisecond
)

// ReplacementOptions isolates process, filesystem, and native quarantine effects.
type ReplacementOptions struct {
	VerifyBinary      func(context.Context, string, string) error
	ClearQuarantine   func(string) error
	Chmod             func(string, os.FileMode) error
	Remove            func(string) error
	Rename            func(string, string) error
	Sleep             func(time.Duration) error
	RetryCount        int
	ProcessAlive      func(int) bool
	ParentSleep       func(time.Duration) error
	CurrentExecutable func() (string, error)
}

// ApplyTransaction publishes a verified candidate and restores the prior target on failure.
func ApplyTransaction(ctx context.Context, state UpdateTransaction, options ReplacementOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	options = normalizeReplacementOptions(options)
	if err := validateState(state); err != nil {
		return "", err
	}
	if _, err := os.Stat(state.StagedPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("downloaded update file is missing")
		}
		return "", fmt.Errorf("inspect staged candidate: %w", err)
	}
	if _, err := os.Stat(state.BackupPath); err == nil {
		return "", errors.New("a previous update backup is still present")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect update backup: %w", err)
	}

	originalMoved := false
	rollback := func(cause error) error {
		var rollbackErr error
		if !originalMoved {
			if _, statErr := os.Stat(state.TargetPath); statErr == nil {
				rollbackErr = retryOperation(options, func() error {
					err := options.Remove(state.TargetPath)
					if errors.Is(err, os.ErrNotExist) {
						return nil
					}
					return err
				})
			} else if !errors.Is(statErr, os.ErrNotExist) {
				rollbackErr = fmt.Errorf("inspect target for rollback: %w", statErr)
			}
			if rollbackErr != nil {
				return fmt.Errorf("%w; rollback failed: %v", cause, rollbackErr)
			}
			return cause
		}
		if _, statErr := os.Stat(state.TargetPath); statErr == nil {
			rollbackErr = retryOperation(options, func() error {
				err := options.Remove(state.TargetPath)
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			})
		} else if !errors.Is(statErr, os.ErrNotExist) {
			rollbackErr = fmt.Errorf("inspect target for rollback: %w", statErr)
		}
		if rollbackErr == nil {
			rollbackErr = retryOperation(options, func() error { return options.Rename(state.BackupPath, state.TargetPath) })
		}
		if rollbackErr == nil {
			rollbackErr = protectUpgradePath(state.TargetPath, 0o755, options.Chmod)
		}
		if rollbackErr != nil {
			return fmt.Errorf("%w; rollback failed: %v", cause, rollbackErr)
		}
		return cause
	}

	if _, err := os.Stat(state.TargetPath); err == nil {
		if err := retryOperation(options, func() error { return options.Rename(state.TargetPath, state.BackupPath) }); err != nil {
			return "", fmt.Errorf("move current binary to backup: %w", err)
		}
		originalMoved = true
		if err := protectUpgradePath(state.BackupPath, 0o755, options.Chmod); err != nil {
			return "", rollback(fmt.Errorf("protect previous binary: %w", err))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect current binary: %w", err)
	}
	if err := retryOperation(options, func() error { return options.Rename(state.StagedPath, state.TargetPath) }); err != nil {
		return "", rollback(fmt.Errorf("publish staged candidate: %w", err))
	}
	if err := protectUpgradePath(state.TargetPath, 0o755, options.Chmod); err != nil {
		return "", rollback(fmt.Errorf("protect installed binary: %w", err))
	}
	if err := options.ClearQuarantine(state.TargetPath); err != nil {
		return "", rollback(err)
	}
	installedHash, err := hashFile(state.TargetPath)
	if err != nil {
		return "", rollback(fmt.Errorf("hash installed binary: %w", err))
	}
	if !strings.EqualFold(installedHash, state.ExpectedHash) {
		return "", rollback(errors.New("installed binary checksum verification failed"))
	}
	if err := options.VerifyBinary(ctx, state.TargetPath, state.ExpectedVersion); err != nil {
		return "", rollback(err)
	}
	if !originalMoved || !fileExists(state.BackupPath) {
		return "", nil
	}
	if err := retryOperation(options, func() error { return options.Remove(state.BackupPath) }); err != nil {
		return fmt.Sprintf("could not remove the previous binary: %v", err), nil
	}
	return "", nil
}

// RunInternalUpdater executes the hidden replacement entry and records a Go-owned result.
func RunInternalUpdater(ctx context.Context, arguments []string, options ReplacementOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	currentExecutable := options.CurrentExecutable
	if currentExecutable == nil {
		currentExecutable = os.Executable
	}
	updaterPath, err := currentExecutable()
	if err != nil {
		return err
	}
	parsed, err := ParseInternalArguments(arguments, updaterPath)
	if err != nil {
		return err
	}
	state, err := ResolveInternalState(parsed)
	if err != nil {
		return err
	}
	// A completed transaction is consumed by the next ordinary startup. If a
	// detached process is retried, never apply the same transaction twice.
	if state.Status != StatusPending {
		return nil
	}
	options = normalizeReplacementOptions(options)
	if err := WaitForParent(ctx, state.ParentPID, options.ProcessAlive, options.ParentSleep); err != nil {
		cleanupInternalUpdaterFiles(state, options, true)
		return recordFailedState(state, err)
	}
	warning, err := ApplyTransaction(ctx, state, options)
	if err != nil {
		cleanupInternalUpdaterFiles(state, options, true)
		return recordFailedState(state, err)
	}
	if runtime.GOOS != "windows" {
		if cleanupErr := removeUpdaterCopyWith(state.UpdaterPath, options.Remove); cleanupErr != nil {
			if warning == "" {
				warning = fmt.Sprintf("could not remove detached updater: %v", cleanupErr)
			} else {
				warning += fmt.Sprintf("; detached updater cleanup also failed: %v", cleanupErr)
			}
		}
	}
	state.Status = StatusSucceeded
	state.Message = ""
	if warning != "" {
		state.Status = StatusSucceededCleanupWarn
		state.Message = stateMessageCleanup
	}
	if err := WriteState(state); err != nil {
		return fmt.Errorf("persist update result: %w", err)
	}
	return nil
}

func cleanupInternalUpdaterFiles(state UpdateTransaction, options ReplacementOptions, staged bool) {
	if staged {
		_ = retryOperation(options, func() error {
			err := options.Remove(state.StagedPath)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		})
	}
	if runtime.GOOS != "windows" {
		_ = removeUpdaterCopyWith(state.UpdaterPath, options.Remove)
	}
}

func recordFailedState(state UpdateTransaction, cause error) error {
	if cause == nil {
		cause = errors.New("update failed")
	}
	if err := writeFailedState(state, cause); err != nil {
		// Keep the original failure for callers while making state persistence
		// failure explicit without replacing the operation's root cause.
		return errors.Join(cause, errors.New("could not persist update failure state"))
	}
	return cause
}

func normalizeReplacementOptions(options ReplacementOptions) ReplacementOptions {
	if options.VerifyBinary == nil {
		options.VerifyBinary = func(ctx context.Context, path, version string) error {
			return VerifyBinary(ctx, path, version, nil, []string{"YCY_INTERNAL_UPDATE_VERIFY=1"})
		}
	}
	if options.ClearQuarantine == nil {
		options.ClearQuarantine = clearQuarantine
	}
	if options.Chmod == nil {
		options.Chmod = os.Chmod
	}
	if options.Remove == nil {
		options.Remove = os.Remove
	}
	if options.Rename == nil {
		options.Rename = os.Rename
	}
	if options.Sleep == nil {
		options.Sleep = func(duration time.Duration) error {
			time.Sleep(duration)
			return nil
		}
	}
	if options.RetryCount <= 0 {
		options.RetryCount = fileRetryCount
	}
	if options.ProcessAlive == nil {
		options.ProcessAlive = defaultProcessAlive
	}
	if options.ParentSleep == nil {
		options.ParentSleep = func(duration time.Duration) error {
			return options.Sleep(duration)
		}
	}
	return options
}

func retryOperation(options ReplacementOptions, operation func() error) error {
	return retryFileOperation(options.RetryCount, options.Sleep, operation)
}

func retryFileOperation(retryCount int, sleep func(time.Duration) error, operation func() error) error {
	var last error
	for attempt := 0; attempt < retryCount; attempt++ {
		if err := operation(); err == nil {
			return nil
		} else {
			last = err
			if !retryableFileError(err) || attempt == retryCount-1 {
				return err
			}
		}
		if err := sleep(fileRetryInterval); err != nil {
			return err
		}
	}
	return last
}

func retryableFileError(err error) bool {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		err = pathError.Err
	}
	return isRetryableFileError(err)
}

func defaultFileSleep(duration time.Duration) error {
	time.Sleep(duration)
	return nil
}

func removeUpgradeFile(path string) error {
	return retryFileOperation(fileRetryCount, defaultFileSleep, func() error { return os.Remove(path) })
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var hasher hash.Hash = sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writeFailedState(state UpdateTransaction, cause error) error {
	state.Status = StatusFailed
	state.Message = safeFailureMessage(cause)
	return WriteState(state)
}
