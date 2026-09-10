package server_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	rootcommand "github.com/hackycy/hackycy-cli/pkg/cmd/root"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestRootConfiguresDiagnosticsBeforeTunnelServerValidation(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    stdout,
			ErrOut: stderr,
		},
		Environment:       func(string) string { return "" },
		EnvironmentLookup: func(string) (string, bool) { return "", false },
		Capabilities:      terminal.Capabilities{Interaction: terminal.Automation},
	})
	runtime := logging.NewRuntime(logging.Options{Writer: stderr})
	factory.Logging = runtime
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "tunnel", "server"})
	if outcome.Code != 1 || outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "YCY_TUNNEL_ADMIN_PASSWORD") || runtime.Level() != logging.Warn || !strings.Contains(stderr.String(), "Could not resolve tunnel server configuration") || stdout.Len() != 0 {
		t.Fatalf("outcome = %#v, level = %v, streams = (%q, %q)", outcome, runtime.Level(), stdout.String(), stderr.String())
	}
}
