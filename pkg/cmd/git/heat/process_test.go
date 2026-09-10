package heat

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
)

func TestGitRunnerAdapterUsesArgvAndCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	root := t.TempDir()
	argumentsPath := filepath.Join(root, "arguments")
	t.Setenv("HEAT_GIT_ARGUMENTS", argumentsPath)
	script := writeHeatGitScript(t, root, "git", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HEAT_GIT_ARGUMENTS\"\nprintf 'child stdout'\nprintf 'child stderr' >&2\n")

	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: script}}
	output, err := runner.Run(context.Background(), []string{"log", "--name-status"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if output.ExitCode != 0 || string(output.Stdout) != "child stdout" || string(output.Stderr) != "child stderr" {
		t.Fatalf("output = %#v", output)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatalf("read arguments: %v", err)
	}
	if got, want := string(arguments), "log\n--name-status\n"; got != want {
		t.Fatalf("arguments = %q, want %q", got, want)
	}
}

func TestGitRunnerAdapterPreservesMissingExecutableFailure(t *testing.T) {
	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: filepath.Join(t.TempDir(), "missing-git")}}
	_, err := runner.Run(context.Background(), nil)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run() error = %v, want missing executable", err)
	}
}

func TestGitRunnerAdapterMapsGitExitToCapturedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	script := writeHeatGitScript(t, t.TempDir(), "git", "#!/bin/sh\nprintf 'fatal output' >&2\nexit 7\n")
	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: script}}
	output, err := runner.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if output.ExitCode != 7 || string(output.Stderr) != "fatal output" {
		t.Fatalf("output = %#v", output)
	}
}

func writeHeatGitScript(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name+".sh")
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestGitRunnerAdapterReturnsCancelledContext(t *testing.T) {
	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: "git"}}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runner.Run(cancelled, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context cancellation", err)
	}
}

func TestGitRunnerAdapterLeavesNoChildForCompletedScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	script := writeHeatGitScript(t, t.TempDir(), "git", "#!/bin/sh\nprintf '%s' done\n")
	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: script}}
	output, err := runner.Run(context.Background(), strings.Fields("log"))
	if err != nil || string(output.Stdout) != "done" {
		t.Fatalf("Run() = (%#v, %v)", output, err)
	}
}
