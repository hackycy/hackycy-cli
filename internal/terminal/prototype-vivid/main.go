// Command prototype-vivid is a throwaway PTY prototype for a ycy visual-system decision.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

func main() {
	variantFlag := flag.String("variant", "console", "console (default), signal, or focus")
	outcomeFlag := flag.String("outcome", "success", "success, failure, or cancel")
	flag.Parse()

	selectedVariant, err := parseVariant(*variantFlag)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	selectedOutcome, err := parseOutcome(*outcomeFlag)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	initial := newModel(selectedVariant, selectedOutcome)
	result, runErr := tea.NewProgram(
		initial,
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stderr),
		tea.WithEnvironment(os.Environ()),
	).Run()
	if runErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "prototype renderer failed: %v\n", runErr)
		os.Exit(1)
	}

	final := result.(*model)
	diagnostics := colorprofile.NewWriter(os.Stderr, os.Environ())
	_, _ = diagnostics.WriteString(final.renderTranscript())
	if final.succeeded() {
		_, _ = fmt.Fprintf(os.Stdout, "profile %s configured\n", final.profileName())
	}
}
