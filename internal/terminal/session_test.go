package terminal_test

import (
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"testing"
)

func TestClassify(t *testing.T) {
	for _, test := range []struct {
		name                     string
		environment              map[string]string
		change                   func(*terminal.Facts)
		mode                     terminal.InteractionMode
		stdout, stderr           terminal.ColorProfile
		outControls, errControls bool
	}{
		{name: "ordinary terminal", mode: terminal.RichInteractive, stdout: terminal.ANSI256, stderr: terminal.ANSI256, outControls: true, errControls: true},
		{name: "unknown TERM", environment: map[string]string{"TERM": "custom-terminal"}, mode: terminal.RichInteractive, stdout: terminal.ANSI256, stderr: terminal.ANSI256, outControls: true, errControls: true},
		{name: "NO_COLOR", environment: map[string]string{"NO_COLOR": "1"}, mode: terminal.RichInteractive, outControls: true, errControls: true},
		{name: "NO_COLOR zero", environment: map[string]string{"NO_COLOR": "0"}, mode: terminal.RichInteractive, outControls: true, errControls: true},
		{name: "NO_COLOR arbitrary", environment: map[string]string{"NO_COLOR": "anything"}, mode: terminal.RichInteractive, outControls: true, errControls: true},
		{name: "NO_COLOR empty", environment: map[string]string{"NO_COLOR": ""}, mode: terminal.RichInteractive, stdout: terminal.ANSI256, stderr: terminal.ANSI256, outControls: true, errControls: true},
		{name: "NO_COLOR wins force", environment: map[string]string{"NO_COLOR": "0", "FORCE_COLOR": "1"}, mode: terminal.RichInteractive, outControls: true, errControls: true},
		{name: "dumb", environment: map[string]string{"TERM": "dumb"}, mode: terminal.PlainInteractive},
		{name: "force on dumb", environment: map[string]string{"TERM": "dumb", "FORCE_COLOR": "1"}, mode: terminal.PlainInteractive, stdout: terminal.ANSI256, stderr: terminal.ANSI256},
		{name: "stdin redirected", change: func(f *terminal.Facts) { f.Stdin.Terminal = false }, mode: terminal.Automation, stdout: terminal.ANSI256, stderr: terminal.ANSI256, outControls: true, errControls: true},
		{name: "stdout redirected", change: func(f *terminal.Facts) { f.Stdout.Terminal = false }, mode: terminal.RichInteractive, stderr: terminal.ANSI256, errControls: true},
		{name: "stderr redirected", change: func(f *terminal.Facts) { f.Stderr.Terminal = false }, mode: terminal.Automation, stdout: terminal.ANSI256, outControls: true},
		{name: "all redirected", change: func(f *terminal.Facts) { f.Stdin.Terminal, f.Stdout.Terminal, f.Stderr.Terminal = false, false, false }, mode: terminal.Automation},
		{name: "forced pipes", environment: map[string]string{"FORCE_COLOR": "1"}, change: func(f *terminal.Facts) { f.Stdin.Terminal, f.Stdout.Terminal, f.Stderr.Terminal = false, false, false }, mode: terminal.Automation, stdout: terminal.ANSI256, stderr: terminal.ANSI256},
		{name: "empty force on pipes", environment: map[string]string{"FORCE_COLOR": ""}, change: func(f *terminal.Facts) { f.Stdin.Terminal, f.Stdout.Terminal, f.Stderr.Terminal = false, false, false }, mode: terminal.Automation},
		{name: "CI active", environment: map[string]string{"CI": "1"}, mode: terminal.Automation, stdout: terminal.ANSI256, stderr: terminal.ANSI256, outControls: true, errControls: true},
		{name: "CI empty", environment: map[string]string{"CI": ""}, mode: terminal.RichInteractive, stdout: terminal.ANSI256, stderr: terminal.ANSI256, outControls: true, errControls: true},
		{name: "CI zero", environment: map[string]string{"CI": " 0 "}, mode: terminal.RichInteractive, stdout: terminal.ANSI256, stderr: terminal.ANSI256, outControls: true, errControls: true},
		{name: "CI false", environment: map[string]string{"CI": " False "}, mode: terminal.RichInteractive, stdout: terminal.ANSI256, stderr: terminal.ANSI256, outControls: true, errControls: true},
		{name: "unsupported output backend", change: func(f *terminal.Facts) { f.Stdout.Controls, f.Stderr.Controls = false, false }, mode: terminal.PlainInteractive},
		{name: "force cannot enable unsupported console", environment: map[string]string{"FORCE_COLOR": "1"}, change: func(f *terminal.Facts) { f.Stdout.Controls, f.Stderr.Controls = false, false }, mode: terminal.PlainInteractive},
		{name: "independent profiles", change: func(f *terminal.Facts) { f.Stdout.Profile, f.Stderr.Profile = terminal.ANSI16, terminal.TrueColor }, mode: terminal.RichInteractive, stdout: terminal.ANSI16, stderr: terminal.TrueColor, outControls: true, errControls: true},
		{name: "unknown depth fallback", change: func(f *terminal.Facts) { f.Stdout.Profile, f.Stderr.Profile = terminal.NoColor, terminal.NoColor }, mode: terminal.RichInteractive, stdout: terminal.ANSI16, stderr: terminal.ANSI16, outControls: true, errControls: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := terminal.StreamFacts{Terminal: true, Controls: true, Profile: terminal.ANSI256}
			facts := terminal.Facts{Stdin: stream, Stdout: stream, Stderr: stream, LookupEnv: func(key string) (string, bool) { value, ok := test.environment[key]; return value, ok }}
			if test.change != nil {
				test.change(&facts)
			}
			want := terminal.Capabilities{
				Interaction: test.mode,
				Stdin:       terminal.StreamCapability{Terminal: facts.Stdin.Terminal},
				Stdout:      terminal.StreamCapability{Terminal: facts.Stdout.Terminal, Controls: test.outControls, Profile: test.stdout},
				Stderr:      terminal.StreamCapability{Terminal: facts.Stderr.Terminal, Controls: test.errControls, Profile: test.stderr},
			}
			if got := terminal.Classify(facts); got != want {
				t.Fatalf("Classify() = %#v, want %#v", got, want)
			}
		})
	}
}
