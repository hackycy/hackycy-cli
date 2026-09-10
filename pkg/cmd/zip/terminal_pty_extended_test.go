package zip

import (
	archivezip "archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunZIPRichPTYFourWayPlanningArchiveJourney(t *testing.T) {
	const helperEnvironment = "YCY_ZIP_EXTENDED_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runZIPExtendedRichPTYHelper(t)
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
			releasePath := filepath.Join(t.TempDir(), "reveal-release")
			enterPath := filepath.Join(t.TempDir(), "reveal-enter")
			command := exec.Command(os.Args[0], "-test.run=^TestRunZIPRichPTYFourWayPlanningArchiveJourney$")
			command.Env = zipExtendedPTYEnvironment(map[string]string{
				"NO_COLOR":                   map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                       "xterm-256color",
				helperEnvironment:            "1",
				"YCY_ZIP_EXTENDED_PTY_START": "1",
				"YCY_ZIP_EXTENDED_RELEASE":   releasePath,
				"YCY_ZIP_EXTENDED_ENTER":     enterPath,
			})
			output := runZIPExtendedPTYProcess(t, command, testCase.width, testCase.height, enterPath, releasePath, "success", "")
			assertZIPExtendedPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func TestRunZIPRichPTYCompletionBranches(t *testing.T) {
	const helperEnvironment = "YCY_ZIP_EXTENDED_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runZIPExtendedRichPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name          string
		scenario      string
		width, height uint16
		color         bool
	}{
		{name: "planning cancellation", scenario: "planning-cancellation", width: 120, height: 40, color: true},
		{name: "write failure no color compact", scenario: "write-failure", width: 40, height: 15, color: false},
		{name: "reveal warning", scenario: "reveal-warning", width: 120, height: 40, color: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			releasePath := filepath.Join(t.TempDir(), "reveal-release")
			enterPath := filepath.Join(t.TempDir(), "reveal-enter")
			cancelPath := filepath.Join(t.TempDir(), "cancel")
			command := exec.Command(os.Args[0], "-test.run=^TestRunZIPRichPTYCompletionBranches$")
			command.Env = zipExtendedPTYEnvironment(map[string]string{
				"NO_COLOR":                   map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                       "xterm-256color",
				helperEnvironment:            "1",
				"YCY_ZIP_EXTENDED_PTY_START": "1",
				"YCY_ZIP_EXTENDED_RELEASE":   releasePath,
				"YCY_ZIP_EXTENDED_ENTER":     enterPath,
				"YCY_ZIP_EXTENDED_CANCEL":    cancelPath,
				"YCY_ZIP_EXTENDED_SCENARIO":  testCase.scenario,
			})
			output := runZIPExtendedPTYProcess(t, command, testCase.width, testCase.height, enterPath, releasePath, testCase.scenario, cancelPath)
			assertZIPCompletionBranchPTYOutput(t, output, testCase.scenario, testCase.color)
		})
	}
}

func runZIPExtendedRichPTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_ZIP_EXTENDED_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	releasePath := os.Getenv("YCY_ZIP_EXTENDED_RELEASE")
	if releasePath == "" {
		t.Fatal("missing zip reveal release path")
	}
	enterPath := os.Getenv("YCY_ZIP_EXTENDED_ENTER")
	if enterPath == "" {
		t.Fatal("missing zip reveal enter path")
	}
	scenario := os.Getenv("YCY_ZIP_EXTENDED_SCENARIO")
	if scenario == "" {
		scenario = "success"
	}
	root := t.TempDir()
	writeZIPExtendedFile(t, root, "package.json", `{"name":"demo-project","workspaces":["packages/*"],"devDependencies":{"vite":"1"}}`)
	writeZIPExtendedFile(t, filepath.Join(root, "packages", "extra"), "package.json", `{"name":"extra-package"}`)
	writeZIPExtendedFile(t, filepath.Join(root, "dist"), "index.html", "<main>ok</main>")
	writeZIPExtendedFile(t, filepath.Join(root, "dist"), "app.js", "console.log('ok')")
	writeZIPExtendedFile(t, filepath.Join(root, "dist"), ".hidden", "must not archive")
	if scenario == "write-failure" {
		if err := os.Mkdir(filepath.Join(root, "dist", "demo-project.zip"), 0o700); err != nil {
			t.Fatalf("create archive write failure target: %v", err)
		}
	}

	revealName := "open"
	if runtime.GOOS == "linux" {
		revealName = "xdg-open"
	}
	bin := t.TempDir()
	revealPath := filepath.Join(bin, revealName)
	revealExit := "exit 0"
	if scenario == "reveal-warning" {
		revealExit = "exit 1"
	}
	revealScript := "#!/bin/sh\ntouch \"$YCY_ZIP_EXTENDED_ENTER\"\nwhile [ ! -f \"$YCY_ZIP_EXTENDED_RELEASE\" ]; do sleep 0.01; done\n" + revealExit + "\n"
	if err := os.WriteFile(revealPath, []byte(revealScript), 0o700); err != nil {
		t.Fatalf("write reveal stub: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

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
	ctx := context.Background()
	var cancel context.CancelFunc
	var cancelDone chan struct{}
	if scenario == "planning-cancellation" {
		ctx, cancel = context.WithCancel(ctx)
		cancelDone = make(chan struct{})
		cancelPath := os.Getenv("YCY_ZIP_EXTENDED_CANCEL")
		if cancelPath == "" {
			t.Fatal("missing zip cancellation path")
		}
		go func() {
			defer close(cancelDone)
			for {
				if _, err := os.Stat(cancelPath); err == nil {
					cancel()
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}
	err := runZIP(&Options{
		Context:   ctx,
		Directory: root,
		Open:      true,
		WithDir:   "bundle",
		Terminal:  experience,
	})
	if cancel != nil {
		cancel()
		<-cancelDone
	}
	if scenario == "planning-cancellation" {
		if err != nil {
			t.Fatalf("runZIP() cancellation error = %v", err)
		}
		_, _ = fmt.Fprintln(os.Stderr, "ZIP_CANCELLED_OK")
		return
	}
	if err != nil {
		t.Fatalf("runZIP() error = %v", err)
	}
	if scenario == "write-failure" {
		info, statErr := os.Stat(filepath.Join(root, "dist", "demo-project.zip"))
		if statErr != nil || !info.IsDir() {
			t.Fatalf("archive write failure target = (%#v, %v)", info, statErr)
		}
		_, _ = fmt.Fprintln(os.Stderr, "ZIP_WRITE_FAILURE_OK")
		return
	}
	archivePath := filepath.Join(root, "dist", "demo-project.zip")
	archive, err := archivezip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("open generated archive: %v", err)
	}
	entries := make(map[string]string, len(archive.File))
	for _, file := range archive.File {
		reader, openErr := file.Open()
		if openErr != nil {
			_ = archive.Close()
			t.Fatalf("open archive entry %q: %v", file.Name, openErr)
		}
		contents, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil {
			_ = archive.Close()
			t.Fatalf("read archive entry %q: %v", file.Name, readErr)
		}
		entries[file.Name] = string(contents)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close generated archive: %v", err)
	}
	want := map[string]string{
		"bundle/index.html": "<main>ok</main>",
		"bundle/app.js":     "console.log('ok')",
	}
	if len(entries) != len(want) {
		t.Fatalf("archive entries = %#v, want %#v", entries, want)
	}
	for name, contents := range want {
		if entries[name] != contents {
			t.Fatalf("archive entry %q = %q, want %q", name, entries[name], contents)
		}
	}
	if _, found := entries["bundle/.hidden"]; found {
		t.Fatalf("archive included hidden entry: %#v", entries)
	}
	_, _ = fmt.Fprintln(os.Stderr, "ZIP_ARCHIVE_OK")
}

func writeZIPExtendedFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runZIPExtendedPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, enterPath, releasePath, scenario, cancelPath string) string {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start zip PTY helper: %v", err)
	}
	defer process.Close()
	if err := process.Resize(width, height); err != nil {
		t.Fatalf("resize PTY to %dx%d: %v", width, height, err)
	}
	var output zipPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("go\n")); err != nil {
		t.Fatalf("release zip PTY helper after sizing: %v", err)
	}
	if scenario == "planning-cancellation" {
		waitForZIPPTYText(t, &output, "Select a package")
		if err := os.WriteFile(cancelPath, []byte("cancel"), 0o600); err != nil {
			t.Fatalf("request zip planning cancellation: %v", err)
		}
	} else {
		compact := width < 70
		advanceZIPSelect(t, &output, process, "Select a package", compact)
		advanceZIPSelect(t, &output, process, "Select a directory", compact)
		advanceZIPSelect(t, &output, process, "Select file patterns", compact)
		// At 40x15 the compact root can clip the active input row entirely. The
		// state machine is still waiting on that form, so a short settling window
		// is the stable synchronization point for the compact case.
		if width >= 70 {
			waitForZIPPTYText(t, &output, "text input")
		} else {
			time.Sleep(200 * time.Millisecond)
		}
		if _, err := process.Terminal().Write([]byte("\r")); err != nil {
			t.Fatalf("submit zip output name: %v", err)
		}
		if scenario != "write-failure" {
			waitForZIPFile(t, enterPath)
			if err := os.WriteFile(releasePath, []byte("ok"), 0o600); err != nil {
				t.Fatalf("release zip reveal: %v", err)
			}
		}
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("wait zip PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close zip PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading zip PTY output: %q", output.String())
	}
	return output.String()
}

