package ycycmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	"golang.org/x/sys/windows"
)

func TestProcessConsoleRestoredAfterHelpAndErrors(t *testing.T) {
	const helperKey = "YCY_PROCESS_CONPTY_HELPER"
	const reportKey = "YCY_PROCESS_CONPTY_REPORT"
	if scenario := os.Getenv(helperKey); scenario != "" {
		mode := func(file *os.File) uint32 {
			var value uint32
			if err := windows.GetConsoleMode(windows.Handle(file.Fd()), &value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		for _, file := range []*os.File{os.Stdout, os.Stderr} {
			if err := windows.SetConsoleMode(windows.Handle(file.Fd()), mode(file) & ^uint32(windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)); err != nil {
				t.Fatal(err)
			}
		}
		before := [3]uint32{mode(os.Stdin), mode(os.Stdout), mode(os.Stderr)}
		arguments, wantCode := []string{"--help"}, 0
		if scenario == "root-error" {
			arguments, wantCode = []string{"unknown"}, 1
		}
		if scenario == "startup-error" {
			arguments, wantCode = []string{"--help"}, 1
		}
		version := "0.0.0-dev"
		if scenario == "startup-error" {
			version = ""
		}
		if code := run(version, arguments, os.Stdin, os.Stdout, os.Stderr); code != wantCode {
			t.Fatalf("process code = %d, want %d", code, wantCode)
		}
		after := [3]uint32{mode(os.Stdin), mode(os.Stdout), mode(os.Stderr)}
		if after != before {
			t.Fatalf("process console modes: %x -> %x", before, after)
		}
		encoded, err := json.Marshal(after)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv(reportKey), encoded, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, scenario := range []string{"help", "root-error", "startup-error"} {
		t.Run(scenario, func(t *testing.T) {
			reportPath := filepath.Join(t.TempDir(), "modes.json")
			command := exec.Command(os.Args[0], "-test.run=^TestProcessConsoleRestoredAfterHelpAndErrors$")
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				switch strings.ToUpper(key) {
				case "TERM", "NO_COLOR", "FORCE_COLOR", "CI", "CLICOLOR", "CLICOLOR_FORCE", "TTY_FORCE":
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env, helperKey+"="+scenario, reportKey+"="+reportPath)
			process, err := terminaltest.StartConPTY(command, 120, 40)
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := process.Wait(ctx); err != nil {
				t.Fatalf("process: %v\n%s", err, process.Output())
			}
			process.Close()
			if _, err := os.ReadFile(reportPath); err != nil {
				t.Fatalf("process did not restore console: %v\n%s", err, process.Output())
			}
			text := process.Output()
			if scenario == "help" && (!strings.Contains(terminaltest.StripANSI(text), "Usage:") || !strings.Contains(terminaltest.StyleSequences(text), "38;")) {
				t.Fatalf("help lost native color without TERM: %q", text)
			}
			if scenario != "help" && !strings.Contains(text, "error:") {
				t.Fatalf("missing process diagnostics: %q", text)
			}
		})
	}
}
