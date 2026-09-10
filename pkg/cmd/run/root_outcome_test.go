package run_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	rootcommand "github.com/hackycy/hackycy-cli/pkg/cmd/root"
	runcommand "github.com/hackycy/hackycy-cli/pkg/cmd/run"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestRunAutomationFailsBeforeChildStartupOrInputRead(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writeRunOutcomeFile(t, project, "package.json", `{"scripts":{"check":"echo check"}}`)
	startedPath := filepath.Join(root, "started")
	binDirectory := filepath.Join(root, "bin")
	manager := filepath.Join(binDirectory, string(runcommand.PackageManagerExternal))
	if err := os.MkdirAll(binDirectory, 0o700); err != nil {
		t.Fatalf("create manager directory: %v", err)
	}
	if err := os.WriteFile(manager, []byte("#!/bin/sh\ntouch \"$RUN_STARTED\"\n"), 0o700); err != nil {
		t.Fatalf("write manager fixture: %v", err)
	}
	t.Setenv("PATH", binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RUN_STARTED", startedPath)
	withRunOutcomeWorkingDirectory(t, project)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app, _ := newRunRootApp(t, panicRunReader{}, stdout, stderr, terminal.Capabilities{Interaction: terminal.Automation})
	outcome := app.Execute(context.Background(), []string{"run"})
	if outcome.Code != 1 || outcome.Err == nil || outcome.Err.Error() != "run requires an interactive terminal" || stdout.Len() != 0 || stderr.String() != "error: run requires an interactive terminal\n" {
		t.Fatalf("Automation outcome = %#v, streams = (%q, %q)", outcome, stdout.String(), stderr.String())
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
		t.Fatalf("Automation streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(startedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Automation failure launched child: %v", err)
	}
}

func TestRootConfiguresDiagnosticsForRunAfterLeafArgumentValidation(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app, factory := newRunRootApp(t, strings.NewReader(""), stdout, stderr, terminal.Capabilities{Interaction: terminal.Automation})

	outcome := app.Execute(context.Background(), []string{"run", "one", "two", "--log-level", "invalid"})
	if outcome.Code != 1 || outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "accepts at most 1 arg(s)") || strings.Contains(stderr.String(), "invalid log level") {
		t.Fatalf("invalid arguments outcome = %#v, stderr = %q", outcome, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	outcome = app.Execute(context.Background(), []string{"--log-level", "warn", "run", "missing"})
	if outcome.Code != 1 || outcome.Err == nil || factory.Logging.Level() != logging.Warn {
		t.Fatalf("diagnostic outcome = %#v, level = %v, stderr = %q", outcome, factory.Logging.Level(), stderr.String())
	}
}

func TestRootMapsRunChildExitWithoutDiagnostic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix shell fixture")
	}
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writeRunOutcomeFile(t, project, "package.json", `{"scripts":{"check":"echo check"}}`)
	writeRunOutcomeFile(t, project, "b"+"un"+".lock", "")
	binDirectory := filepath.Join(root, "bin")
	manager := filepath.Join(binDirectory, string(runcommand.PackageManagerExternal))
	if err := os.MkdirAll(binDirectory, 0o700); err != nil {
		t.Fatalf("create manager directory: %v", err)
	}
	if err := os.WriteFile(manager, []byte("#!/bin/sh\nexit 7\n"), 0o700); err != nil {
		t.Fatalf("write manager fixture: %v", err)
	}
	t.Setenv("PATH", binDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	withRunOutcomeWorkingDirectory(t, project)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app, _ := newRunRootApp(t, strings.NewReader("1\n1\n"), stdout, stderr, terminal.Capabilities{Interaction: terminal.PlainInteractive})
	outcome := app.Execute(context.Background(), []string{"run"})
	if outcome.Code != 7 || outcome.Err != nil || strings.Contains(stderr.String(), "error:") {
		t.Fatalf("child exit outcome = %#v, stdout = %q, stderr = %q", outcome, stdout.String(), stderr.String())
	}
}

func newRunRootApp(t *testing.T, input io.Reader, output, diagnostics *bytes.Buffer, session terminal.Capabilities) (*rootcommand.App, *cmdutil.Factory) {
	t.Helper()
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			In:     input,
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: session,
	})
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return app, factory
}

func writeRunOutcomeFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create package directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func withRunOutcomeWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
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
}

type panicRunReader struct{}

func (panicRunReader) Read([]byte) (int, error) {
	panic("run attempted to read Automation input")
}
