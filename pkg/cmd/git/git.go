// Package git registers the public Git command group.
package git

import (
	"github.com/hackycy/hackycy-cli/pkg/cmd/git/cm"
	"github.com/hackycy/hackycy-cli/pkg/cmd/git/fork"
	"github.com/hackycy/hackycy-cli/pkg/cmd/git/heat"
	"github.com/hackycy/hackycy-cli/pkg/cmd/git/pulse"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdGit creates the registration-only Git command parent.
func NewCmdGit(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "git",
		Short: "Git utilities",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Help(); err != nil {
				return err
			}
			return errHelpRequested
		},
	}
	command.AddCommand(heat.NewCmdHeat(factory, nil))
	command.AddCommand(pulse.NewCmdPulse(factory, nil))
	command.AddCommand(fork.NewCmdFork(factory, nil))
	command.AddCommand(cm.NewCmdCM(factory, nil))
	return command
}

var errHelpRequested = &helpRequestedError{}

type helpRequestedError struct{}

func (*helpRequestedError) Error() string {
	return "help requested"
}

func (*helpRequestedError) ExitCode() int {
	return 1
}
