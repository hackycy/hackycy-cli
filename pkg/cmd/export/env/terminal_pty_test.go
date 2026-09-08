package env

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
)

func TestRunExportEnvRichPTYUsesBConsoleAcrossLayouts(t *testing.T) {
	const helperEnvironment = "YCY_EXPORT_ENV_RICH_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runExportEnvRichPTYHelper(t)
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
			command := exec.Command(os.Args[0], "-test.run=^TestRunExportEnvRichPTYUsesBConsoleAcrossLayouts$")
			command.Env = exportEnvPTYEnvironment(map[string]string{
				"NO_COLOR":                 map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                     "xterm-256color",
				helperEnvironment:          "1",
				"YCY_EXPORT_ENV_PTY_START": "1",
			})
			output := runExportEnvPTYProcess(t, command, testCase.width, testCase.height)
			assertExportEnvRichPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func TestRunExportEnvRichPTYAlternatesOneWorkCatalogWithDeclaredForm(t *testing.T) {
	const helperEnvironment = "YCY_EXPORT_ENV_RICH_FORMS_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runExportEnvRichFormsPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name          string
		width, height uint16
		color         bool
	}{
		{name: "wide color", width: 120, height: 40, color: true},
		{name: "compact no color", width: 40, height: 15, color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRunExportEnvRichPTYAlternatesOneWorkCatalogWithDeclaredForm$")
			command.Env = exportEnvPTYEnvironment(map[string]string{
				"NO_COLOR":                 map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                     "xterm-256color",
				helperEnvironment:          "1",
				"YCY_EXPORT_ENV_PTY_START": "1",
			})
			output := runExportEnvFormsPTYProcess(t, command, testCase.width, testCase.height)
			assertExportEnvRichFormsPTYOutput(t, output, testCase.color)
		})
	}
}

func runExportEnvRichPTYHelper(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(root+string(os.PathSeparator)+".env", []byte("BASE=base\nSECRET=do-not-project\n"), 0o600); err != nil {
		t.Fatalf("write base environment: %v", err)
	}
	if err := os.WriteFile(root+string(os.PathSeparator)+".env.production", []byte("VALUE=production\n"), 0o600); err != nil {
		t.Fatalf("write selected environment: %v", err)
	}
	if os.Getenv("YCY_EXPORT_ENV_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}

	color := os.Getenv("NO_COLOR") == ""
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
	err := runEnv(&Options{
		Context:          context.Background(),
		Directory:        ".",
		Environment:      "production",
		Merge:            true,
		WorkingDirectory: func() (string, error) { return root, nil },
		Terminal:         experience,
		Reader:           delayedExportEnvReader{delay: 40 * time.Millisecond},
		Writer:           osExportEnvWriter{},
	})
	if err != nil {
		t.Fatalf("runEnv() error = %v", err)
	}
}

func runExportEnvRichFormsPTYHelper(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for name, contents := range map[string]string{
		".env":            "BASE=base\nSECRET=do-not-project\n",
		".env.local":      "VALUE=local\n",
		".env.production": "VALUE=production\n",
	} {
		if err := os.WriteFile(root+string(os.PathSeparator)+name, []byte(contents), 0o600); err != nil {
			t.Fatalf("write environment file %s: %v", name, err)
		}
	}
	if os.Getenv("YCY_EXPORT_ENV_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}

	color := os.Getenv("NO_COLOR") == ""
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
	err := runEnv(&Options{
		Context:   context.Background(),
		Directory: ".",
		Merge:     true,
		WorkingDirectory: func() (string, error) {
			time.Sleep(80 * time.Millisecond)
			return root, nil
		},
		Terminal: experience,
		Reader:   delayedExportEnvReader{delay: 40 * time.Millisecond},
		Writer:   osExportEnvWriter{},
	})
	if err != nil {
		t.Fatalf("runEnv() error = %v", err)
	}
}

type delayedExportEnvReader struct {
	delay time.Duration
}

func (reader delayedExportEnvReader) ReadFile(path string) ([]byte, error) {
	time.Sleep(reader.delay)
	return os.ReadFile(path)
}

func runExportEnvPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
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

	var output lockedExportEnvPTYBuffer
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

func runExportEnvFormsPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16) string {
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

	var output lockedExportEnvPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("x\n")); err != nil {
		t.Fatalf("release PTY helper after sizing: %v", err)
	}
	waitForExportEnvPTYText(t, &output, "Select environment")
	if _, err := process.Terminal().Write([]byte("\r")); err != nil {
		t.Fatalf("submit environment selection: %v", err)
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

func waitForExportEnvPTYText(t *testing.T, output *lockedExportEnvPTYBuffer, expected string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if strings.Contains(exportEnvPTYText(output.String()), expected) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %q in PTY output: %q", expected, output.String())
		case <-tick.C:
		}
	}
}

func assertExportEnvRichPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	enter := strings.Index(output, "\x1b[?1049h")
	leave := strings.LastIndex(output, "\x1b[?1049l")
	if strings.Count(output, "\x1b[?1049h") != 1 || strings.Count(output, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("Rich PTY output did not restore the primary screen: %q", output)
	}

	live := exportEnvPTYText(output[enter:leave])
	for _, needle := range []string{
		"YCY / export env",
		"environment",
		"Resolve directory",
		"Discover environment files",
		"Read selected files",
		"Parse and merge values",
		"Encode JSON",
		"STATE",
		"PHASE",
		"DETAIL",
		"DONE",
	} {
		if !strings.Contains(live, needle) {
			t.Fatalf("Rich PTY live Console omitted %q: %q", needle, output)
		}
	}
	if wide && !strings.Contains(live, "Export environment") {
		t.Fatalf("wide Rich PTY omitted operation title: %q", output)
	}
	if wide && !strings.Contains(live, "environment JSON") {
		t.Fatalf("wide Rich PTY omitted complete target context: %q", output)
	}
	if strings.Contains(live, "FLOW") || strings.Contains(live, "[done]") || strings.Contains(live, "[active]") {
		t.Fatalf("Rich PTY live Console retained a non-B hierarchy: %q", output)
	}

	postLive := output[leave:]
	resultStart := strings.Index(postLive, "Exported variables:")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result did not start after the Transcript: %q", output)
	}
	transcript := exportEnvPTYText(postLive[:resultStart])
	result := exportEnvPTYText(postLive[resultStart:])
	for _, needle := range []string{
		"Resolve directory (completed)",
		"Discover environment files (completed)",
		"Read selected files (completed)",
		"Parse and merge values (completed)",
		"Encode JSON (completed)",
		"Selected environment: production",
		"Exported 3 variables",
		"succeeded",
	} {
		if !strings.Contains(transcript, needle) {
			t.Fatalf("Rich PTY Transcript omitted %q: %q", needle, output)
		}
	}
	if strings.Contains(transcript, "do-not-project") || strings.Contains(transcript, "BASE=base") {
		t.Fatalf("Rich PTY Transcript leaked environment content: %q", output)
	}
	for _, needle := range []string{"Exported variables:", `"SECRET": "do-not-project"`, `"VALUE": "production"`} {
		if !strings.Contains(result, needle) {
			t.Fatalf("Rich PTY durable result omitted %q: %q", needle, output)
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

func assertExportEnvRichFormsPTYOutput(t *testing.T, output string, color bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter {
		t.Fatalf("Rich PTY did not restore the primary screen: %q", output)
	}
	live := exportEnvPTYText(visible[enter:leave])
	firstWork := strings.Index(live, "Discover environment files")
	form := strings.LastIndex(live, "Select environment")
	lastWork := strings.LastIndex(live, "Read selected files")
	if firstWork < 0 || form < 0 || lastWork < 0 || !(firstWork < form && form < lastWork) {
		t.Fatalf("Rich PTY did not preserve Work/Form/Work order: %q", output)
	}
	for _, expected := range []string{
		"Resolve directory",
		"Discover environment files",
		"Select environment",
		"Read selected files",
		"Parse and merge values",
		"Encode JSON",
	} {
		if !strings.Contains(live, expected) {
			t.Fatalf("Rich PTY journey omitted %q: %q", expected, output)
		}
	}
	postLive := visible[leave:]
	resultStart := strings.Index(postLive, "Exported variables:")
	if resultStart < 0 {
		t.Fatalf("Rich PTY result did not follow the Transcript: %q", output)
	}
	transcript := exportEnvPTYText(postLive[:resultStart])
	result := exportEnvPTYText(postLive[resultStart:])
	for _, expected := range []string{
		"Resolve directory (completed)",
		"Discover environment files (completed)",
		"Selected environment: local",
		"Read selected files (completed)",
		"Parse and merge values (completed)",
		"Encode JSON (completed)",
		"Exported 3 variables",
		"succeeded: Exported 3 variables",
	} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("Rich PTY Transcript omitted %q: %q", expected, output)
		}
	}
	if strings.Contains(transcript, "do-not-project") || strings.Contains(transcript, "BASE=base") {
		t.Fatalf("Rich PTY Transcript leaked environment content: %q", output)
	}
	for _, expected := range []string{"Exported variables:", `"SECRET": "do-not-project"`, `"VALUE": "local"`} {
		if !strings.Contains(result, expected) {
			t.Fatalf("Rich PTY durable result omitted %q: %q", expected, output)
		}
	}
	if color && !strings.Contains(output, "\x1b[38") {
		t.Fatalf("colored Rich PTY omitted B styling: %q", output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("no-color Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func exportEnvPTYText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.Join(strings.Fields(terminaltest.StripANSI(value)), " ")
}

func exportEnvPTYEnvironment(overrides map[string]string) []string {
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

type lockedExportEnvPTYBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (buffer *lockedExportEnvPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(value)
}

func (buffer *lockedExportEnvPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}
