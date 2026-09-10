package heat

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	"golang.org/x/term"
)

func TestRunGitHeatRichPTYUsesBConsoleAndRestoresPrimaryScreen(t *testing.T) {
	const helperEnvironment = "YCY_GIT_HEAT_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runGitHeatRichPTYHelper(t)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunGitHeatRichPTYUsesBConsoleAndRestoresPrimaryScreen$")
			command.Env = gitHeatEnvironmentWith(map[string]string{
				"NO_COLOR":               map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                   "xterm-256color",
				helperEnvironment:        "1",
				"YCY_GIT_HEAT_PTY_START": "1",
			})
			output := runGitHeatPTYProcess(t, command, testCase.width, testCase.height)
			assertGitHeatRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runGitHeatRichPTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_GIT_HEAT_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}

	root := t.TempDir()
	gitScript := filepath.Join(root, "git-fixture.sh")
	const script = `#!/bin/sh
sleep 0.08
if [ "$1" = "rev-parse" ]; then
  printf '%s\n' "$HEAT_REPOSITORY"
  exit 0
fi
printf '\000__HACKYCY_HEAT_COMMIT__abc\0371704067200\0372024-01-01 00:00:00 +0000\000M\000src/main.go\000A\000README.md\000'
`
	if err := os.WriteFile(gitScript, []byte(script), 0o700); err != nil {
		t.Fatalf("write Git fixture: %v", err)
	}
	if err := os.Setenv("HEAT_REPOSITORY", filepath.Join(root, "repo")); err != nil {
		t.Fatalf("set Git fixture repository: %v", err)
	}
	defer os.Unsetenv("HEAT_REPOSITORY")

	color := os.Getenv("NO_COLOR") == ""
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		width = 80
	}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: color},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: color},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	err = runHeat(&Options{
		Context:      context.Background(),
		Target:       TargetFiles,
		Sort:         SortPath,
		RelativeTime: true,
		Query:        "main",
		Width:        width,
		Terminal:     experience,
		Git:          &gitprocess.Runner{Executable: gitScript},
		Now:          func() time.Time { return time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("runHeat() error = %v", err)
	}
}

func runGitHeatPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
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

	var output lockedGitHeatPTYBuffer
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

func assertGitHeatRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	enter := strings.Index(output, "\x1b[?1049h")
	leave := strings.LastIndex(output, "\x1b[?1049l")
	if strings.Count(output, "\x1b[?1049h") != 1 || strings.Count(output, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}

	live := gitHeatPTYText(output[enter:leave])
	for _, needle := range []string{
		"YCY / git heat",
		"range last 20 commits",
		"Locate Git repository",
		"Read Git history",
		"Rank hot paths",
		"Locating repository",
		"Reading last 20 commits",
		"DONE",
	} {
		if !strings.Contains(live, needle) {
			t.Fatalf("Rich PTY live Console omitted %q: %q", needle, output)
		}
	}
	if wide {
		for _, needle := range []string{"sort path", "relative time", "file heat", "SUCCEEDED"} {
			if !strings.Contains(live, needle) {
				t.Fatalf("Rich PTY wide Console omitted %q: %q", needle, output)
			}
		}
	} else if !strings.Contains(live, "sort") || !strings.Contains(live, "path") {
		t.Fatalf("Rich PTY compact Console omitted bounded sort context: %q", output)
	}
	if wide && !strings.Contains(live, "file heat") {
		t.Fatalf("Rich PTY wide target context omitted: %q", output)
	}
	state := strings.Index(live, "STATE")
	phase := strings.Index(live, "PHASE")
	detail := strings.Index(live, "DETAIL")
	if state < 0 || phase < state || detail < phase {
		t.Fatalf("Rich PTY B table heading order = %q", output)
	}

	postLive := output[leave:]
	resultStart := strings.Index(postLive, "YCY / git heat")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result did not start after the Transcript: %q", output)
	}
	transcript := gitHeatPTYText(postLive[:resultStart])
	result := gitHeatPTYText(postLive[resultStart:])
	for _, needle := range []string{
		"Locate Git repository (completed): Repository located",
		"Read Git history (completed): Read 1 commits",
		"Rank hot paths (completed): Ranked 2 files",
		"succeeded: Ranked 2 files from last 20 commits",
	} {
		if !strings.Contains(transcript, needle) {
			t.Fatalf("Rich PTY Transcript omitted %q: %q", needle, output)
		}
	}
	for _, forbidden := range []string{"src/main.go", "README.md", "main", "Legend", "HEAT_REPOSITORY", "/repo"} {
		if strings.Contains(transcript, forbidden) {
			t.Fatalf("Rich PTY Transcript leaked %q: %q", forbidden, output)
		}
	}
	for _, needle := range []string{"Repository heat", "src/", "main", "README.md", "Legend: latest", "▲ latest", "Changed at"} {
		if !strings.Contains(result, needle) {
			t.Fatalf("Rich PTY durable result omitted %q: %q", needle, output)
		}
	}
	if wide {
		if !strings.Contains(result, "File") || !strings.Contains(result, "M A D R C") {
			t.Fatalf("Rich PTY wide result omitted File column: %q", output)
		}
	} else {
		for _, needle := range []string{"Changes:", "File:", "Range:"} {
			if !strings.Contains(result, needle) {
				t.Fatalf("Rich PTY compact result omitted %q: %q", needle, output)
			}
		}
	}
	if color {
		if !strings.Contains(output, "\x1b[38") {
			t.Fatalf("color Rich PTY omitted B styling: %q", output)
		}
		return
	}
	for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
		if strings.Contains(output, prefix) {
			t.Fatalf("NO_COLOR Rich PTY contains %q: %q", prefix, output)
		}
	}
}

