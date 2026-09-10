package ycycmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/hackycy/hackycy-cli/internal/fsthumbnail"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/updater"
	upgradecommand "github.com/hackycy/hackycy-cli/pkg/cmd/upgrade"
)

func runHiddenUpgrade(arguments []string) (bool, error) {
	if updater.FindInternalMarker(arguments) < 0 {
		return false, nil
	}
	err := updater.RunInternalUpdater(context.Background(), arguments, updater.ReplacementOptions{})
	return true, err
}

// RunHiddenUpgrade handles the private updater entry before Cobra sees argv.
func RunHiddenUpgrade(arguments []string) (bool, error) {
	return runHiddenUpgrade(arguments)
}

// DispatchThumbnailWorker handles the private thumbnail worker entry before
// Cobra sees argv and preserves the worker's length-framed stream protocol.
func DispatchThumbnailWorker(arguments []string, input io.Reader, output io.Writer) (bool, error) {
	if !fsthumbnail.IsThumbnailWorkerInvocation(arguments) {
		return false, nil
	}
	return true, fsthumbnail.RunThumbnailWorker(input, output)
}

func consumeUpgradeStartup(arguments []string, experience *terminalexperience.Runtime) error {
	return consumeUpgradeStartupWith(arguments, experience, os.Executable, updater.ConsumeState)
}

// ConsumeUpgradeStartup consumes a completed hidden-update transaction before
// ordinary command execution.
func ConsumeUpgradeStartup(arguments []string, experience *terminalexperience.Runtime) error {
	return consumeUpgradeStartup(arguments, experience)
}

func consumeUpgradeStartupWith(arguments []string, experience *terminalexperience.Runtime, executable func() (string, error), consumeState func(string) (*updater.UpdateTransaction, error)) error {
	if updater.FindInternalMarker(arguments) >= 0 || hasVersionFlag(arguments) {
		return nil
	}
	target, err := executable()
	if err != nil {
		return err
	}
	state, err := consumeState(target)
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}
	if state.Status == updater.StatusPending {
		_, _ = fmt.Fprintln(experience.DiagnosticWriter(), upgradecommand.StateMessage(*state))
		return fmt.Errorf("update is still pending")
	}
	return upgradecommand.PresentResult(context.Background(), experience, updater.UpgradeResult{PreviousState: state}, nil)
}

func hasVersionFlag(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--version" || argument == "-V" {
			return true
		}
	}
	return false
}
