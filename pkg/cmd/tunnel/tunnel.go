package tunnel

import (
	"github.com/hackycy/hackycy-cli/pkg/cmd/tunnel/connect"
	tunnelserver "github.com/hackycy/hackycy-cli/pkg/cmd/tunnel/server"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdTunnel creates the registration-only Tunnel command parent.
func NewCmdTunnel(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage trusted tunnel clients and tunnel definitions",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Help(); err != nil {
				return err
			}
			return tunnelHelpError{}
		},
	}
	command.AddCommand(
		tunnelserver.NewCmdServer(factory, nil),
		connect.NewCmdConnect(factory, nil),
	)
	return command
}

type tunnelHelpError struct{}

func (tunnelHelpError) Error() string { return "tunnel help requested" }

func (tunnelHelpError) ExitCode() int { return 1 }