func TestRunGitHeatStreamsPreservePlainAutomationAndFailureBoundaries(t *testing.T) {
	script := writeGitHeatFixtureScript(t, false)
	for _, testCase := range []struct {
		name       string
		mode       terminalexperience.InteractionMode
		wantOutput string
		wantDiag   string
	}{
		{name: "plain", mode: terminalexperience.PlainInteractive, wantOutput: "HACKYCY CLI\nfixture | last 20 commits | 1 file\n#\tChanged at\tM A D R C\tFile\n1 (latest)\t2024-01-01 00:00:00\tM - - - -\tsrc/main.go\nLegend: latest, earliest, M modified, A added, D deleted, R renamed, C copied\n", wantDiag: "Locating repository...\nReading last 20 commits...\nRanking files by path...\n"},
		{name: "automation", mode: terminalexperience.Automation, wantOutput: "HACKYCY CLI\nfixture | last 20 commits | 1 file\n#\tChanged at\tM A D R C\tFile\n1 (latest)\t2024-01-01 00:00:00\tM - - - -\tsrc/main.go\nLegend: latest, earliest, M modified, A added, D deleted, R renamed, C copied\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: testCase.mode},
				Output:       &output,
				Diagnostics:  &diagnostics,
			})
			runner := &gitprocess.Runner{Executable: script}
			err := runHeat(&Options{Context: context.Background(), Target: TargetFiles, Sort: SortPath, Terminal: experience, Git: runner, Now: func() time.Time { return time.Date(2024, time.January, 1, 0, 0, 1, 0, time.UTC) }})
			if err != nil {
				t.Fatalf("runHeat() error = %v", err)
			}
			if output.String() != testCase.wantOutput || diagnostics.String() != testCase.wantDiag {
				t.Fatalf("streams = output %q, diagnostics %q", output.String(), diagnostics.String())
			}
			if terminaltest.ContainsTerminalControl(output.Bytes()) || terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
				t.Fatalf("non-rich streams contain terminal control: output=%q diagnostics=%q", output.String(), diagnostics.String())
			}
		})
	}

	failingScript := writeGitHeatFixtureScript(t, true)
	var output, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation}, Output: &output, Diagnostics: &diagnostics})
	if err := runHeat(&Options{Context: context.Background(), Target: TargetFiles, Sort: SortPath, Terminal: experience, Git: &gitprocess.Runner{Executable: failingScript}, Now: time.Now}); err == nil || err.Error() != "not a repository" {
		t.Fatalf("runHeat() failure = %v, want not a repository", err)
	}
	if output.Len() != 0 || diagnostics.Len() != 0 {
		t.Fatalf("failed Automation streams = output %q, diagnostics %q", output.String(), diagnostics.String())
	}
}

func writeGitHeatFixtureScript(t *testing.T, fail bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git-fixture.sh")
	body := `#!/bin/sh
if [ "$1" = "rev-parse" ]; then
  printf 'fixture\n'
  exit 0
fi
printf '\000__HACKYCY_HEAT_COMMIT__abc\0371704067200\0372024-01-01 00:00:00 +0000\000M\000src/main.go\000'
`
	if fail {
		body = `#!/bin/sh
printf 'not a repository\n' >&2
exit 1
`
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write Git fixture: %v", err)
	}
	return path
}

func gitHeatPTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

func gitHeatEnvironmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

type lockedGitHeatPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedGitHeatPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedGitHeatPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
