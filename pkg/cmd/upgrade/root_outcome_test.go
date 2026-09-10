package upgrade_test

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

func TestRootConfiguresDiagnosticsBeforeUpgradeValidation(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	factory := commandfactory.New(commandfactory.Options{
		Version: "not-a-semantic-version",
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

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "upgrade"})
	if outcome.Code != 1 || outcome.Err != nil || runtime.Level() != logging.Warn || stdout.String() != "Update aborted.\n" || !strings.Contains(stderr.String(), "current CLI version is invalid") {
		t.Fatalf("outcome = %#v, level = %v, streams = (%q, %q)", outcome, runtime.Level(), stdout.String(), stderr.String())
	}
}
