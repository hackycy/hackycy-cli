package terminal

import (
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
)

// Invocation owns output preparation until all command output has completed.
// UI runs independently own their temporary raw-input and screen lifecycle.
type Invocation struct {
	Capabilities Capabilities
	Environment  []string
	restore      func() error
	once         sync.Once
	closeErr     error
}

// PrepareInvocation observes actual streams before any renderer/lease wrapping.
// Unsupported native output controls degrade to plain output.
func PrepareInvocation(input, output, diagnostics *os.File, environment []string) (*Invocation, error) {
	invocation := &Invocation{Environment: append([]string{}, environment...)}
	stdoutControls, stderrControls, restore, err := prepareOutputs(output, diagnostics)
	if err != nil {
		return nil, err
	}
	invocation.restore = restore
	normalized := normalizedColorEnvironment(invocation.Environment)
	observe := func(file *os.File, controls bool) StreamFacts {
		stream := StreamFacts{Terminal: IsTerminal(file), Controls: controls}
		if stream.Terminal && controls {
			stream.Profile = fromLibraryProfile(colorprofile.Detect(file, normalized))
		} else if !stream.Terminal {
			stream.Profile = fromLibraryProfile(colorprofile.Env(normalized))
		}
		return stream
	}
	invocation.Capabilities = Classify(Facts{
		Stdin:     StreamFacts{Terminal: IsTerminal(input)},
		Stdout:    observe(output, stdoutControls),
		Stderr:    observe(diagnostics, stderrControls),
		LookupEnv: environmentLookup(invocation.Environment),
	})
	return invocation, nil
}

// Close restores inherited output modes exactly once, including shared buffers.
func (invocation *Invocation) Close() error {
	invocation.once.Do(func() {
		if invocation.restore != nil {
			invocation.closeErr = invocation.restore()
		}
	})
	return invocation.closeErr
}

// IsTerminal observes an actual native terminal, not merely a character device.
func IsTerminal(file *os.File) bool {
	return file != nil && term.IsTerminal(int(file.Fd()))
}

func environmentLookup(environment []string) LookupEnv {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[environmentKey(key)] = value
		}
	}
	return func(key string) (string, bool) {
		value, ok := values[environmentKey(key)]
		return value, ok
	}
}
