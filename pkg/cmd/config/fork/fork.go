package fork

import (
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/fork/add"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/fork/list"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/fork/remove"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdFork creates the registration-only config fork parent.
func NewCmdFork(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "fork",
		Short: "Manage git fork provider instances",
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
		add.NewCmdAdd(factory, nil),
		remove.NewCmdRemove(factory, nil),
	)
	return command
}

type parentHelpError struct{}

func (parentHelpError) Error() string { return "config parent help requested" }

func (parentHelpError) ExitCode() int { return 1 }

var _ error = parentHelpError{}
