package cm_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestRootConfiguresDiagnosticsBeforeGitCM(t *testing.T) {
	repository := t.TempDir()
	runGitCMRootOutcome(t, repository, "init", "-q")
	runGitCMRootOutcome(t, repository, "config", "user.email", "fixture@example.test")
	runGitCMRootOutcome(t, repository, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

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
	runtime := logging.NewRuntime(logging.Options{Writer: stderr})
	providerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		providerCalls++
		if runtime.Level() != logging.Warn {
			t.Errorf("logging level during provider request = %v, want %v", runtime.Level(), logging.Warn)
		}
		if request.Method != http.MethodPost || request.URL.Path != "/chat/completions" || request.Header.Get("Authorization") != "Bearer fixture-api-key" {
			t.Errorf("provider request = %s %s, authorization = %q", request.Method, request.URL.Path, request.Header.Get("Authorization"))
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"choices":[{"message":{"content":"feat(cm): prove root diagnostics"}}]}`)
	}))
	defer server.Close()
	t.Setenv("YCY_CM_PROFILE", "")
	t.Setenv("YCY_CM_BASE_URL", server.URL)
	t.Setenv("YCY_CM_MODEL", "fixture-model")
	t.Setenv("YCY_CM_API_KEY", "fixture-api-key")

	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    stdout,
			ErrOut: stderr,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
	factory.Logging = runtime
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "git", "cm", "--dry-run"})
	if outcome.Code != 0 || outcome.Err != nil || runtime.Level() != logging.Warn || providerCalls != 1 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "feat(cm): prove root diagnostics") {
		t.Fatalf("outcome = %#v, level = %v, calls = %d, streams = (%q, %q)", outcome, runtime.Level(), providerCalls, stdout.String(), stderr.String())
	}
	if status := gitCMRootOutcome(t, repository, "status", "--short"); status != "?? README.md\n" {
		t.Fatalf("dry run status = %q", status)
	}
}

func TestRootNormalizesGitCMOptionalRemoteArgument(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    stdout,
			ErrOut: stderr,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"git", "cm", "--push", "upstream"})
	const want = "error: Use --push with --stage, --staged, or --stage-all.\n"
	if outcome.Code != 1 || outcome.Err == nil || outcome.Err.Error() != "Use --push with --stage, --staged, or --stage-all." || stderr.String() != want || stdout.Len() != 0 {
		t.Fatalf("outcome = %#v, streams = (%q, %q)", outcome, stdout.String(), stderr.String())
	}
}

func runGitCMRootOutcome(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func gitCMRootOutcome(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
