package heat_test

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

func TestRootConfiguresDiagnosticsBeforeGitHeat(t *testing.T) {
	repository := t.TempDir()
	runGitHeatCommand(t, repository, "init", "-q")
	runGitHeatCommand(t, repository, "config", "user.email", "fixture@example.test")
	runGitHeatCommand(t, repository, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGitHeatCommand(t, repository, "add", "README.md")
	runGitHeatCommand(t, repository, "commit", "-qm", "fixture")

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(repository); err != nil {
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

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "git", "heat", "--limit", "1", "--type", "files"})
	if outcome.Code != 0 || outcome.Err != nil || runtime.Level() != logging.Warn || stderr.Len() != 0 || !strings.Contains(stdout.String(), "README.md") {
		t.Fatalf("outcome = %#v, level = %v, streams = (%q, %q)", outcome, runtime.Level(), stdout.String(), stderr.String())
	}
}

func runGitHeatCommand(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