func assertZIPCompletionBranchPTYOutput(t *testing.T, output, scenario string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.LastIndex(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("zip Rich PTY did not restore primary screen: %q", output)
	}
	transcript := visible[leave:]
	var expected []string
	switch scenario {
	case "planning-cancellation":
		expected = []string{"Discover workspace", "cancelled", "Archive planning cancelled", "ZIP_CANCELLED_OK"}
		if strings.Contains(transcript, "Done!") {
			t.Fatalf("zip cancellation produced synthetic success output: %q", output)
		}
	case "write-failure":
		expected = []string{"Write archive", "failed", "Archive failed (write)", "Failed to write zip (write).", "ZIP_WRITE_FAILURE_OK"}
		if strings.Contains(transcript, "Done!") {
			t.Fatalf("zip write failure produced synthetic success output: %q", output)
		}
	case "reveal-warning":
		expected = []string{"Reveal archive", "succeeded", "Archive complete", "Archive created", "Archive created; host reveal unavailable", "Archive ready", "Done!", "ZIP_ARCHIVE_OK"}
	default:
		t.Fatalf("unknown zip completion scenario %q", scenario)
	}
	last := 0
	for _, value := range expected {
		next := strings.Index(transcript[last:], value)
		if next < 0 {
			t.Fatalf("zip %s Transcript missing %q: %q", scenario, value, output)
		}
		last += next + len(value)
	}
	for _, forbidden := range []string{"/private/", "token=secret", "https://"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("zip %s output leaked %q: %q", scenario, forbidden, output)
		}
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR zip %s output contains %q: %q", scenario, prefix, output)
			}
		}
	}
}

func advanceZIPSelect(t *testing.T, output *zipPTYBuffer, process *terminaltest.PTYProcess, prompt string, compact bool) {
	t.Helper()
	if compact {
		// The complete Form Catalog can clip the active Huh row at 40x15, but
		// the form state remains ready for the same two Enter submissions.
		time.Sleep(200 * time.Millisecond)
	} else {
		waitForZIPPTYText(t, output, prompt)
	}
	if _, err := process.Terminal().Write([]byte("\r")); err != nil {
		t.Fatalf("submit zip selection %q: %v", prompt, err)
	}
	// Huh's searchable Select/MultiSelect commits its filter on the first
	// Enter and submits the form on the second.
	time.Sleep(100 * time.Millisecond)
	if _, err := process.Terminal().Write([]byte("\r")); err != nil {
		t.Fatalf("finish zip selection %q: %v", prompt, err)
	}
}

func assertZIPExtendedPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	for _, form := range []string{"Workspace package", "Source directory", "File patterns", "Output name"} {
		index := strings.Index(visible, form)
		if index < 0 {
			t.Fatalf("zip Rich PTY Form Catalog did not render %q: %q", form, output)
		}
		if wide {
			firstPrompt := strings.Index(visible, "Select a package to zip:")
			if firstPrompt < 0 || index >= firstPrompt {
				t.Fatalf("zip Rich PTY Form Catalog did not render %q before the first interaction: %q", form, output)
			}
		}
	}
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("zip Rich PTY did not restore primary screen: %q", output)
	}
	live := zipExtendedPTYText(visible[enter:leave])
	liveExpected := []string{"YCY / zip", "STATE", "PHASE", "DETAIL"}
	if wide {
		liveExpected = append(liveExpected,
			"Discover workspace", "Select source", "Select patterns", "Prepare archive",
			"Collect files", "Compress files", "Write archive", "Reveal archive")
	} else {
		liveExpected = append(liveExpected, "Workspace package", "Source directory", "File patterns", "Output name")
	}
	for _, expected := range liveExpected {
		if !strings.Contains(live, expected) {
			t.Fatalf("zip Rich PTY live Console missing %q: %q", expected, output)
		}
	}
	if wide {
		for _, expected := range []string{"Plan and publish a bounded archive", "Zip Directory", "with-dir enabled", "reveal enabled"} {
			if !strings.Contains(live, expected) {
				t.Fatalf("wide zip Rich PTY omitted descriptor context %q: %q", expected, output)
			}
		}
	} else if !strings.Contains(live, "Zip Directory") && !strings.Contains(live, "directory") {
		t.Fatalf("compact zip Rich PTY omitted bounded metadata: %q", output)
	}
	outputExpected := []string{"ZIP_ARCHIVE_OK", "Archive complete", "Archive created", "Collected 2; included 2; output demo-project.zip", "Archive ready", "Done!"}
	if wide {
		outputExpected = append(outputExpected, "Select a package", "Select a directory", "Select file patterns", "Enter the name")
	} else {
		// The compact active region can clip prompt labels; the durable
		// Transcript retains each safe answer instead.
		outputExpected = append(outputExpected, "Workspace package", "Source directory", "File patterns", "Archive output", "demo-project")
	}
	for _, expected := range outputExpected {
		if !strings.Contains(visible, expected) {
			t.Fatalf("zip Rich PTY output missing %q: %q", expected, output)
		}
	}
	for _, expected := range []string{"Discover workspace", "Select source", "Select patterns", "Prepare archive", "Collect files", "Compress files", "Write archive", "Reveal archive"} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("zip Rich PTY output missing phase %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "/private/") || strings.Contains(output, "https://") || strings.Contains(output, "must not archive") {
		t.Fatalf("zip Rich PTY leaked unsafe archive context: %q", output)
	}
	transcript := visible[leave:]
	ordered := []string{
		"Discover workspace",
		"Select source",
		"Select patterns",
		"Prepare archive",
		"Collect files",
		"Compress files",
		"Write archive",
		"Reveal archive",
		"succeeded",
		"Archive complete",
		"Archive created",
		"Collected 2; included 2; output demo-project.zip",
		"Archive ready",
		"Done!",
	}
	last := 0
	for _, expected := range ordered {
		next := strings.Index(transcript[last:], expected)
		if next < 0 {
			t.Fatalf("zip Rich PTY Transcript missing ordered event %q: %q", expected, output)
		}
		last += next + len(expected)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR zip Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func zipExtendedPTYEnvironment(overrides map[string]string) []string {
	ignored := map[string]struct{}{
		"CI": {}, "CLICOLOR": {}, "CLICOLOR_FORCE": {}, "COLORTERM": {}, "NO_COLOR": {}, "TERM": {},
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, skip := ignored[key]; skip {
				continue
			}
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

func waitForZIPPTYText(t *testing.T, output *zipPTYBuffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if strings.Contains(output.String(), needle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for zip PTY text %q: %q", needle, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForZIPFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for zip marker %q", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func zipExtendedPTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

type zipPTYBuffer struct {
	mu    sync.Mutex
	value strings.Builder
}

func (buffer *zipPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.value.Write(value)
}

func (buffer *zipPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.value.String()
}
