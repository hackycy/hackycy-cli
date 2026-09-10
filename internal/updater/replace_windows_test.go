//go:build windows

package updater

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/windowsacl"
	"golang.org/x/sys/windows"
)

func TestApplyTransactionRetriesWindowsSharingViolation(t *testing.T) {
	state := replacementState(t, "old binary", "new binary")
	handle := lockUpgradeWithoutDeleteSharing(t, state.TargetPath)
	released := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = windows.CloseHandle(handle)
		close(released)
	}()
	t.Cleanup(func() { <-released })

	options := replacementOptions(t)
	options.RetryCount = fileRetryCount
	options.Sleep = defaultFileSleep
	options.Rename = func(old, new string) error {
		if old == state.StagedPath && new == state.TargetPath {
			if err := windowsacl.VerifyPrivatePath(state.BackupPath); err != nil {
				t.Fatalf("backup DACL: %v", err)
			}
		}
		return os.Rename(old, new)
	}
	options.VerifyBinary = func(_ context.Context, path, _ string) error {
		return windowsacl.VerifyPrivatePath(path)
	}
	if warning, err := ApplyTransaction(context.Background(), state, options); err != nil || warning != "" {
		t.Fatalf("apply = warning %q, err %v", warning, err)
	}
	contents, err := os.ReadFile(state.TargetPath)
	if err != nil || string(contents) != "new binary" {
		t.Fatalf("target = %q, %v", contents, err)
	}
}

func TestRunInternalUpdaterRecordsWindowsSharingViolation(t *testing.T) {
	state := replacementState(t, "old binary", "new binary")
	if err := WriteState(state); err != nil {
		t.Fatal(err)
	}
	handle := lockUpgradeWithoutDeleteSharing(t, state.TargetPath)
	defer windows.CloseHandle(handle)
	options := replacementOptions(t)
	options.RetryCount = 1
	options.CurrentExecutable = func() (string, error) { return state.UpdaterPath, nil }
	if err := RunInternalUpdater(context.Background(), InternalUpdateArgs(state), options); err == nil {
		t.Fatal("locked update unexpectedly succeeded")
	}
	read, err := ReadState(state.StatePath)
	if err != nil || read == nil || read.Status != StatusFailed {
		t.Fatalf("failed state = %#v, %v", read, err)
	}
	contents, err := os.ReadFile(state.TargetPath)
	if err != nil || string(contents) != "old binary" {
		t.Fatalf("target after sharing violation = %q, %v", contents, err)
	}
}

func lockUpgradeWithoutDeleteSharing(t *testing.T, path string) windows.Handle {
	t.Helper()
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(path),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}
