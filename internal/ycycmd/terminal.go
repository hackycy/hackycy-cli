// Package ycycmd owns process-level orchestration for the ycy binary.
package ycycmd

import (
	"os"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

// ProcessFacts are the inherited process streams and terminal capability used
// to construct the command Factory. The values are captured once per process.
type ProcessFacts struct {
	IOStreams    cmdutil.IOStreams
	Capabilities terminalexperience.Capabilities
}

// NewProcessFacts preserves inherited streams and terminal-owned capabilities.
func NewProcessFacts(input, output, diagnostics *os.File, capabilities terminalexperience.Capabilities) ProcessFacts {
	return ProcessFacts{
		IOStreams: cmdutil.IOStreams{
			In:     input,
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: capabilities,
	}
}
