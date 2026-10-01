package terminal_test

import (
	"os"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
)

func TestInvocationPipePolicyAndCapturedEnvironment(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, test := range []struct {
		name        string
		environment []string
		want        terminal.ColorProfile
	}{
		{name: "auto", environment: []string{"TERM=xterm-256color"}},
		{name: "forced", environment: []string{"TERM=xterm-256color", "FORCE_COLOR=1"}, want: terminal.ANSI256},
		{name: "NO_COLOR zero", environment: []string{"TERM=xterm-256color", "NO_COLOR=0", "FORCE_COLOR=1"}},
		{name: "NO_COLOR arbitrary", environment: []string{"TERM=xterm-256color", "NO_COLOR=custom", "FORCE_COLOR=1"}},
		{name: "empty NO_COLOR", environment: []string{"TERM=xterm-256color", "NO_COLOR=", "FORCE_COLOR=1"}, want: terminal.ANSI256},
		{name: "library overrides ignored", environment: []string{"TERM=xterm-256color", "TTY_FORCE=1", "CLICOLOR_FORCE=1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			invocation, err := terminal.PrepareInvocation(file, file, file, test.environment)
			if err != nil {
				t.Fatal(err)
			}
			defer invocation.Close()
			capabilities := invocation.Capabilities
			if capabilities.Interaction != terminal.Automation || capabilities.Stdout.Terminal || capabilities.Stdout.Controls || capabilities.Stderr.Terminal || capabilities.Stderr.Controls {
				t.Fatalf("pipe acquired terminal permission: %#v", capabilities)
			}
			if capabilities.Stdout.Profile != test.want || capabilities.Stderr.Profile != test.want {
				t.Fatalf("pipe profiles = %#v, want %v", capabilities, test.want)
			}
			captured := invocation.Environment[0]
			test.environment[0] = "TERM=dumb"
			if invocation.Environment[0] != captured {
				t.Fatal("environment was not captured")
			}
			if err := invocation.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
