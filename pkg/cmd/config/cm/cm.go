package cm

import (
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/cm/add"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/cm/list"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/cm/remove"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/cm/set"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/cm/test"
	"github.com/hackycy/hackycy-cli/pkg/cmd/config/cm/use"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdCM creates the registration-only config cm parent.
func NewCmdCM(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "cm",
		Short: "Manage CM profiles",
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
		use.NewCmdUse(factory, nil),
		set.NewCmdSet(factory, nil),
		remove.NewCmdRemove(factory, nil),
		test.NewCmdTest(factory, nil),
	)
	return command
}

type parentHelpError struct{}

func (parentHelpError) Error() string { return "config cm parent help requested" }

func (parentHelpError) ExitCode() int { return 1 }
