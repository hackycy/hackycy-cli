package zip_test

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func TestZIPAutomationFailsBeforeArchiveCreationOrInputRead(t *testing.T) {
	project := t.TempDir()
	writeZIPOutcomeFile(t, project, "package.json", `{"name":"project","devDependencies":{"vite":"1"}}`)
	writeZIPOutcomeFile(t, project, "dist/index.html", "<main />")
	withZIPOutcomeWorkingDirectory(t, project)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app, factory := newZIPRootApp(t, panicZIPReader{}, stdout, stderr, terminal.Capabilities{Interaction: terminal.Automation})

	outcome := app.Execute(context.Background(), []string{"zip", ".", "--without-open"})
	if outcome.Code != 1 || outcome.Err == nil || outcome.Err.Error() != "zip requires an interactive terminal" || stdout.Len() != 0 || stderr.String() != "error: zip requires an interactive terminal\n" || terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
		t.Fatalf("Automation outcome = %#v, streams = (%q, %q)", outcome, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, "dist", "project.zip")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Automation created archive: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	outcome = app.Execute(context.Background(), []string{"--log-level", "warn", "zip", ".", "--without-open"})
	if outcome.Code != 1 || outcome.Err == nil || factory.Logging.Level() != logging.Warn {
		t.Fatalf("diagnostic outcome = %#v, level = %v, stderr = %q", outcome, factory.Logging.Level(), stderr.String())
	}
}

func newZIPRootApp(t *testing.T, input io.Reader, output, diagnostics *bytes.Buffer, session terminal.Capabilities) (*rootcommand.App, *cmdutil.Factory) {
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

func writeZIPOutcomeFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func withZIPOutcomeWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

type panicZIPReader struct{}

func (panicZIPReader) Read([]byte) (int, error) {
	panic("zip Automation must not read stdin")
}
