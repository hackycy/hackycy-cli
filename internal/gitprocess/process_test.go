package gitprocess

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunnerUsesArgvAndCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	root := t.TempDir()
	argumentsPath := filepath.Join(root, "arguments")
	t.Setenv("GITPROCESS_ARGUMENTS", argumentsPath)
	runner := &Runner{Executable: writeScript(t, root, "git", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GITPROCESS_ARGUMENTS\"\nprintf 'child stdout'\nprintf 'child stderr' >&2\n")}

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

func TestRunnerPreservesMissingExecutableFailure(t *testing.T) {
	runner := &Runner{Executable: filepath.Join(t.TempDir(), "missing-git")}
	_, err := runner.Run(context.Background(), nil)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run() error = %v, want missing executable", err)
	}
}

func TestRunnerMapsGitExitToCapturedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	runner := &Runner{Executable: writeScript(t, t.TempDir(), "git", "#!/bin/sh\nprintf 'fatal output' >&2\nexit 7\n")}
	output, err := runner.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if output.ExitCode != 7 || string(output.Stderr) != "fatal output" {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunnerReturnsCancelledContextBeforeStartup(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Runner{}).Run(cancelled, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context cancellation", err)
	}
}

func TestRunnerPassesInputAndCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	root := t.TempDir()
	inputPath := filepath.Join(root, "input")
	t.Setenv("GITPROCESS_INPUT", inputPath)
	runner := &Runner{Executable: writeScript(t, root, "git", "#!/bin/sh\ncat > \"$GITPROCESS_INPUT\"\nprintf 'child stdout'\nprintf 'child stderr' >&2\n")}

	output, err := runner.RunInput(context.Background(), []string{"cat-file", "--batch"}, []byte("HEAD:package.json\n"))
	if err != nil {
		t.Fatalf("RunInput() error = %v", err)
	}
	if output.ExitCode != 0 || string(output.Stdout) != "child stdout" || string(output.Stderr) != "child stderr" {
		t.Fatalf("RunInput() output = %#v", output)
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}
	if got, want := string(input), "HEAD:package.json\n"; got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}

func writeScript(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name+".sh")
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
