package terminal_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestOpenConsoleRichStartupIsEager(t *testing.T) {
	const helperEnvironment = "YCY_TERMINAL_EAGER_CONSOLE_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runEagerConsoleHelper(t)
		return
	}

	command := exec.Command(os.Args[0], "-test.run=^TestOpenConsoleRichStartupIsEager$")
	command.Env = append(os.Environ(), helperEnvironment+"=1", "TERM=xterm-256color")
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start PTY helper: %v", err)
	}
	var output bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if err := process.Wait(); err != nil {
		t.Fatalf("wait PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close PTY helper: %v", err)
	}
	<-readDone
	text := output.String()
	if !strings.Contains(text, "EAGER_RICH_OK") || !strings.Contains(text, "Workspace") || !strings.Contains(text, "Confirm") || !strings.Contains(text, "\x1b[?1049h") {
		t.Fatalf("OpenConsole did not eagerly render Rich catalog: %q", text)
	}
}

func runEagerConsoleHelper(t *testing.T) {
	t.Helper()
	runtime := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{
			Interaction: terminal.RichInteractive,
			Stdin:       terminal.StreamCapability{Terminal: true},
			Stdout:      terminal.StreamCapability{Terminal: true, Color: true},
			Stderr:      terminal.StreamCapability{Terminal: true, Color: true},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	run, err := runtime.OpenConsole(context.Background(), terminal.ConsoleDescriptor{
		Command: "YCY / config",
		FormCatalog: []terminal.ConsoleFormStep{
			{ID: "workspace", Name: "Workspace", Detail: "choose project"},
			{ID: "confirm", Name: "Confirm", Detail: "apply changes"},
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stdout, "EAGER_RICH_ERROR", err)
		return
	}
	if err := run.Close(); err != nil {
		t.Fatalf("eager Rich Close() error = %v", err)
	}
	fmt.Fprintln(os.Stdout, "EAGER_RICH_OK")
}
