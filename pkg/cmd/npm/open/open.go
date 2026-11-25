// Package open registers browser destinations on the npm website.
package open

import (
	"github.com/hackycy/hackycy-cli/pkg/cmd/npm/open/packagesettings"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdOpen registers the npm browser navigation group.
func NewCmdOpen(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "open",
		Short: "Open pages on the npm website",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Help(); err != nil {
				return err
			}
			return helpRequested{}
		},
	}
	command.AddCommand(packagesettings.NewCmdPackageSettings(factory, nil))
	return command
}

type helpRequested struct{}

func (helpRequested) Error() string { return "npm open help requested" }
func (helpRequested) ExitCode() int { return 1 }
