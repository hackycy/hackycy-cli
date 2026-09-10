package config

import (
	cmcommand "github.com/hackycy/hackycy-cli/pkg/cmd/config/cm"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/fork"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdConfig creates the config parent and registers both nested groups.
func NewCmdConfig(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Manage ycy configuration",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Help(); err != nil {
				return err
			}
			return configHelpError{}
		},
	}
	command.AddCommand(
		fork.NewCmdFork(factory),
		cmcommand.NewCmdCM(factory),
	)
	return command
}

type configHelpError struct{}

func (configHelpError) Error() string { return "config help requested" }

func (configHelpError) ExitCode() int { return 1 }
