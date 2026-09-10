package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	InternalApplyMarker  = "--internal-apply-update"
	stateSuffix          = ".go-update-state.json"
	stateTempSuffix      = ".tmp"
	stateTempMaxAge      = time.Minute
	stateMessageFailed   = "replacement failed"
	stateMessageRollback = "rollback failed"
	stateMessageCleanup  = "cleanup warning"
	stateMessageParent   = "parent wait failed"
)

var consumeStateMu sync.Mutex

// UpdateStatus is the Go-owned transaction result vocabulary.
type UpdateStatus string

const (
	StatusPending              UpdateStatus = "pending"
	StatusSucceeded            UpdateStatus = "succeeded"
	StatusSucceededCleanupWarn UpdateStatus = "succeeded_with_cleanup_warning"
	StatusFailed               UpdateStatus = "failed"
)

// UpdateTransaction is persisted beside the install target and never shares the legacy namespace.
type UpdateTransaction struct {
	TransactionID   string       `json:"transactionId"`
	ParentPID       int          `json:"parentPid"`
	TargetPath      string       `json:"targetPath"`
	StagedPath      string       `json:"stagedPath"`
	BackupPath      string       `json:"backupPath"`
	ExpectedHash    string       `json:"expectedHash"`
	ExpectedVersion string       `json:"expectedVersion"`
	StatePath       string       `json:"statePath"`
	UpdaterPath     string       `json:"updaterPath"`
	CreatedAt       string       `json:"createdAt"`
	Status          UpdateStatus `json:"status"`
	Message         string       `json:"message,omitempty"`
}

// MalformedStateError distinguishes an unreadable Go-owned state document.
type MalformedStateError struct {
	Path string
	Err  error
}

func (err *MalformedStateError) Error() string {
	return fmt.Sprintf("malformed update state %s: %v", err.Path, err.Err)
}
func (err *MalformedStateError) Unwrap() error { return err.Err }

// StatePath returns the only state path consumed by the Go updater.
func StatePath(targetPath string) string { return targetPath + stateSuffix }

// NewUpdateTransaction creates a pending transaction from a staged candidate.
func NewUpdateTransaction(targetPath string, candidate Candidate, expectedVersion string, parentPID int, updaterPath string, now time.Time) UpdateTransaction {
	return UpdateTransaction{
		TransactionID:   candidate.TransactionID,
		ParentPID:       parentPID,
		TargetPath:      targetPath,
		StagedPath:      candidate.Path,
		BackupPath:      transactionBinaryPath(targetPath, ".backup.", candidate.TransactionID),
		ExpectedHash:    strings.ToLower(candidate.ExpectedHash),
		ExpectedVersion: expectedVersion,
		StatePath:       StatePath(targetPath),
		UpdaterPath:     updaterPath,
		CreatedAt:       now.UTC().Format(time.RFC3339Nano),
		Status:          StatusPending,
	}
}

// WriteState atomically publishes a validated Go-owned transaction.
func WriteState(state UpdateTransaction) error {
	if err := validateState(state); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(state.StatePath), 0o755); err != nil {
		return fmt.Errorf("create update state directory: %w", err)
	}
	tempPath := state.StatePath + "." + state.TransactionID + stateTempSuffix
	file, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write update state: %w", err)
	}
	removeTemp := true
	defer func() {
		_ = file.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return fmt.Errorf("encode update state: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync update state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close update state: %w", err)
	}
	if err := replaceStateFile(tempPath, state.StatePath); err != nil {
		return fmt.Errorf("publish update state: %w", err)
	}
	if err := protectUpgradePath(state.StatePath, 0o600, os.Chmod); err != nil {
		return fmt.Errorf("protect update state: %w", err)
	}
	removeTemp = false
	return nil
}

// ReadState reads and validates only the Go-owned state path supplied by the caller.
func ReadState(statePath string) (*UpdateTransaction, error) {
	if !strings.HasSuffix(filepath.Base(statePath), stateSuffix) {
		return nil, &MalformedStateError{Path: statePath, Err: errors.New("state path is outside the Go-owned namespace")}
	}
	contents, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read update state: %w", err)
	}
	var state UpdateTransaction
	if err := json.Unmarshal(contents, &state); err != nil {
		return nil, &MalformedStateError{Path: statePath, Err: err}
	}
	if err := validateState(state); err != nil {
		return nil, &MalformedStateError{Path: statePath, Err: err}
	}
	return &state, nil
}

// ConsumeState reports pending state or removes a completed state exactly once.
func ConsumeState(targetPath string) (*UpdateTransaction, error) {
	consumeStateMu.Lock()
	defer consumeStateMu.Unlock()

	statePath := StatePath(targetPath)
	state, err := ReadState(statePath)
	cleanupTemporaryFiles(targetPath, state, defaultProcessAlive)
	if err != nil || state == nil || state.Status == StatusPending {
		return state, err
	}
	if err := removeUpgradeFile(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return state, fmt.Errorf("consume update state: %w", err)
	} else if errors.Is(err, os.ErrNotExist) {
		// Another startup won the one-time consume race after this reader loaded
		// the document. Do not present the same result twice.
		return nil, nil
	}
	if state.UpdaterPath != "" {
		_ = removeUpdaterCopy(state.UpdaterPath)
	}
	return state, nil
}

