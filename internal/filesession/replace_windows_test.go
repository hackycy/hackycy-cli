//go:build windows

package filesession

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestReplaceSessionFileRetriesSharingViolation(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "record.json")
	candidate := filepath.Join(directory, "record.json.tmp")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle := lockWithoutDeleteSharing(t, target)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = windows.CloseHandle(handle)
	}()
	if err := replaceSessionFile(candidate, target); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "new" {
		t.Fatalf("target contents = %q, %v", contents, err)
	}
}

func TestReplaceSessionFileRetainsTargetWhenSharingViolationPersists(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "record.json")
	candidate := filepath.Join(directory, "record.json.tmp")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle := lockWithoutDeleteSharing(t, target)
	defer windows.CloseHandle(handle)
	if err := replaceSessionFileWithRetry(candidate, target, 1, 0); err == nil {
		t.Fatal("replaceSessionFileWithRetry unexpectedly replaced a locked target")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "old" {
		t.Fatalf("target contents = %q, %v", contents, err)
	}
	if contents, err := os.ReadFile(candidate); err != nil || string(contents) != "new" {
		t.Fatalf("candidate contents = %q, %v", contents, err)
	}
}

func lockWithoutDeleteSharing(t *testing.T, path string) windows.Handle {
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
