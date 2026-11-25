//go:build acceptance

package acceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
if [ "$YCY_NPM_TEST_FAIL" = slow ]; then /bin/sleep 0.6; fi
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
		name, term, fail string
		selectKeys       string
		steps            []string
		interrupt        bool
		wantLog          string
		wantResult       []string
		wantCode         int
	}{
		{name: "plain stop", term: "dumb", selectKeys: "\r", steps: []string{"n\r"}, wantResult: []string{"Pending 3"}},
		{name: "plain interrupt", term: "dumb", selectKeys: "\r", interrupt: true, wantResult: []string{"Pending 3"}, wantCode: 1},
		{name: "rich cancel selection", term: "xterm-256color", selectKeys: "\x1b", wantResult: []string{"Pending 3"}},
		{name: "rich select subset", term: "xterm-256color", selectKeys: "\x1b[B \r", steps: []string{"\r", "\r"}, wantLog: "https://www.npmjs.com/package/@example/a/access\nhttps://www.npmjs.com/package/example-gamma/access\n", wantResult: []string{"Opened 2", "Not selected 1", "Not selected  example-beta"}},
		{name: "rich stop after first", term: "xterm-256color", fail: "slow", selectKeys: "\r", steps: []string{"\r", "\x1b"}, wantLog: "https://www.npmjs.com/package/@example/a/access\n", wantResult: []string{"Opened 1", "Pending 2"}},
		{name: "rich continue after failure", term: "xterm-256color", fail: "1", selectKeys: "\r", steps: []string{"\r", "\r", "\r"}, wantLog: addresses, wantResult: []string{"Opened 2", "Failed 1"}, wantCode: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			log := filepath.Join(t.TempDir(), "open.log")
			command := newCommand(ctx, log, test.term, test.fail)
			var stdout bytes.Buffer
			command.Stdout = &stdout
			process, err := terminaltest.StartPTYWithSize(command, 100, 22)
			if err != nil {
				if errors.Is(err, terminaltest.ErrPTYUnsupported) {
					t.Skip(err)
				}
				t.Fatal(err)
			}
			var screen npmPTYBuffer
			readDone := make(chan struct{})
			go func() {
				_, _ = io.Copy(&screen, process.Terminal())
				close(readDone)
			}()
			defer func() { _ = process.Close(); <-readDone }()
			rich := test.term != "dumb"
			waitForNPMPrompt(t, ctx, process, &screen, "All selected by default", rich)
			assertNPMOpenLog(t, log, "")
			if _, err := io.WriteString(process.Terminal(), test.selectKeys); err != nil {
				t.Fatal(err)
			}
			if test.interrupt {
				waitForNPMPrompt(t, ctx, process, &screen, "Enter to open", false)
				assertNPMOpenLog(t, log, "")
				if err := command.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}
			for index, keys := range test.steps {
				want := "Enter to open"
				if rich {
					want = "Enter [Open next]"
					if index == 1 {
						want = "Opened 1 · Pending"
					} else if index == 2 {
						want = "Failed 1"
					}
				}
				waitForNPMPrompt(t, ctx, process, &screen, want, false)
				assertNPMOpenLog(t, log, strings.Join(strings.SplitAfter(test.wantLog, "\n")[:index], ""))
				if _, err := io.WriteString(process.Terminal(), keys); err != nil {
					t.Fatal(err)
				}
				if test.fail == "slow" && index == 0 {
					waitForNPMPrompt(t, ctx, process, &screen, "Opening 1", false)
					if _, err := io.WriteString(process.Terminal(), "\r"); err != nil {
						t.Fatal(err)
					}
				}
			}
			err = process.Wait()
			if ctx.Err() != nil || exitCode(err) != test.wantCode {
				t.Fatalf("interactive exit = %v, context %v, stdout %q, terminal %q", err, ctx.Err(), stdout.String(), screen.String())
			}
			for _, status := range test.wantResult {
				if !strings.Contains(stdout.String(), status) {
					t.Errorf("stdout is missing %q: %q", status, stdout.String())
				}
			}
			assertNPMOpenLog(t, log, test.wantLog)
			if rich {
				waitForNPMPrompt(t, ctx, process, &screen, "\x1b[?1049l", false)
				waitForNPMPrompt(t, ctx, process, &screen, "\x1b[?1002l", false)
			}
		})
	}
	t.Run("rich long checklist scrolls and keeps every result", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		workspace := t.TempDir()
		writeStandaloneZIPFile(t, workspace, "package.json", `{"private":true,"workspaces":["packages/*"]}`)
		for index := 0; index < 72; index++ {
			name := fmt.Sprintf("scroll-package-%02d", index)
			if index == 71 {
				name = "workspace-last-package"
			}
			writeStandaloneZIPFile(t, workspace, filepath.Join("packages", name, "package.json"), fmt.Sprintf(`{"name":%q}`, name))
		}
		log := filepath.Join(t.TempDir(), "open.log")
		command := newCommand(ctx, log, "xterm-256color", "")
		command.Dir = workspace
		var stdout bytes.Buffer
		command.Stdout = &stdout
		process, err := terminaltest.StartPTYWithSize(command, 60, 13)
		if err != nil {
			if errors.Is(err, terminaltest.ErrPTYUnsupported) {
				t.Skip(err)
			}
			t.Fatal(err)
		}
		var screen npmPTYBuffer
		readDone := make(chan struct{})
		go func() {
			_, _ = io.Copy(&screen, process.Terminal())
			close(readDone)
		}()
		defer func() { _ = process.Close(); <-readDone }()
		send := func(keys string) {
			t.Helper()
			if _, err := io.WriteString(process.Terminal(), keys); err != nil {
				t.Fatal(err)
			}
		}
		waitForNPMPrompt(t, ctx, process, &screen, "scroll-package-00", true)
		send("\r")
		waitForNPMPrompt(t, ctx, process, &screen, "Enter [Open next]", false)
		if strings.Contains(screen.String(), "workspace-last-package") {
			t.Fatal("last row was shown before scrolling")
		}
		send("\x1b[F") // End scrolls to the final row without changing the next package.
		waitForNPMPrompt(t, ctx, process, &screen, "workspace-last-package", false)
		assertNPMOpenLog(t, log, "")
		send("\r")
		waitForNPMPrompt(t, ctx, process, &screen, "Opened 1 · Pending 71", false)
		send("\x1b[D\r") // Choose the Stop button rather than opening the next package.
		if err := process.Wait(); err != nil {
			t.Fatalf("stop = %v; terminal %q", err, screen.String())
		}
		assertNPMOpenLog(t, log, "https://www.npmjs.com/package/scroll-package-00/access\n")
		result := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(result) != 73 || !strings.Contains(result[1], "Opened        scroll-package-00") || !strings.Contains(result[72], "Pending       workspace-last-package") {
			t.Fatalf("final result did not retain all 72 package statuses: %q", stdout.String())
		}
		waitForNPMPrompt(t, ctx, process, &screen, "\x1b[?1049l", false)
	})
}

func assertNPMOpenLog(t *testing.T, file, want string) {
	t.Helper()
	if got, err := os.ReadFile(file); (err != nil && !(want == "" && errors.Is(err, os.ErrNotExist))) || string(got) != want {
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