// FormatStateResult maps a completed transaction to its one-time human result.
func FormatStateResult(state UpdateTransaction) string {
	version := safeStateVersion(state.ExpectedVersion)
	switch state.Status {
	case StatusSucceeded:
		return fmt.Sprintf("Updated ycy to v%s.", version)
	case StatusSucceededCleanupWarn:
		return fmt.Sprintf("Updated ycy to v%s, but cleanup failed.", version)
	case StatusFailed:
		if hasRollbackFailure(state.Message) {
			return "Previous update failed and rollback failed."
		}
		if strings.Contains(strings.ToLower(state.Message), "parent wait") {
			return "Previous update did not start."
		}
		return "Previous update failed and was rolled back."
	default:
		return "An update is being applied. Retry in a moment."
	}
}

func safeStateVersion(value string) string {
	value = strings.TrimSpace(value)
	if _, err := parseVersion(value); err != nil {
		return "unknown"
	}
	return value
}

func hasRollbackFailure(message string) bool {
	return strings.Contains(strings.ToLower(message), "rollback failed")
}

// safeFailureMessage is the only error detail persisted in a transaction.
// Paths, credentials, protocol payloads, and raw filesystem messages stay
// process-local diagnostics and cannot leak through startup consumption.
func safeFailureMessage(err error) string {
	if err == nil {
		return stateMessageFailed
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "rollback failed"):
		return stateMessageRollback
	case strings.Contains(message, "wait") || strings.Contains(message, "parent"):
		return stateMessageParent
	case strings.Contains(message, "cleanup") || strings.Contains(message, "remove the previous"):
		return stateMessageCleanup
	default:
		return stateMessageFailed
	}
}

// InternalUpdateArgs creates the exact hidden key/value argument sequence.
func InternalUpdateArgs(state UpdateTransaction) []string {
	return []string{
		InternalApplyMarker,
		"--transaction-id", state.TransactionID,
		"--parent-pid", strconv.Itoa(state.ParentPID),
		"--target-path", state.TargetPath,
		"--staged-path", state.StagedPath,
		"--backup-path", state.BackupPath,
		"--expected-hash", state.ExpectedHash,
		"--expected-version", state.ExpectedVersion,
		"--state-path", state.StatePath,
	}
}

// FindInternalMarker returns the marker position even when ordinary argv surrounds it.
func FindInternalMarker(arguments []string) int {
	for index, argument := range arguments {
		if argument == InternalApplyMarker {
			return index
		}
	}
	return -1
}

// ParseInternalArguments parses the hidden entry without exposing it in Cobra.
func ParseInternalArguments(arguments []string, updaterPath string) (UpdateTransaction, error) {
	marker := FindInternalMarker(arguments)
	if marker < 0 {
		return UpdateTransaction{}, errors.New("internal update marker is missing")
	}
	values := make(map[string]string)
	tail := arguments[marker+1:]
	if len(tail)%2 != 0 {
		return UpdateTransaction{}, errors.New("invalid internal updater arguments")
	}
	allowed := map[string]bool{
		"--transaction-id": true, "--parent-pid": true, "--target-path": true,
		"--staged-path": true, "--backup-path": true, "--expected-hash": true,
		"--expected-version": true, "--state-path": true,
	}
	for index := 0; index < len(tail); index += 2 {
		name, value := tail[index], tail[index+1]
		if !allowed[name] || value == "" {
			return UpdateTransaction{}, fmt.Errorf("invalid internal updater argument %q", name)
		}
		if _, exists := values[name]; exists {
			return UpdateTransaction{}, fmt.Errorf("duplicate internal updater argument %q", name)
		}
		values[name] = value
	}
	read := func(name string) (string, error) {
		value := values[name]
		if value == "" {
			return "", fmt.Errorf("missing internal updater argument: %s", name)
		}
		return value, nil
	}
	transactionID, err := read("--transaction-id")
	if err != nil {
		return UpdateTransaction{}, err
	}
	parentRaw, err := read("--parent-pid")
	if err != nil {
		return UpdateTransaction{}, err
	}
	parentPID, err := strconv.Atoi(parentRaw)
	if err != nil || parentPID <= 0 {
		return UpdateTransaction{}, errors.New("internal updater parent PID is invalid")
	}
	targetPath, err := read("--target-path")
	if err != nil {
		return UpdateTransaction{}, err
	}
	stagedPath, err := read("--staged-path")
	if err != nil {
		return UpdateTransaction{}, err
	}
	backupPath, err := read("--backup-path")
	if err != nil {
		return UpdateTransaction{}, err
	}
	expectedHash, err := read("--expected-hash")
	if err != nil {
		return UpdateTransaction{}, err
	}
	expectedVersion, err := read("--expected-version")
	if err != nil {
		return UpdateTransaction{}, err
	}
	statePath, err := read("--state-path")
	if err != nil {
		return UpdateTransaction{}, err
	}
	state := UpdateTransaction{
		TransactionID: transactionID, ParentPID: parentPID, TargetPath: targetPath,
		StagedPath: stagedPath, BackupPath: backupPath, ExpectedHash: expectedHash,
		ExpectedVersion: expectedVersion, StatePath: statePath, UpdaterPath: updaterPath,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: StatusPending,
	}
	if err := validateState(state); err != nil {
		return UpdateTransaction{}, err
	}
	return state, nil
}

