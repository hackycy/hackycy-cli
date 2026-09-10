package pulse

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
)

func TestGitRunnerAdapterUsesArgvAndCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	root := t.TempDir()
	argumentsPath := filepath.Join(root, "arguments")
	t.Setenv("PULSE_GIT_ARGUMENTS", argumentsPath)
	script := writePulseGitScript(t, root, "git", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PULSE_GIT_ARGUMENTS\"\nprintf 'pulse stdout'\nprintf 'pulse stderr' >&2\n")
	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: script}}

	output, err := runner.Run(context.Background(), []string{"log", "--since=2026-08-23 00:00:00"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if output.ExitCode != 0 || string(output.Stdout) != "pulse stdout" || string(output.Stderr) != "pulse stderr" {
		t.Fatalf("output = %#v", output)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatalf("read arguments: %v", err)
	}
	if got, want := string(arguments), "log\n--since=2026-08-23 00:00:00\n"; got != want {
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
	script := writePulseGitScript(t, t.TempDir(), "git", "#!/bin/sh\nprintf 'fatal output' >&2\nexit 7\n")
	runner := gitRunnerAdapter{runner: &gitprocess.Runner{Executable: script}}
	output, err := runner.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if output.ExitCode != 7 || string(output.Stderr) != "fatal output" {
		t.Fatalf("output = %#v", output)
	}
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

func writePulseGitScript(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name+".sh")
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
