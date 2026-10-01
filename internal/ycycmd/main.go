package ycycmd

import (
	"context"
	"fmt"
	"os"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	rootcommand "github.com/hackycy/hackycy-cli/pkg/cmd/root"
	"github.com/hackycy/hackycy-cli/web"
)

// Main runs one ycy process and returns the exit status to the binary entry.
// All process orchestration stays here so cmd/ycy only owns os.Exit.
func Main(version string) int {
	return run(version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

func run(version string, arguments []string, input, output, diagnostics *os.File) (code int) {
	if handled, err := RunHiddenUpgrade(arguments); handled {
		if err != nil {
			// The hidden child has no parent presentation surface. Keep its one
			// diagnostic line fixed so filesystem paths and raw process errors do
			// not escape before the next startup can consume the safe state.
			_, _ = fmt.Fprintln(diagnostics, "error: detached updater failed")
			return 1
		}
		return 0
	}

	if handled, err := DispatchThumbnailWorker(arguments, input, output); handled {
		if err != nil {
			_, _ = fmt.Fprintf(diagnostics, "thumbnail worker error: %s\n", err)
			return 1
		}
		return 0
	}

	invocation, err := terminal.PrepareInvocation(input, output, diagnostics, os.Environ())
	if err != nil {
		_, _ = fmt.Fprintf(diagnostics, "error: %s\n", err)
		return 1
	}
	defer func() {
		if err := invocation.Close(); err != nil {
			_, _ = fmt.Fprintf(diagnostics, "error: restore terminal: %s\n", err)
			if code == 0 {
				code = 1
			}
		}
	}()
	processFacts := NewProcessFacts(input, output, diagnostics, invocation.Capabilities)
	commandFactory := commandfactory.New(commandfactory.Options{
		Version:             version,
		IOStreams:           processFacts.IOStreams,
		Capabilities:        processFacts.Capabilities,
		TerminalEnvironment: invocation.Environment,
		Environment:         os.Getenv,
		EnvironmentLookup:   os.LookupEnv,
	})
	normalDiagnostics := commandFactory.Terminal.DiagnosticWriter()
	if err := ConsumeUpgradeStartup(arguments, commandFactory.Terminal); err != nil {
		_, _ = fmt.Fprintf(normalDiagnostics, "error: %s\n", err)
		return 1
	}

	if err := webassets.Validate(); err != nil {
		_, _ = fmt.Fprintln(processFacts.IOStreams.Out)
		_, _ = fmt.Fprintf(normalDiagnostics, "error: %s\n", err)
		return 1
	}

	ctx, stop := NewSignalContext(context.Background())
	defer stop()
	app, err := rootcommand.New(commandFactory)
	if err != nil {
		_, _ = fmt.Fprintln(processFacts.IOStreams.Out)
		_, _ = fmt.Fprintf(normalDiagnostics, "error: %s\n", err)
		return 1
	}
	result := app.Execute(ctx, arguments)
	return result.Code
}
