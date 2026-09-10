package ycycmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/updater"
)

func TestHiddenUpgradeRouteIsolatedFromOrdinaryArguments(t *testing.T) {
	if handled, err := runHiddenUpgrade([]string{"ordinary", "value"}); handled || err != nil {
		t.Fatalf("ordinary route = handled %v, err %v", handled, err)
	}
	state := testStateForStartup(t)
	if err := updater.WriteState(state); err != nil {
		t.Fatal(err)
	}
	arguments := append([]string{"ordinary", "value"}, updater.InternalUpdateArgs(state)...)
	// The fixture intentionally fails before touching a target; marker discovery is
	// the property under test and the hidden route must not fall through Cobra.
	handled, err := runHiddenUpgrade(arguments)
	if !handled || err == nil {
		t.Fatalf("hidden route = handled %v, err %v", handled, err)
	}
}

func TestDispatchThumbnailWorkerKeepsPrivateRouteBounded(t *testing.T) {
	if handled, err := DispatchThumbnailWorker([]string{"fs"}, bytes.NewReader(nil), &bytes.Buffer{}); handled || err != nil {
		t.Fatalf("ordinary route = handled %v, err %v", handled, err)
	}
	handled, err := DispatchThumbnailWorker([]string{"--internal-thumbnail-worker"}, bytes.NewReader([]byte{0, 0, 0, 1}), &bytes.Buffer{})
	if !handled || err == nil || !strings.Contains(err.Error(), "thumbnail worker:") {
		t.Fatalf("worker route = handled %v, err %v", handled, err)
	}
}

func TestVersionFlagsSkipStartupResultConsumption(t *testing.T) {
	output, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       output,
		Diagnostics:  diagnostics,
	})
	if err := consumeUpgradeStartup([]string{"--version"}, experience); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 || diagnostics.Len() != 0 {
		t.Fatalf("version startup output = %q / %q", output.String(), diagnostics.String())
	}
}

func TestConsumeUpgradeStartupReportsCompletedAndBlocksPending(t *testing.T) {
	testCases := []struct {
		name       string
		status     updater.UpdateStatus
		wantOutput string
		wantError  string
	}{
		{name: "completed", status: updater.StatusSucceeded, wantOutput: "Updated ycy to v1.0.1.\n"},
		{name: "pending", status: updater.StatusPending, wantError: "An update is being applied. Retry in a moment.\n"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state := testStateForStartup(t)
			state.Status = testCase.status
			output, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
				Output:       output,
				Diagnostics:  diagnostics,
			})
			err := consumeUpgradeStartupWith(
				[]string{"config", "cm", "list"},
				experience,
				func() (string, error) { return state.TargetPath, nil },
				func(target string) (*updater.UpdateTransaction, error) {
					if target != state.TargetPath {
						t.Fatalf("target = %q, want %q", target, state.TargetPath)
					}
					return &state, nil
				},
			)
			if testCase.status == updater.StatusPending {
				if err == nil || err.Error() != "update is still pending" {
					t.Fatalf("pending error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != testCase.wantOutput {
				t.Fatalf("stdout = %q, want %q", got, testCase.wantOutput)
			}
			if got := diagnostics.String(); got != testCase.wantError {
				t.Fatalf("stderr = %q, want %q", got, testCase.wantError)
			}
		})
	}
}

func testStateForStartup(t *testing.T) updater.UpdateTransaction {
	t.Helper()
	directory := t.TempDir()
	target := filepath.Join(directory, "ycy")
	state := updater.UpdateTransaction{
		TransactionID:   "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		ParentPID:       2147483647,
		TargetPath:      target,
		StagedPath:      target + ".new.bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		BackupPath:      target + ".backup.bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		ExpectedHash:    strings.Repeat("a", 64),
		ExpectedVersion: "1.0.1",
		StatePath:       updater.StatePath(target),
		UpdaterPath:     filepath.Join(directory, "updater"),
		CreatedAt:       "2026-08-25T00:00:00Z",
		Status:          updater.StatusPending,
	}
	if err := os.WriteFile(state.StagedPath, []byte("candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	return state
}
