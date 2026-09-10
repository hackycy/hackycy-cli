package env_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	rootcommand "github.com/hackycy/hackycy-cli/pkg/cmd/root"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestExportEnvAutomationPreservesResolvedPathsAndRejectsAmbiguityBeforeEffects(t *testing.T) {
	workingDirectory := t.TempDir()
	writeEnvFile(t, filepath.Join(workingDirectory, "named", ".env.production"), "VALUE=production\n")
	writeEnvFile(t, filepath.Join(workingDirectory, "unique", ".env.production"), "VALUE=unique\n")
	writeEnvFile(t, filepath.Join(workingDirectory, "ambiguous", ".env"), "BASE=base\n")
	writeEnvFile(t, filepath.Join(workingDirectory, "ambiguous", ".env.production"), "VALUE=production\n")
	protectedPath := filepath.Join(workingDirectory, "protected.json")
	if err := os.WriteFile(protectedPath, []byte("protected"), 0o600); err != nil {
		t.Fatalf("write protected output: %v", err)
	}
	withWorkingDirectory(t, workingDirectory)

	for _, testCase := range []struct {
		name      string
		arguments []string
		wantOut   string
		wantErr   string
	}{
		{name: "named environment", arguments: []string{"export", "env", "named", "--env", "production"}, wantOut: "Exported variables:\n{\n  \"VALUE\": \"production\"\n}\n"},
		{name: "unique environment", arguments: []string{"export", "env", "unique"}, wantOut: "Exported variables:\n{\n  \"VALUE\": \"unique\"\n}\n"},
		{name: "ambiguous environment", arguments: []string{"export", "env", "ambiguous", "--out", "protected.json"}, wantErr: "error: export env requires an interactive terminal\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			app, _ := newRootApp(t, panicReader{}, stdout, stderr, terminal.Capabilities{Interaction: terminal.Automation})
			outcome := app.Execute(context.Background(), testCase.arguments)
			if testCase.wantErr == "" {
				if outcome.Code != 0 || outcome.Err != nil || stdout.String() != testCase.wantOut || stderr.Len() != 0 {
					t.Fatalf("resolved Automation outcome = %#v, stdout = %q, stderr = %q", outcome, stdout.String(), stderr.String())
				}
			} else if outcome.Code != 1 || outcome.Err == nil || outcome.Err.Error() != "export env requires an interactive terminal" || stdout.Len() != 0 || stderr.String() != testCase.wantErr {
				t.Fatalf("ambiguous Automation outcome = %#v, stdout = %q, stderr = %q", outcome, stdout.String(), stderr.String())
			}
			if terminaltest.ContainsTerminalControl(append(append([]byte{}, stdout.Bytes()...), stderr.Bytes()...)) {
				t.Fatalf("Automation streams contain terminal control: (%q, %q)", stdout.String(), stderr.String())
			}
		})
	}
	contents, err := os.ReadFile(protectedPath)
	if err != nil || string(contents) != "protected" {
		t.Fatalf("ambiguous Automation changed output target = (%v, %q)", err, contents)
	}
}

func TestExportEnvPlainCancellationDoesNotWriteOutput(t *testing.T) {
	workingDirectory := t.TempDir()
	writeEnvFile(t, filepath.Join(workingDirectory, "project", ".env"), "BASE=base\n")
	writeEnvFile(t, filepath.Join(workingDirectory, "project", ".env.production"), "VALUE=production\n")
	protectedPath := filepath.Join(workingDirectory, "protected.json")
	if err := os.WriteFile(protectedPath, []byte("protected"), 0o600); err != nil {
		t.Fatalf("write protected output: %v", err)
	}
	withWorkingDirectory(t, workingDirectory)
	stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	app, _ := newRootApp(t, strings.NewReader("cancel\n"), stdout, diagnostics, terminal.Capabilities{Interaction: terminal.PlainInteractive})

	outcome := app.Execute(context.Background(), []string{"export", "env", "project", "--out", "protected.json"})
	if outcome.Code != 0 || outcome.Err != nil || stdout.String() != "Cancelled\n" || terminaltest.ContainsTerminalControl(append(stdout.Bytes(), diagnostics.Bytes()...)) {
		t.Fatalf("Plain cancellation outcome = %#v, stdout = %q, diagnostics = %q", outcome, stdout.String(), diagnostics.String())
	}
	contents, err := os.ReadFile(protectedPath)
	if err != nil || string(contents) != "protected" {
		t.Fatalf("Plain cancellation changed output target = (%v, %q)", err, contents)
	}
}

func TestRootConfiguresDiagnosticsBeforeExportEnv(t *testing.T) {
	workingDirectory := t.TempDir()
	writeEnvFile(t, filepath.Join(workingDirectory, "project", ".env.production"), "VALUE=production\n")
	withWorkingDirectory(t, workingDirectory)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	app, runtime := newRootApp(t, panicReader{}, stdout, stderr, terminal.Capabilities{Interaction: terminal.Automation})

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "export", "env", "project", "--env", "production"})
	if outcome.Code != 0 || outcome.Err != nil || runtime.Level() != logging.Warn {
		t.Fatalf("export env outcome = %#v, level = %v, stderr = %q", outcome, runtime.Level(), stderr.String())
	}
}

func newRootApp(t *testing.T, input io.Reader, output, diagnostics *bytes.Buffer, session terminal.Capabilities) (*rootcommand.App, *logging.Runtime) {
	t.Helper()
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			In:     input,
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities:      session,
		Environment:       func(string) string { return "" },
		EnvironmentLookup: func(string) (string, bool) { return "", false },
	})
	runtime := logging.NewRuntime(logging.Options{Writer: diagnostics})
	factory.Logging = runtime
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return app, runtime
}

func writeEnvFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func withWorkingDirectory(t *testing.T, directory string) {
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

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) {
	panic("export env attempted to read Automation input")
}
