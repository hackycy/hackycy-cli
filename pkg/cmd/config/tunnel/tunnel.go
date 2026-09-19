package tunnel

import (
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/tunnel/list"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/tunnel/remove"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdTunnel creates the registration-only local Tunnel configuration parent.
func NewCmdTunnel(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage remembered Tunnel connections",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Help(); err != nil {
				return err
			}
			return parentHelpError{}
		},
	}
	command.AddCommand(
		list.NewCmdList(factory, nil),
		remove.NewCmdRemove(factory, nil),
	)
	return command
}

type parentHelpError struct{}

func (parentHelpError) Error() string { return "config tunnel help requested" }

func (parentHelpError) ExitCode() int { return 1 }
