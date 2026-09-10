// Package terminaltest provides deterministic terminal fixtures for tests.
package terminaltest

// Stream identifies one inherited standard stream.
type Stream string

const (
	// Stdin is the process standard-input stream.
	Stdin Stream = "stdin"
	// Stdout is the process standard-output stream.
	Stdout Stream = "stdout"
	// Stderr is the process standard-error stream.
	Stderr Stream = "stderr"
)

// Size is the observable size of a terminal stream.
type Size struct {
	Width  int
	Height int
}

// StreamFacts describes a stream without consulting the test process terminal.
type StreamFacts struct {
	Terminal bool
	Size     Size
}

// Facts supplies injected stream and environment facts to terminal tests.
// Environment preserves the distinction between an unset variable and a set,
// empty variable.
type Facts struct {
	Stdin       StreamFacts
	Stdout      StreamFacts
	Stderr      StreamFacts
	Environment map[string]string
}

// Stream returns the facts for one standard stream. Unknown streams are not
// terminals, which keeps test defaults conservative.
func (facts Facts) Stream(stream Stream) StreamFacts {
	switch stream {
	case Stdin:
		return facts.Stdin
	case Stdout:
		return facts.Stdout
	case Stderr:
		return facts.Stderr
	default:
		return StreamFacts{}
	}
}

// LookupEnv mirrors os.LookupEnv against the injected environment.
func (facts Facts) LookupEnv(key string) (string, bool) {
	value, ok := facts.Environment[key]
	return value, ok
}
