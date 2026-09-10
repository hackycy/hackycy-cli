package pulse_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	rootcommand "github.com/hackycy/hackycy-cli/pkg/cmd/root"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestRootConfiguresDiagnosticsBeforeGitPulse(t *testing.T) {
	workspace := t.TempDir()
	repository := filepath.Join(workspace, "fixture")
	runGitPulseCommand(t, repository, "init", "-q")
	runGitPulseCommand(t, repository, "config", "user.email", "fixture@example.test")
	runGitPulseCommand(t, repository, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGitPulseCommand(t, repository, "add", "README.md")
	runGitPulseCommand(t, repository, "commit", "-qm", "fixture")

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			In:     strings.NewReader("1\n"),
			Out:    stdout,
			ErrOut: stderr,
		},
		Capabilities:     terminal.Capabilities{Interaction: terminal.PlainInteractive},
		WorkingDirectory: func() (string, error) { return workspace, nil },
	})
	runtime := logging.NewRuntime(logging.Options{Writer: stderr})
	factory.Logging = runtime
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "git", "pulse", "--days", "1"})
	if outcome.Code != 0 || outcome.Err != nil || runtime.Level() != logging.Warn || !strings.Contains(stdout.String(), "fixture") {
		t.Fatalf("outcome = %#v, level = %v, streams = (%q, %q)", outcome, runtime.Level(), stdout.String(), stderr.String())
	}
}

func runGitPulseCommand(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
