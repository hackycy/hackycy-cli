package connect_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	rootcommand "github.com/hackycy/hackycy-cli/pkg/cmd/root"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestRootConfiguresDiagnosticsBeforeTunnelConnectValidation(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	home := t.TempDir()
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    stdout,
			ErrOut: stderr,
		},
		Environment: func(key string) string {
			if key == "HOME" {
				return home
			}
			return ""
		},
		EnvironmentLookup: func(string) (string, bool) { return "", false },
		Capabilities:      terminal.Capabilities{Interaction: terminal.Automation},
	})
	store, err := appconfig.New(appconfig.Dependencies{
		Environment: func(key string) string {
			if key == "HOME" {
				return home
			}
			return ""
		},
		MachineID: func() (string, error) { return "fixture-machine", nil },
		Username:  func() (string, error) { return "fixture-user", nil },
	})
	if err != nil {
		t.Fatalf("appconfig.New() error = %v", err)
	}
	factory.ConfigStore = func() (*appconfig.Store, error) { return store, nil }
	runtime := logging.NewRuntime(logging.Options{Writer: stderr})
	factory.Logging = runtime
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "tunnel", "connect", "--server", "", "--token", "client-token"})
	if outcome.Code != 1 || outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "Control plane must not be empty") || runtime.Level() != logging.Warn || !strings.Contains(stderr.String(), "Could not resolve tunnel client configuration") || stdout.Len() != 0 {
		t.Fatalf("outcome = %#v, level = %v, streams = (%q, %q)", outcome, runtime.Level(), stdout.String(), stderr.String())
	}
}
