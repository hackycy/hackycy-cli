package run

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestOSRunChildRunnerUsesArgvCWDAndInheritedStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell fixture is Unix-specific")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "project")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("create project: %v", err)
	}
	argumentsPath := filepath.Join(root, "arguments")
	workingDirectoryPath := filepath.Join(root, "working-directory")
	inputPath := filepath.Join(root, "input")
	t.Setenv("RUN_ARGUMENTS", argumentsPath)
	t.Setenv("RUN_WORKING_DIRECTORY", workingDirectoryPath)
	t.Setenv("RUN_INPUT", inputPath)
	script := filepath.Join(root, "runner.sh")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RUN_ARGUMENTS\"\npwd > \"$RUN_WORKING_DIRECTORY\"\ncat > \"$RUN_INPUT\"\nprintf 'child stdout'\nprintf 'child stderr' >&2\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write child fixture: %v", err)
	}

	streams := terminaltest.NewRedirectedStreams("stdin payload")
	runner := newOSRunChildRunner(streams.Stdin, streams.Stdout, streams.Stderr)
	result, err := runner.Run(context.Background(), ChildRequest{
		Executable: script,
		Arguments:  []string{"run", "check"},
		Directory:  directory,
	})

	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	assertRunProcessFile(t, argumentsPath, "run\ncheck\n")
	assertRunProcessFile(t, workingDirectoryPath, directory+"\n")
	assertRunProcessFile(t, inputPath, "stdin payload")
	if streams.Stdout.String() != "child stdout" || streams.Stderr.String() != "child stderr" {
		t.Fatalf("streams = (%q, %q)", streams.Stdout.String(), streams.Stderr.String())
	}
	if terminaltest.ContainsTerminalControl(streams.Stdout.Bytes()) || terminaltest.ContainsTerminalControl(streams.Stderr.Bytes()) {
		t.Fatalf("raw child streams contain unexpected terminal control: stdout = %q stderr = %q", streams.Stdout.String(), streams.Stderr.String())
	}
}

func TestOSRunChildRunnerPreservesMissingExecutableFailure(t *testing.T) {
	runner := newOSRunChildRunner(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	_, err := runner.Run(context.Background(), ChildRequest{
		Executable: filepath.Join(t.TempDir(), "missing-executable"),
	})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Run() error = %v, want missing executable", err)
	}
}

func assertRunProcessFile(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(contents) != want {
		t.Fatalf("%s = %q, want %q", path, contents, want)
	}
}
