package root

import (
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestGlobalSurface(t *testing.T) {
	app, output, errors, runtime := testApp(t, nil)

	if outcome := app.Execute(context.Background(), nil); outcome.Code != 1 || !strings.Contains(output.String(), "Usage:") || errors.Len() != 0 {
		t.Fatalf("no-argument outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"--help"}); outcome.Code != 0 || !strings.Contains(output.String(), "Usage:") || errors.Len() != 0 {
		t.Fatalf("help outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
	output.Reset()
	if outcome := app.Execute(context.Background(), []string{"-V"}); outcome.Code != 0 || output.String() != "0.0.0-dev\n" {
		t.Fatalf("version outcome = %#v, stdout = %q", outcome, output.String())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"unknown"}); outcome.Code != 1 || errors.String() != "error: unknown command 'unknown'; Run 'ycy --help' for usage.\n" {
		t.Fatalf("unknown outcome = %#v, stderr = %q", outcome, errors.String())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"--log-level", "warn"}); outcome.Code != 1 || runtime.Level() != logging.Info {
		t.Fatalf("log-level outcome = %#v, level = %v", outcome, runtime.Level())
	}
}

func TestGlobalLogPrecedenceAndVersionBypass(t *testing.T) {
	app, output, errors, runtime := testApp(t, map[string]string{"YCY_LOG_LEVEL": "debug"})
	if outcome := app.Execute(context.Background(), nil); outcome.Code != 1 || runtime.Level() != logging.Info {
		t.Fatalf("environment outcome = %#v, level = %v", outcome, runtime.Level())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"--version", "--log-level", "invalid"}); outcome.Code != 0 || output.String() != "0.0.0-dev\n" || errors.Len() != 0 {
		t.Fatalf("version bypass outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"--log-level", "invalid"}); outcome.Code != 1 || errors.Len() != 0 || !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("root discovery bypass outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
}

func TestDiagnosticConfigurationRunsBeforeEffectsAndBypassesDiscovery(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		arguments   []string
		wantCode    int
		wantLevel   logging.Level
		wantFormat  logging.RecordFormat
		wantError   string
	}{
		{
			name:        "environment config applies to executing command",
			environment: map[string]string{"YCY_LOG_LEVEL": "warn", "YCY_LOG_FORMAT": "json"},
			arguments:   []string{"rm", "missing"},
			wantLevel:   logging.Warn,
			wantFormat:  logging.JSONFormat,
		},
		{
			name:        "explicit level wins over environment",
			environment: map[string]string{"YCY_LOG_LEVEL": "warn", "YCY_LOG_FORMAT": "json"},
			arguments:   []string{"--log-level", "debug", "rm", "missing"},
			wantLevel:   logging.Debug,
			wantFormat:  logging.JSONFormat,
		},
		{
			name:       "explicit format reaches runtime",
			arguments:  []string{"--log-format", "json", "rm", "missing"},
			wantLevel:  logging.Info,
			wantFormat: logging.JSONFormat,
		},
		{
			name:       "verbose selects debug",
			arguments:  []string{"--verbose", "rm", "missing"},
			wantLevel:  logging.Debug,
			wantFormat: logging.TextFormat,
		},
		{
			name:       "quiet selects error",
			arguments:  []string{"-q", "rm", "missing"},
			wantLevel:  logging.Error,
			wantFormat: logging.TextFormat,
		},
		{
			name:       "conflicting controls stop before effect",
			arguments:  []string{"--log-level", "info", "--quiet", "rm", "missing"},
			wantCode:   1,
			wantLevel:  logging.Info,
			wantFormat: logging.TextFormat,
			wantError:  "error: --log-level, --verbose, and --quiet are mutually exclusive\n",
		},
		{
			name:       "invalid format stops before effect",
			arguments:  []string{"--log-format", "yaml", "rm", "missing"},
			wantCode:   1,
			wantLevel:  logging.Info,
			wantFormat: logging.TextFormat,
			wantError:  `error: invalid log format "yaml" (expected text or json)` + "\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := &bytes.Buffer{}
			errors := &bytes.Buffer{}
			runtime := logging.NewRuntime(logging.Options{Writer: errors})
			app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
				Out:         output,
				Err:         errors,
				Environment: func(key string) string { return test.environment[key] },
				Logging:     runtime,
			})
			if err != nil {
				t.Fatalf("New returned an error: %v", err)
			}

			outcome := app.Execute(context.Background(), test.arguments)
			if outcome.Code != test.wantCode || errors.String() != test.wantError || runtime.Level() != test.wantLevel || runtime.Format() != test.wantFormat {
				t.Fatalf("outcome = %#v, stderr = %q, level = %v, format = %q", outcome, errors.String(), runtime.Level(), runtime.Format())
			}
		})
	}

	for _, arguments := range [][]string{
		{"--help", "--log-level", "invalid"},
		{"--version", "--log-level", "invalid"},
		{"completion", "bash", "--log-format", "yaml"},
	} {
		output := &bytes.Buffer{}
		errors := &bytes.Buffer{}
		app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
			Out:         output,
			Err:         errors,
			Environment: func(string) string { return "invalid" },
		})
		if err != nil {
			t.Fatalf("New returned an error: %v", err)
		}
		if outcome := app.Execute(context.Background(), arguments); outcome.Code != 0 || errors.Len() != 0 || output.Len() == 0 {
			t.Fatalf("discovery arguments %q outcome = %#v, stdout = %q, stderr = %q", arguments, outcome, output.String(), errors.String())
		}
	}
}

