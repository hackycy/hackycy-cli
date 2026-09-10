// Package ycycmd owns process-level orchestration for the ycy binary.
package ycycmd

import (
	"os"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"golang.org/x/term"
)

// ProcessFacts are the inherited process streams and terminal capability used
// to construct the command Factory. The values are captured once per process.
type ProcessFacts struct {
	IOStreams    cmdutil.IOStreams
	Capabilities terminalexperience.Capabilities
}

// CurrentProcessFacts captures the real standard streams and environment.
func CurrentProcessFacts() ProcessFacts {
	return NewProcessFacts(os.Stdin, os.Stdout, os.Stderr, os.LookupEnv, isTerminal)
}

// NewProcessFacts classifies an invocation from explicit process facts. Tests
// use this boundary to avoid consulting the host terminal or environment.
func NewProcessFacts(input, output, diagnostics *os.File, lookup terminalexperience.LookupEnv, terminal func(*os.File) bool) ProcessFacts {
	return ProcessFacts{
		IOStreams: cmdutil.IOStreams{
			In:     input,
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminalexperience.Classify(terminalexperience.Facts{
			Stdin:     terminalexperience.StreamFacts{Terminal: terminal(input)},
			Stdout:    terminalexperience.StreamFacts{Terminal: terminal(output)},
			Stderr:    terminalexperience.StreamFacts{Terminal: terminal(diagnostics)},
			LookupEnv: lookup,
		}),
	}
}

func isTerminal(file *os.File) bool {
	return file != nil && term.IsTerminal(int(file.Fd()))
}