// ResolveInternalState lets a stored Go transaction override parsed values for the same ID.
func ResolveInternalState(parsed UpdateTransaction) (UpdateTransaction, error) {
	stored, err := ReadState(parsed.StatePath)
	if err != nil {
		return UpdateTransaction{}, err
	}
	if stored != nil && stored.TransactionID == parsed.TransactionID {
		return *stored, nil
	}
	return parsed, nil
}

func validateState(state UpdateTransaction) error {
	if strings.TrimSpace(state.TransactionID) == "" || strings.ContainsAny(state.TransactionID, `/\\`) {
		return errors.New("update transaction ID is invalid")
	}
	if state.ParentPID <= 0 {
		return errors.New("update parent PID is invalid")
	}
	for name, value := range map[string]string{
		"target path": state.TargetPath, "staged path": state.StagedPath, "backup path": state.BackupPath,
		"state path": state.StatePath, "updater path": state.UpdaterPath, "created time": state.CreatedAt,
		"expected version": state.ExpectedVersion,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("update %s is empty", name)
		}
	}
	if !digestPattern.MatchString(state.ExpectedHash) {
		return errors.New("update expected checksum is invalid")
	}
	if _, err := parseVersion(state.ExpectedVersion); err != nil {
		return fmt.Errorf("update expected version is invalid: %w", err)
	}
	if state.StatePath != StatePath(state.TargetPath) {
		return errors.New("update state path is not target-owned")
	}
	if filepath.Dir(state.TargetPath) != filepath.Dir(state.StagedPath) || filepath.Dir(state.TargetPath) != filepath.Dir(state.BackupPath) {
		return errors.New("update files must be in the target binary directory")
	}
	switch state.Status {
	case StatusPending, StatusSucceeded, StatusSucceededCleanupWarn, StatusFailed:
		return nil
	default:
		return errors.New("update status is invalid")
	}
}

func cleanupTemporaryFiles(targetPath string, pending *UpdateTransaction, alive func(int) bool) {
	entries, err := os.ReadDir(filepath.Dir(targetPath))
	if err != nil {
		return
	}
	statePath := StatePath(targetPath)
	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join(filepath.Dir(targetPath), name)
		prefix := filepath.Base(targetPath) + ".tmp."
		if strings.HasPrefix(name, prefix) {
			pid, parseErr := strconv.Atoi(strings.TrimPrefix(name, prefix))
			if parseErr == nil && pid > 0 && !alive(pid) {
				_ = removeUpgradeFile(fullPath)
			}
			continue
		}
		statePrefix := filepath.Base(statePath) + "."
		if !strings.HasPrefix(name, statePrefix) || !strings.HasSuffix(name, stateTempSuffix) {
			continue
		}
		if pending != nil && pending.Status == StatusPending {
			continue
		}
		info, statErr := entry.Info()
		if statErr == nil && time.Since(info.ModTime()) >= stateTempMaxAge {
			_ = removeUpgradeFile(fullPath)
		}
	}
}

func defaultProcessAlive(pid int) bool {
	return processAlive(pid)
}

func removeUpdaterCopy(path string) error {
	return removeUpdaterCopyWith(path, os.Remove)
}

func removeUpdaterCopyWith(path string, remove func(string) error) error {
	temporary, err := filepath.Abs(os.TempDir())
	if err != nil {
		return nil
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	relative, err := filepath.Rel(temporary, resolved)
	if err != nil || relative == "" || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) || !strings.HasPrefix(filepath.Base(resolved), "ycy-updater-") {
		return nil
	}
	if remove == nil {
		remove = os.Remove
	}
	err = retryFileOperation(fileRetryCount, defaultFileSleep, func() error { return remove(resolved) })
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// WaitForParent is kept in this slice as the hidden entry's bounded process gate.
func WaitForParent(ctx context.Context, pid int, alive func(int) bool, sleep func(time.Duration) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if alive == nil {
		alive = defaultProcessAlive
	}
	if sleep == nil {
		sleep = func(duration time.Duration) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for alive(pid) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for process %d to exit", pid)
		default:
		}
		if err := sleep(50 * time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}
