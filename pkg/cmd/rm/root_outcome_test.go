package rm_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	rootcommand "github.com/hackycy/hackycy-cli/pkg/cmd/root"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestRMAutomationErrorUsesStderrWithoutPartialCommandResult(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.txt")
	if err := os.WriteFile(target, []byte("contents"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			In:     panicReader{},
			Out:    stdout,
			ErrOut: stderr,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
	factory.Logging = logging.NewRuntime(logging.Options{Writer: stderr})
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"rm", filepath.Base(target)})
	if outcome.Code != 1 || outcome.Err == nil || outcome.Err.Error() != "rm requires an interactive terminal" || stdout.Len() != 0 || stderr.String() != "error: rm requires an interactive terminal\n" {
		t.Fatalf("Automation outcome = %#v, streams = (%q, %q)", outcome, stdout.String(), stderr.String())
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
		t.Fatalf("Automation streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("Automation failure changed target: %v", err)
	}
}

func TestRootConfiguresDiagnosticsBeforeRM(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    stdout,
			ErrOut: stderr,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
	runtime := logging.NewRuntime(logging.Options{Writer: stderr})
	factory.Logging = runtime
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "rm", "missing"})
	if outcome.Code != 0 || outcome.Err != nil || runtime.Level() != logging.Warn {
		t.Fatalf("rm outcome = %#v, level = %v, stderr = %q", outcome, runtime.Level(), stderr.String())
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("rm attempted to read Automation input")
}
