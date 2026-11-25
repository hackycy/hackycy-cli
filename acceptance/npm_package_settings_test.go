//go:build acceptance

package acceptance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestStandaloneNPMPackageSettings(t *testing.T) {
	root := t.TempDir()
	binary := standaloneBinaryOutputPath(filepath.Join(root, "ycy"))
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	build.Dir = repositoryRoot(t)
	build.Env = environmentWith(map[string]string{"CGO_ENABLED": "0", "GOTOOLCHAIN": "go1.26.7", "GOWORK": "off"})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ycy: %v\n%s", err, output)
	}
	workspace := filepath.Join(root, "workspace")
	writeStandaloneZIPFile(t, workspace, "package.json", `{"private":true,"workspaces":["packages/*"]}`)
	writeStandaloneZIPFile(t, workspace, "packages/a/package.json", `{"name":"@example/a"}`)
	writeStandaloneZIPFile(t, workspace, "packages/b/package.json", `{"name":"example-beta"}`)
	writeStandaloneZIPFile(t, workspace, "packages/c/package.json", `{"name":"example-gamma"}`)
	invocationDirectory := filepath.Join(workspace, "packages", "c", "src")
	if err := os.Mkdir(invocationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	launcher := map[string]string{"darwin": "open", "linux": "gio"}[runtime.GOOS]
	if launcher != "" {
		writeStandaloneZIPFile(t, bin, launcher, `#!/bin/sh
if [ "$1" = 'open' ]; then shift; fi
printf '%s\n' "$1" >> "$YCY_NPM_TEST_LOG"
case "$1" in */example-beta/access) if [ "$YCY_NPM_TEST_FAIL" = 1 ]; then exit 4; fi;; esac
`)
		if err := os.Chmod(filepath.Join(bin, launcher), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	addresses := "https://www.npmjs.com/package/@example/a/access\n" +
		"https://www.npmjs.com/package/example-beta/access\n" +
		"https://www.npmjs.com/package/example-gamma/access\n"
	const list = "Found 3 npm packages:\n  @example/a\n  example-beta\n  example-gamma\n"
	newCommand := func(ctx context.Context, log, term, fail string, args ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, binary, append([]string{"npm", "open", "package-settings"}, args...)...)
		command.Dir = invocationDirectory
		command.Env = environmentWith(map[string]string{
			"PATH": bin, "CI": "", "NO_COLOR": "1", "TERM": term, "HOME": root, "USERPROFILE": root,
			"YCY_NPM_TEST_LOG": log, "YCY_NPM_TEST_FAIL": fail,
		})
		return command
	}
	t.Run("requires terminal unless batch", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "open.log")
		command := newCommand(t.Context(), log, "dumb", "")
		got, err := command.CombinedOutput()
		if exitCode(err) != 1 || !strings.Contains(string(got), "use --batch") {
			t.Fatalf("noninteractive command = %q, %v", got, err)
		}
		if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("noninteractive command touched browser log: %v", err)
		}
	})
	if launcher == "" {
		return // The native Windows browser opens through ShellExecute, which PATH cannot stub.
	}
	t.Run("batch opens full workspace and reports failures", func(t *testing.T) {
		log := filepath.Join(t.TempDir(), "open.log")
		command := newCommand(t.Context(), log, "dumb", "1", "--batch")
		var diagnostics bytes.Buffer
		command.Stderr = &diagnostics
		output, err := command.Output()
		if exitCode(err) != 1 || string(output) != list {
			t.Fatalf("batch command = %q, %v, stderr %q", output, err, diagnostics.String())
		}
		if !strings.Contains(diagnostics.String(), "https://www.npmjs.com/package/example-beta/access") ||
			!strings.Contains(diagnostics.String(), "failed to open 1") {
			t.Fatalf("batch diagnostics = %q", diagnostics.String())
		}
		assertNPMOpenLog(t, log, addresses)
	})
	for _, test := range []struct {
		name, term, input string
		interrupt         bool
		continueAll       bool
	}{
		{name: "plain decline", term: "dumb", input: "n\r"},
		{name: "plain interrupt", term: "dumb", interrupt: true},
		{name: "rich continue", term: "xterm-256color", input: "\r", continueAll: true},
		{name: "rich cancel", term: "xterm-256color", input: "\x1b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			log := filepath.Join(t.TempDir(), "open.log")
			command := newCommand(ctx, log, test.term, "")
			var stdout bytes.Buffer
			command.Stdout = &stdout
			process, err := terminaltest.StartPTYWithSize(command, 120, 30)
			if err != nil {
				if errors.Is(err, terminaltest.ErrPTYUnsupported) {
					t.Skip(err)
				}
				t.Fatal(err)
			}
			defer process.Close()
			var screen npmPTYBuffer
			readDone := make(chan struct{})
			go func() {
				_, _ = io.Copy(&screen, process.Terminal())
				close(readDone)
			}()
			defer func() { _ = process.Close(); <-readDone }()
			rich := test.term != "dumb"
			waitForNPMPrompt(t, ctx, process, &screen, "example-beta", rich)
			first := strings.SplitAfterN(addresses, "\n", 2)[0]
			assertNPMOpenLog(t, log, first)
			if test.interrupt {
				if err := command.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			} else if _, err := io.WriteString(process.Terminal(), test.input); err != nil {
				t.Fatal(err)
			}
			wantLog := first
			if test.continueAll {
				waitForNPMPrompt(t, ctx, process, &screen, "example-gamma", false)
				if _, err := io.WriteString(process.Terminal(), test.input); err != nil {
					t.Fatal(err)
				}
				wantLog = addresses
			}
			err = process.Wait()
			wantCode := 0
			if test.interrupt {
				wantCode = 1
			}
			if ctx.Err() != nil || exitCode(err) != wantCode || stdout.String() != list {
				t.Fatalf("interactive exit = %v, context %v, stdout %q, terminal %q", err, ctx.Err(), stdout.String(), screen.String())
			}
			assertNPMOpenLog(t, log, wantLog)
			if rich {
				waitForNPMPrompt(t, ctx, process, &screen, "\x1b[?1049l", false)
			}
		})
	}
}

func assertNPMOpenLog(t *testing.T, file, want string) {
	t.Helper()
	if got, err := os.ReadFile(file); err != nil || string(got) != want {
		t.Fatalf("browser log = %q, %v; want %q", got, err, want)
	}
}

type npmPTYBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *npmPTYBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(p)
}

func (buffer *npmPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func waitForNPMPrompt(t *testing.T, ctx context.Context, process *terminaltest.PTYProcess, screen *npmPTYBuffer, want string, respond bool) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		output := screen.String()
		if strings.Contains(output, want) {
			return
		}
		if respond && (strings.Contains(output, "\x1b]11;?") || strings.Contains(output, "\x1b[6n")) {
			if _, err := io.WriteString(process.Terminal(), "\x1b]11;rgb:0000/0000/0000\x1b\\\x1b[1;1R"); err != nil {
				t.Fatal(err)
			}
			respond = false
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %q: %v, terminal %q", want, ctx.Err(), output)
		case <-tick.C:
		}
	}
}
