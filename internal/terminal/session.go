// Package terminal owns terminal capability and rendering behavior.
package terminal

import "strings"

// InteractionMode describes how a command may interact with its caller.
type InteractionMode uint8

const (
	// Automation never reads stdin or controls the screen. Text may still be colored.
	Automation InteractionMode = iota
	// PlainInteractive supports line-oriented interaction without screen control.
	PlainInteractive
	// RichInteractive supports a full-screen terminal controller on stderr.
	RichInteractive
)

// StreamCapability records independent behavior for one inherited stream.
type StreamCapability struct {
	Terminal bool
	Controls bool
	Profile  ColorProfile
}

// Capabilities are the immutable terminal capabilities selected for an invocation.
// Interaction is determined by stdin and stderr; stdout remains independent so
// durable results can be redirected without disabling a rich UI on stderr.
type Capabilities struct {
	Interaction InteractionMode
	Stdin       StreamCapability
	Stdout      StreamCapability
	Stderr      StreamCapability
}

// StreamFacts are the observable capability facts for one inherited stream.
type StreamFacts struct {
	Terminal bool
	Controls bool
	Profile  ColorProfile
}

// LookupEnv looks up one environment variable while preserving whether it is set.
type LookupEnv func(string) (string, bool)

// Facts provides all capability inputs without consulting the process terminal.
type Facts struct {
	Stdin     StreamFacts
	Stdout    StreamFacts
	Stderr    StreamFacts
	LookupEnv LookupEnv
}

// Classify selects terminal behavior from injected stream and environment facts.
func Classify(facts Facts) Capabilities {
	term, _ := facts.lookup("TERM")
	dumb := strings.EqualFold(strings.TrimSpace(term), "dumb")
	noColor, _ := facts.lookup("NO_COLOR")
	forceColor, _ := facts.lookup("FORCE_COLOR")
	output := func(stream StreamFacts) StreamCapability {
		capability := StreamCapability{Terminal: stream.Terminal, Controls: stream.Terminal && stream.Controls && !dumb}
		if noColor == "" && (capability.Controls || (forceColor != "" && (!stream.Terminal || stream.Controls))) {
			capability.Profile = stream.Profile
			if capability.Profile == NoColor {
				capability.Profile = ANSI16
			}
		}
		return capability
	}
	capabilities := Capabilities{
		Stdin:  StreamCapability{Terminal: facts.Stdin.Terminal},
		Stdout: output(facts.Stdout),
		Stderr: output(facts.Stderr),
	}
	ci, _ := facts.lookup("CI")
	ci = strings.ToLower(strings.TrimSpace(ci))
	if !facts.Stdin.Terminal || !facts.Stderr.Terminal || (ci != "" && ci != "0" && ci != "false") {
		capabilities.Interaction = Automation
		return capabilities
	}
	if capabilities.Stderr.Controls {
		capabilities.Interaction = RichInteractive
		return capabilities
	}
	capabilities.Interaction = PlainInteractive
	return capabilities
}

func (facts Facts) lookup(key string) (string, bool) {
	if facts.LookupEnv == nil {
		return "", false
	}
	return facts.LookupEnv(key)
}
