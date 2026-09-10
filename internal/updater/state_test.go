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

func testState(t *testing.T, status UpdateStatus) UpdateTransaction {
	t.Helper()
	directory := t.TempDir()
	target := nativeTestExecutablePath(filepath.Join(directory, "ycy"))
	return UpdateTransaction{
		TransactionID:   "11111111-1111-4111-8111-111111111111",
		ParentPID:       2147483647,
		TargetPath:      target,
		StagedPath:      expectedTransactionPath(target, ".new.", "11111111-1111-4111-8111-111111111111"),
		BackupPath:      expectedTransactionPath(target, ".backup.", "11111111-1111-4111-8111-111111111111"),
		ExpectedHash:    strings.Repeat("a", 64),
		ExpectedVersion: "1.2.3",
		StatePath:       StatePath(target),
		UpdaterPath:     expectedUpdaterPath(directory, "11111111"),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Status:          status,
	}
}

func TestStatePublishesAtomicallyAndConsumesCompletedOnce(t *testing.T) {
	state := testState(t, StatusSucceeded)
	if err := WriteState(state); err != nil {
		t.Fatal(err)
	}
	assertPrivateUpgradePath(t, state.StatePath, 0o600)
	read, err := ReadState(state.StatePath)
	if err != nil || read == nil || read.Status != StatusSucceeded {
		t.Fatalf("read state = %#v, %v", read, err)
	}
	if err := os.WriteFile(state.UpdaterPath, []byte("updater"), 0o600); err != nil {
		t.Fatal(err)
	}
	consumed, err := ConsumeState(state.TargetPath)
	if err != nil || consumed == nil || consumed.TransactionID != state.TransactionID {
		t.Fatalf("consume = %#v, %v", consumed, err)
	}
	if _, err := os.Stat(state.StatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state remains after consume: %v", err)
	}
	if _, err := os.Stat(state.UpdaterPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("updater remains after consume: %v", err)
	}
	if next, err := ConsumeState(state.TargetPath); err != nil || next != nil {
		t.Fatalf("second consume = %#v, %v", next, err)
	}
}

func TestPendingStateIsRetainedAndMalformedStateIsRejected(t *testing.T) {
	state := testState(t, StatusPending)
	if err := WriteState(state); err != nil {
		t.Fatal(err)
	}
	read, err := ConsumeState(state.TargetPath)
	if err != nil || read == nil || read.Status != StatusPending {
		t.Fatalf("pending consume = %#v, %v", read, err)
	}
	if _, err := os.Stat(state.StatePath); err != nil {
		t.Fatalf("pending state removed: %v", err)
	}
	if err := os.WriteFile(state.StatePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var malformed *MalformedStateError
	_, err = ReadState(state.StatePath)
	if !errors.As(err, &malformed) {
		t.Fatalf("malformed error = %v", err)
	}
}

func TestInternalArgumentsScanMarkerAndRejectMalformedPairs(t *testing.T) {
	state := testState(t, StatusPending)
	arguments := append([]string{"--ordinary", "value"}, InternalUpdateArgs(state)...)
	parsed, err := ParseInternalArguments(arguments, state.UpdaterPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TransactionID != state.TransactionID || parsed.StatePath != state.StatePath || FindInternalMarker(arguments) != 2 {
		t.Fatalf("parsed = %#v", parsed)
	}
	for _, invalid := range [][]string{
		{InternalApplyMarker, "--transaction-id"},
		{InternalApplyMarker, "--transaction-id", "one", "--transaction-id", "two"},
		{InternalApplyMarker, "--unknown", "value"},
	} {
		if _, err := ParseInternalArguments(invalid, state.UpdaterPath); err == nil {
			t.Fatalf("invalid args accepted: %#v", invalid)
		}
	}
}

func TestInternalStateOverridesParsedValuesOnlyForMatchingGoState(t *testing.T) {
	state := testState(t, StatusPending)
	if err := WriteState(state); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseInternalArguments(InternalUpdateArgs(state), state.UpdaterPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed.ExpectedHash = strings.Repeat("b", 64)
	resolved, err := ResolveInternalState(parsed)
	if err != nil || resolved.ExpectedHash != state.ExpectedHash {
		t.Fatalf("resolved = %#v, %v", resolved, err)
	}
	foreign := parsed
	foreign.TransactionID = "22222222-2222-4222-8222-222222222222"
	resolved, err = ResolveInternalState(foreign)
	if err != nil || resolved.ExpectedHash != foreign.ExpectedHash {
		t.Fatalf("foreign resolved = %#v, %v", resolved, err)
	}
}

func TestStateTemporaryCleanupAndParentPolling(t *testing.T) {
	state := testState(t, StatusSucceeded)
	oldPath := state.StatePath + ".old.tmp"
	if err := os.WriteFile(oldPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}
	cleanupTemporaryFiles(state.TargetPath, nil, func(int) bool { return false })
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp remains: %v", err)
	}
	checks := 0
	sleeps := 0
	err := WaitForParent(context.Background(), 42, func(int) bool {
		checks++
		return checks < 3
	}, func(time.Duration) error {
		sleeps++
		return nil
	})
	if err != nil || checks != 3 || sleeps != 2 {
		t.Fatalf("poll = checks %d sleeps %d err %v", checks, sleeps, err)
	}
}

func TestGoStateNamespaceDoesNotTouchAdjacentState(t *testing.T) {
	state := testState(t, StatusSucceeded)
	legacyPath := strings.TrimSuffix(state.StatePath, ".go-update-state.json") + ".update-state.json"
	if err := os.WriteFile(legacyPath, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(state); err != nil {
		t.Fatal(err)
	}
	if _, err := ConsumeState(state.TargetPath); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(legacyPath)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("adjacent state changed: %q, %v", contents, err)
	}
}

func TestFormatStateResultDoesNotExposePersistedRawFailure(t *testing.T) {
	cleanup := UpdateTransaction{ExpectedVersion: "1.2.3", Status: StatusSucceededCleanupWarn, Message: "cleanup failed /private/path Bearer secret"}
	if got := FormatStateResult(cleanup); strings.Contains(got, "/private/path") || strings.Contains(got, "secret") || !strings.Contains(got, "cleanup failed") {
		t.Fatalf("cleanup result = %q", got)
	}
	failure := UpdateTransaction{ExpectedVersion: "1.2.3", Status: StatusFailed, Message: "replacement failed; rollback failed: /private/path token=secret"}
	if got := FormatStateResult(failure); strings.Contains(got, "/private/path") || strings.Contains(got, "secret") || !strings.Contains(got, "rollback failed") {
		t.Fatalf("failure result = %q", got)
	}
}

func TestProcessProbeRetainsLiveCurrentProcessAndRejectsForeignStateNamespace(t *testing.T) {
	if !defaultProcessAlive(os.Getpid()) {
		t.Fatal("current process was reported dead")
	}
	foreign := filepath.Join(t.TempDir(), "ycy.update-state.json")
	if err := os.WriteFile(foreign, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(foreign); err == nil {
		t.Fatal("foreign state namespace unexpectedly read")
	}
	contents, err := os.ReadFile(foreign)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("foreign state changed: %q, %v", contents, err)
	}
}
