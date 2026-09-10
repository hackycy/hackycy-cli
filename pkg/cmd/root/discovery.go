package root

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// DiscoveryPresenter receives a Cobra-owned command discovery document for
// terminal-specific durable presentation.
type DiscoveryPresenter interface {
	PresentDiscovery(context.Context, DiscoveryDocument) error
}

// DiscoveryDocument describes one command without exposing Cobra to a terminal adapter.
type DiscoveryDocument struct {
	CommandPath string
	Summary     string
	Usage       string
	Descendants []DiscoveryDescendant
	Flags       []DiscoveryFlag
	Examples    []string
}

// DiscoveryDescendant describes one direct, invocable child command.
type DiscoveryDescendant struct {
	Name    string
	Summary string
}

// DiscoveryFlag describes one command-local or inherited flag.
type DiscoveryFlag struct {
	Name      string
	Shorthand string
	Usage     string
}

func newDiscoveryDocument(command *cobra.Command) DiscoveryDocument {
	document := DiscoveryDocument{
		CommandPath: command.CommandPath(),
		Summary:     command.Short,
		Usage:       command.UseLine(),
		Examples:    discoveryExamples(command.Example),
	}
	for _, child := range command.Commands() {
		if child.IsAvailableCommand() {
			document.Descendants = append(document.Descendants, DiscoveryDescendant{
				Name:    child.Name(),
				Summary: child.Short,
			})
		}
	}
	document.Flags = appendDiscoveryFlags(document.Flags, command.LocalFlags())
	document.Flags = appendDiscoveryFlags(document.Flags, command.InheritedFlags())
	return document
}

func appendDiscoveryFlags(flags []DiscoveryFlag, source *pflag.FlagSet) []DiscoveryFlag {
	source.VisitAll(func(flag *pflag.Flag) {
		flags = append(flags, DiscoveryFlag{
			Name:      flag.Name,
			Shorthand: flag.Shorthand,
			Usage:     flag.Usage,
		})
	})
	return flags
}

func discoveryExamples(value string) []string {
	lines := strings.Split(value, "\n")
	examples := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			examples = append(examples, line)
		}
	}
	return examples
}
