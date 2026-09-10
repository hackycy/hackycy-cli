package export

import (
	"github.com/hackycy/hackycy-cli/pkg/cmd/export/env"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdExport creates the registration-only export command parent.
func NewCmdExport(factory *cmdutil.Factory) *cobra.Command {
	command := &cobra.Command{
		Use:   "export",
		Short: "Export command output",
	}
	command.AddCommand(env.NewCmdEnv(factory, nil))
	return command
}
