// Package npm registers the public npm command group.
package npm

import (
	"github.com/hackycy/hackycy-cli/pkg/cmd/npm/open"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdNPM registers the npm command group.
func NewCmdNPM(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "npm",
		Short: "npm utilities",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Help(); err != nil {
				return err
			}
			return helpRequested{}
		},
	}
	command.AddCommand(open.NewCmdOpen(factory))
	return command
}

type helpRequested struct{}

func (helpRequested) Error() string { return "npm help requested" }
func (helpRequested) ExitCode() int { return 1 }
