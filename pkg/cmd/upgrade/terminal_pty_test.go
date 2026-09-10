package upgrade

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	"github.com/hackycy/hackycy-cli/internal/updater"
)

func TestRunUpgradeRichPTYRestoresPrimaryScreenAndReplaysSafeTranscript(t *testing.T) {
	const helperEnvironment = "YCY_UPGRADE_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runUpgradeRichPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name          string
		width, height uint16
		color         bool
	}{
		{name: "wide color", width: 120, height: 40, color: true},
		{name: "wide no color", width: 120, height: 40, color: false},
		{name: "compact color", width: 40, height: 15, color: true},
		{name: "compact no color", width: 40, height: 15, color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunUpgradeRichPTYRestoresPrimaryScreenAndReplaysSafeTranscript$")
			command.Env = upgradePTYEnvironmentWith(map[string]string{
				"NO_COLOR":              map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                  "xterm-256color",
				helperEnvironment:       "1",
				"YCY_UPGRADE_PTY_START": "1",
			})
			output := runUpgradePTYProcess(t, command, testCase.width, testCase.height)
			assertUpgradeRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runUpgradeRichPTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_UPGRADE_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	err := runUpgrade(&Options{
		Context:        context.Background(),
		Terminal:       experience,
		CurrentVersion: "1.0.0",
		run: func(_ context.Context, options updater.UpgradeOptions) (updater.UpgradeResult, error) {
			options.Observer.PreviousState(updater.UpdateTransaction{ExpectedVersion: "1.0.1", Status: updater.StatusSucceeded})
			for _, event := range []updater.UpgradePhaseEvent{
				{Phase: updater.UpgradePhaseConsumeStartupTransaction, State: updater.UpgradePhaseActive},
				{Phase: updater.UpgradePhaseConsumeStartupTransaction, State: updater.UpgradePhaseCompleted, Detail: "Startup transaction checked"},
				{Phase: updater.UpgradePhaseResolveRelease, State: updater.UpgradePhaseActive},
				{Phase: updater.UpgradePhaseResolveRelease, State: updater.UpgradePhaseCompleted, Detail: "Release metadata resolved", CurrentVersion: "1.0.0", CandidateVersion: "2.0.0", TargetOS: "linux", TargetArchitecture: "amd64"},
				{Phase: updater.UpgradePhaseResolveArtifact, State: updater.UpgradePhaseActive},
				{Phase: updater.UpgradePhaseResolveArtifact, State: updater.UpgradePhaseCompleted, Detail: "Artifact and checksum resolved", ArtifactName: "ycy-linux-x64", ChecksumSource: updater.ChecksumReleaseDigest},
				{Phase: updater.UpgradePhaseComplete, State: updater.UpgradePhaseActive},
				{Phase: updater.UpgradePhaseComplete, State: updater.UpgradePhaseCompleted, Detail: "Update scheduled", CandidateVersion: "2.0.0"},
			} {
				options.Observer.Phase(event)
			}
			return updater.UpgradeResult{Scheduled: true, ScheduledVersion: "2.0.0"}, nil
		},
	})
	if err != nil {
		t.Fatalf("runUpgrade() error = %v", err)
	}
}

func runUpgradePTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	defer process.Close()
	if err := process.Resize(width, height); err != nil {
		t.Fatalf("resize PTY to %dx%d: %v", width, height, err)
	}
	var output lockedUpgradePTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("x\n")); err != nil {
		t.Fatalf("release PTY helper after sizing: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("wait PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading PTY output: %q", output.String())
	}
	return output.String()
}

func assertUpgradeRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}
	live := upgradePTYText(visible[enter:leave])
	for _, expected := range []string{"YCY / upgrade", "release update", "Consume startup", "Resolve release", "Resolve artifact", "STATE", "PHASE", "DETAIL"} {
		if !strings.Contains(live, expected) {
			t.Fatalf("Rich PTY live Console missing %q: %q", expected, output)
		}
	}
	if wide && !strings.Contains(live, "Complete") {
		t.Fatalf("wide Rich PTY live Console omitted the final phase: %q", output)
	}
	if wide && !strings.Contains(live, "detached updater") {
		t.Fatalf("wide Rich PTY omitted complete updater scope: %q", output)
	}
	if !wide && !strings.Contains(live, "detached") {
		t.Fatalf("compact Rich PTY omitted bounded updater scope: %q", output)
	}
	if strings.Contains(live, "FLOW") || strings.Contains(live, "[done]") || strings.Contains(live, "[active]") {
		t.Fatalf("Rich PTY live Console retained a non-B hierarchy: %q", output)
	}
	postLive := visible[leave:]
	resultStart := strings.LastIndex(postLive, "Updated ycy to v1.0.1.")
	if resultStart < 0 {
		t.Fatalf("Rich PTY durable result did not follow the Transcript: %q", output)
	}
	transcript := upgradePTYText(postLive[:resultStart])
	result := upgradePTYText(postLive[resultStart:])
	for _, expected := range []string{"Consume startup transaction (completed)", "Resolve release (completed)", "Resolve artifact (completed)", "Complete (completed)", "succeeded"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Rich PTY Transcript omitted %q: %q", expected, output)
		}
	}
	if strings.Index(transcript, "Updated ycy to v1.0.1.") < 0 {
		t.Fatalf("Rich PTY Transcript omitted prior transaction result: %q", output)
	}
	for _, expected := range []string{"Updated ycy to v1.0.1.", "Update to v2.0.0 has been scheduled and will finish after ycy exits."} {
		if !strings.Contains(result, expected) {
			t.Fatalf("Rich PTY durable result omitted %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "upgrade-secret") {
		t.Fatalf("Rich PTY output leaked a secret: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func upgradePTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

func upgradePTYEnvironmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := overrides[key]; !replaced {
				environment = append(environment, entry)
			}
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

type lockedUpgradePTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedUpgradePTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedUpgradePTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