func TestRichTerminalDiscoveryPreservesVersionAndRawCompletion(t *testing.T) {
	output := &bytes.Buffer{}
	errors := &bytes.Buffer{}
	app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
		Out: output,
		Err: errors,
		Capabilities: terminal.Capabilities{
			Interaction: terminal.RichInteractive,
			Stdout:      terminal.StreamCapability{Terminal: true, Color: true},
			Stderr:      terminal.StreamCapability{Terminal: true, Color: true},
		},
		Logging: logging.NewRuntime(logging.Options{Writer: errors}),
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	if outcome := app.Execute(context.Background(), []string{"--help"}); outcome.Code != 0 || !strings.Contains(output.String(), "Ycy command line interface") || errors.Len() != 0 {
		t.Fatalf("help outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
	if strings.Contains(output.String(), "\x1b[?1049") || strings.Contains(output.String(), "ANSWERS") || strings.Contains(output.String(), "WORK") || strings.Contains(output.String(), "OUTCOME") {
		t.Fatalf("help entered a Console lifecycle: %q", output.String())
	}

	output.Reset()
	if outcome := app.Execute(context.Background(), []string{"--version"}); outcome.Code != 0 || output.String() != "0.0.0-dev\n" {
		t.Fatalf("version outcome = %#v, stdout = %q", outcome, output.String())
	}
	output.Reset()
	if outcome := app.Execute(context.Background(), []string{"completion", "bash"}); outcome.Code != 0 || !strings.Contains(output.String(), "__start_ycy") {
		t.Fatalf("completion outcome = %#v, stdout = %q", outcome, output.String())
	}
}

func TestDiscoveryWriteFailureBecomesOneRootDiagnostic(t *testing.T) {
	outputFailure := stderrors.New("discovery output unavailable")
	output := &failingWriter{err: outputFailure}
	errors := &bytes.Buffer{}
	app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
		Out:     output,
		Err:     errors,
		Logging: logging.NewRuntime(logging.Options{Writer: errors}),
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"--help"})
	if outcome.Code != 1 || !stderrors.Is(outcome.Err, outputFailure) || errors.String() != "error: discovery output unavailable\n" || output.calls != 1 {
		t.Fatalf("outcome = %#v, stderr = %q, output calls = %d", outcome, errors.String(), output.calls)
	}
}

func TestDiscoveryShortWriteBecomesOneRootDiagnostic(t *testing.T) {
	output := &shortWriter{}
	errors := &bytes.Buffer{}
	app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
		Out:     output,
		Err:     errors,
		Logging: logging.NewRuntime(logging.Options{Writer: errors}),
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"--help"})
	if outcome.Code != 1 || !stderrors.Is(outcome.Err, io.ErrShortWrite) || errors.String() != "error: short write\n" || output.calls != 1 {
		t.Fatalf("outcome = %#v, stderr = %q, output calls = %d", outcome, errors.String(), output.calls)
	}
}

func TestParserRecoveryUsesOneActionableErrorLine(t *testing.T) {
	output := &bytes.Buffer{}
	errors := &bytes.Buffer{}
	app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
		Out:     output,
		Err:     errors,
		Logging: logging.NewRuntime(logging.Options{Writer: errors}),
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	for _, testCase := range []struct {
		name      string
		arguments []string
		want      string
	}{
		{
			name:      "unique command candidate",
			arguments: []string{"exprot"},
			want:      "error: unknown command 'exprot'; did you mean 'export'? Run 'ycy export --help' for usage.\n",
		},
		{
			name:      "unique nested command candidate",
			arguments: []string{"config", "frok"},
			want:      "error: unknown command 'frok'; did you mean 'fork'? Run 'ycy config fork --help' for usage.\n",
		},
		{
			name:      "unique flag candidate",
			arguments: []string{"export", "env", "--merg"},
			want:      "error: unknown flag: --merg; did you mean '--merge'? Run 'ycy export env --help' for usage.\n",
		},
		{
			name:      "no candidate",
			arguments: []string{"unknown"},
			want:      "error: unknown command 'unknown'; Run 'ycy --help' for usage.\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			output.Reset()
			errors.Reset()
			outcome := app.Execute(context.Background(), testCase.arguments)
			if outcome.Code != 1 || errors.String() != testCase.want || output.Len() != 0 {
				t.Fatalf("outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
			}
		})
	}
}

