// Package terminal owns terminal capability and rendering behavior.
package terminal

import "strings"

// InteractionMode describes how a command may interact with its caller.
type InteractionMode uint8

const (
	// Automation never reads stdin and never emits terminal control sequences.
	Automation InteractionMode = iota
	// PlainInteractive supports line-oriented interaction without terminal control.
	PlainInteractive
	// RichInteractive supports a full-screen terminal controller on stderr.
	RichInteractive
)

// StreamCapability records independent behavior for one inherited stream.
type StreamCapability struct {
	Terminal bool
	Color    bool
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
	color := colorEnabled(facts)
	capabilities := Capabilities{
		Stdin:  StreamCapability{Terminal: facts.Stdin.Terminal},
		Stdout: StreamCapability{Terminal: facts.Stdout.Terminal, Color: facts.Stdout.Terminal && color},
		Stderr: StreamCapability{Terminal: facts.Stderr.Terminal, Color: facts.Stderr.Terminal && color},
	}
	if !facts.Stdin.Terminal || !facts.Stderr.Terminal || facts.isSet("CI") {
		capabilities.Interaction = Automation
		capabilities.Stdout.Color = false
		capabilities.Stderr.Color = false
		return capabilities
	}

	term, _ := facts.lookup("TERM")
	if supportsRichControls(term) {
		capabilities.Interaction = RichInteractive
		return capabilities
	}
	capabilities.Interaction = PlainInteractive
	capabilities.Stdout.Color = false
	capabilities.Stderr.Color = false
	return capabilities
}

func colorEnabled(facts Facts) bool {
	noColor, set := facts.lookup("NO_COLOR")
	return !set || noColor == ""
}

func (facts Facts) isSet(key string) bool {
	_, set := facts.lookup(key)
	return set
}

func (facts Facts) lookup(key string) (string, bool) {
	if facts.LookupEnv == nil {
		return "", false
	}
	return facts.LookupEnv(key)
}

func supportsRichControls(term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	for _, family := range []string{
		"alacritty",
		"foot",
		"iterm",
		"kitty",
		"konsole",
		"rxvt",
		"screen",
		"st",
		"tmux",
		"vte",
		"wezterm",
		"xterm",
	} {
		if term == family || strings.HasPrefix(term, family+"-") {
			return true
		}
	}
	return false
}
