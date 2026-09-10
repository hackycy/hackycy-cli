package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func replacementState(t *testing.T, targetContents, stagedContents string) UpdateTransaction {
	t.Helper()
	directory := t.TempDir()
	target := nativeTestExecutablePath(filepath.Join(directory, "ycy"))
	transactionID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	state := UpdateTransaction{
		TransactionID:   transactionID,
		ParentPID:       2147483647,
		TargetPath:      target,
		StagedPath:      expectedTransactionPath(target, ".new.", transactionID),
		BackupPath:      expectedTransactionPath(target, ".backup.", transactionID),
		ExpectedHash:    sha256Bytes([]byte(stagedContents)),
		ExpectedVersion: "1.2.3",
		StatePath:       StatePath(target),
		UpdaterPath:     expectedUpdaterPath(directory, transactionID),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Status:          StatusPending,
	}
	if targetContents != "" {
		if err := os.WriteFile(state.TargetPath, []byte(targetContents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(state.StagedPath, []byte(stagedContents), 0o600); err != nil {
		t.Fatal(err)
	}
	return state
}

func replacementOptions(t *testing.T) ReplacementOptions {
	t.Helper()
	return ReplacementOptions{
		VerifyBinary:    func(context.Context, string, string) error { return nil },
		ClearQuarantine: func(string) error { return nil },
		Sleep:           func(time.Duration) error { return nil },
		RetryCount:      3,
		ProcessAlive:    func(int) bool { return false },
		ParentSleep:     func(time.Duration) error { return nil },
	}
}

func TestApplyTransactionReplacesAndRemovesBackup(t *testing.T) {
	state := replacementState(t, "old binary", "new binary")
	if warning, err := ApplyTransaction(context.Background(), state, replacementOptions(t)); err != nil || warning != "" {
		t.Fatalf("apply = warning %q, err %v", warning, err)
	}
	contents, err := os.ReadFile(state.TargetPath)
	if err != nil || string(contents) != "new binary" {
		t.Fatalf("target = %q, %v", contents, err)
	}
	if fileExists(state.BackupPath) || fileExists(state.StagedPath) {
		t.Fatal("backup or staged file remains")
	}
	assertPrivateUpgradePath(t, state.TargetPath, 0o755)
}

func TestApplyTransactionRollsBackOnHashOrVersionFailure(t *testing.T) {
	state := replacementState(t, "old binary", "new binary")
	state.ExpectedHash = strings.Repeat("0", 64)
	if _, err := ApplyTransaction(context.Background(), state, replacementOptions(t)); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("hash failure = %v", err)
	}
	contents, err := os.ReadFile(state.TargetPath)
	if err != nil || string(contents) != "old binary" || fileExists(state.BackupPath) {
		t.Fatalf("rollback target = %q, backup=%v, err=%v", contents, fileExists(state.BackupPath), err)
	}

	state = replacementState(t, "old binary", "new binary")
	options := replacementOptions(t)
	options.VerifyBinary = func(context.Context, string, string) error { return errors.New("wrong version") }
	if _, err := ApplyTransaction(context.Background(), state, options); err == nil || !strings.Contains(err.Error(), "wrong version") {
		t.Fatalf("version failure = %v", err)
	}
	contents, err = os.ReadFile(state.TargetPath)
	if err != nil || string(contents) != "old binary" {
		t.Fatalf("version rollback target = %q, %v", contents, err)
	}
}

func TestApplyTransactionReportsBackupCleanupWarning(t *testing.T) {
	state := replacementState(t, "old binary", "new binary")
	options := replacementOptions(t)
	options.Remove = func(path string) error {
		if path == state.BackupPath {
			return errors.New("permission denied")
		}
		return os.Remove(path)
	}
	warning, err := ApplyTransaction(context.Background(), state, options)
	if err != nil || warning != "could not remove the previous binary: permission denied" {
		t.Fatalf("warning = %q, err %v", warning, err)
	}
	if !fileExists(state.BackupPath) {
		t.Fatal("backup was removed despite injected failure")
	}
}

func TestApplyTransactionSupportsMissingTargetAndBackupConflict(t *testing.T) {
	state := replacementState(t, "", "new binary")
	if _, err := ApplyTransaction(context.Background(), state, replacementOptions(t)); err != nil {
		t.Fatal(err)
	}
	if !fileExists(state.TargetPath) || fileExists(state.BackupPath) {
		t.Fatal("missing-target publication left unexpected files")
	}

	state = replacementState(t, "old binary", "new binary")
	if err := os.WriteFile(state.BackupPath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyTransaction(context.Background(), state, replacementOptions(t)); err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("backup conflict = %v", err)
	}
}

func TestApplyTransactionRetainsRollbackFailureWhenTargetWasInitiallyMissing(t *testing.T) {
	state := replacementState(t, "", "new binary")
	options := replacementOptions(t)
	options.VerifyBinary = func(context.Context, string, string) error { return errors.New("self-check failed") }
	options.Remove = func(path string) error {
		if path == state.TargetPath {
			return errors.New("target is locked")
		}
		return os.Remove(path)
	}
	if _, err := ApplyTransaction(context.Background(), state, options); err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("rollback failure = %v", err)
	}
	if !fileExists(state.TargetPath) {
		t.Fatal("new target was removed despite rollback failure")
	}
}

func TestApplyTransactionRetriesRetryableRename(t *testing.T) {
	state := replacementState(t, "old binary", "new binary")
	options := replacementOptions(t)
	originalRename := os.Rename
	attempts := 0
	options.Rename = func(old, new string) error {
		attempts++
		if attempts == 1 {
			return &os.PathError{Op: "rename", Path: old, Err: os.ErrPermission}
		}
		return originalRename(old, new)
	}
	if _, err := ApplyTransaction(context.Background(), state, options); err != nil {
		t.Fatal(err)
	}
	if attempts < 3 {
		t.Fatalf("rename attempts = %d, want retry for target and staged", attempts)
	}
}

func TestRunInternalUpdaterRecordsSuccessAndFailure(t *testing.T) {
	state := replacementState(t, "old binary", "new binary")
	if err := WriteState(state); err != nil {
		t.Fatal(err)
	}
	options := replacementOptions(t)
	options.CurrentExecutable = func() (string, error) { return state.UpdaterPath, nil }
	if err := RunInternalUpdater(context.Background(), InternalUpdateArgs(state), options); err != nil {
		t.Fatal(err)
	}
	read, err := ReadState(state.StatePath)
	if err != nil || read == nil || read.Status != StatusSucceeded {
		t.Fatalf("success state = %#v, %v", read, err)
	}

	failure := replacementState(t, "old binary", "new binary")
	failure.ExpectedHash = strings.Repeat("0", 64)
	if err := WriteState(failure); err != nil {
		t.Fatal(err)
	}
	options = replacementOptions(t)
	options.CurrentExecutable = func() (string, error) { return failure.UpdaterPath, nil }
	if err := RunInternalUpdater(context.Background(), InternalUpdateArgs(failure), options); err == nil {
		t.Fatal("failed update unexpectedly succeeded")
	}
	read, err = ReadState(failure.StatePath)
	if err != nil || read == nil || read.Status != StatusFailed {
		t.Fatalf("failure state = %#v, %v", read, err)
	}
}

func TestRunInternalUpdaterDoesNotReapplyCompletedTransaction(t *testing.T) {
	state := replacementState(t, "stable binary", "new binary")
	state.Status = StatusSucceeded
	if err := WriteState(state); err != nil {
		t.Fatal(err)
	}
	options := replacementOptions(t)
	options.CurrentExecutable = func() (string, error) { return state.UpdaterPath, nil }
	if err := RunInternalUpdater(context.Background(), InternalUpdateArgs(state), options); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(state.TargetPath)
	if err != nil || string(contents) != "stable binary" {
		t.Fatalf("completed transaction changed target = %q, %v", contents, err)
	}
}