func TestParserRecoveryLeavesCommandErrorsUntouched(t *testing.T) {
	output := &bytes.Buffer{}
	errors := &bytes.Buffer{}
	app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
		Out:     output,
		Err:     errors,
		Logging: logging.NewRuntime(logging.Options{Writer: errors}),
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	outcome := app.execute(func() error { return stderrors.New("command validation failed") })
	if outcome.Code != 1 || errors.String() != "error: command validation failed\n" || output.Len() != 0 {
		t.Fatalf("outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
}

func TestRootDiagnosticProjectionIsRedactedControlFreeAndBounded(t *testing.T) {
	app, output, diagnostics, _ := testApp(t, nil)
	original := stderrors.New("token=not-for-output\r\n\x1b[31mvisible\x1b[0m " + strings.Repeat("x", 2048))

	outcome := app.execute(func() error { return original })
	projected := diagnostics.String()
	if outcome.Code != 1 || !stderrors.Is(outcome.Err, original) {
		t.Fatalf("outcome = %#v", outcome)
	}
	if output.Len() != 0 || strings.Contains(projected, "not-for-output") || terminaltest.ContainsTerminalControl([]byte(projected)) || strings.Count(projected, "\n") != 1 || !strings.Contains(projected, "visible") || !strings.Contains(projected, "[truncated]") || len(projected) >= len(original.Error()) {
		t.Fatalf("stdout = %q, stderr = %q", output.String(), projected)
	}
}

func TestConfigParentExposesMigratedForkGroup(t *testing.T) {
	app, output, errors, _ := testApp(t, nil)

	if outcome := app.Execute(context.Background(), []string{"config", "fork", "--help"}); outcome.Code != 0 || !strings.Contains(output.String(), "list") || !strings.Contains(output.String(), "add") || !strings.Contains(output.String(), "remove") {
		t.Fatalf("fork help outcome = %#v, stdout = %q", outcome, output.String())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"config", "fork", "list"}); outcome.Code != 0 || errors.Len() != 0 {
		t.Fatalf("config fork list outcome = %#v, stderr = %q", outcome, errors.String())
	}
}

func TestFSLeafIsAlwaysRegistered(t *testing.T) {
	app, output, errors, _ := testApp(t, nil)

	if outcome := app.Execute(context.Background(), []string{"--help"}); outcome.Code != 0 || !strings.Contains(output.String(), "\n  fs") {
		t.Fatalf("help outcome = %#v, stdout = %q", outcome, output.String())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"fs", "--help"}); outcome.Code != 0 || !strings.Contains(output.String(), "Browse a directory in a browser") || errors.Len() != 0 {
		t.Fatalf("fs help outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
}

func TestTunnelLeafIsAlwaysRegistered(t *testing.T) {
	app, output, errors, _ := testApp(t, nil)

	if outcome := app.Execute(context.Background(), []string{"--help"}); outcome.Code != 0 || !strings.Contains(output.String(), "\n  tunnel") {
		t.Fatalf("help outcome = %#v, stdout = %q", outcome, output.String())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"tunnel", "--help"}); outcome.Code != 0 || !strings.Contains(output.String(), "server") || !strings.Contains(output.String(), "connect") || errors.Len() != 0 {
		t.Fatalf("tunnel help outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
	output.Reset()
	errors.Reset()
	if outcome := app.Execute(context.Background(), []string{"tunnel", "connect", "--help"}); outcome.Code != 0 || !strings.Contains(output.String(), "--server") || !strings.Contains(output.String(), "--token") || strings.Contains(output.String(), "--control-port") || !strings.Contains(output.String(), "--log-level") || !strings.Contains(output.String(), "--log-format") || !strings.Contains(output.String(), "--quiet") || !strings.Contains(output.String(), "--verbose") || errors.Len() != 0 {
		t.Fatalf("tunnel connect help outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
}

func TestPanicMappingRedactsAndAddsDebugStack(t *testing.T) {
	app, output, errors, _ := testApp(t, map[string]string{"DEBUG": "1"})
	outcome := app.execute(func() error {
		panic("token=not-for-output")
	})
	if outcome.Code != 1 || output.String() != "\n" || strings.Contains(errors.String(), "not-for-output") || !strings.Contains(errors.String(), "token=[REDACTED]") || !strings.Contains(errors.String(), "goroutine") {
		t.Fatalf("panic outcome = %#v, stdout = %q, stderr = %q", outcome, output.String(), errors.String())
	}
}

type failingWriter struct {
	err   error
	calls int
}

func (writer *failingWriter) Write(value []byte) (int, error) {
	writer.calls++
	return 0, writer.err
}

type shortWriter struct {
	calls int
}

func (writer *shortWriter) Write(value []byte) (int, error) {
	writer.calls++
	if len(value) == 0 {
		return 0, nil
	}
	return len(value) - 1, nil
}

func testApp(t *testing.T, environment map[string]string) (*App, *bytes.Buffer, *bytes.Buffer, *logging.Runtime) {
	t.Helper()
	output := &bytes.Buffer{}
	errors := &bytes.Buffer{}
	runtime := logging.NewRuntime(logging.Options{Writer: errors})
	app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
		Out: output,
		Err: errors,
		Environment: func(key string) string {
			return environment[key]
		},
		Logging: runtime,
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	return app, output, errors, runtime
}
