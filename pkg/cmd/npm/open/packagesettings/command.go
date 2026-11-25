// Package packagesettings opens npm package settings in the default browser.
package packagesettings

import (
	"context"
	"errors"
	"io"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options holds the inputs and dependencies for one invocation.
type Options struct {
	Context          context.Context
	Batch            bool
	WorkingDirectory func() (string, error)
	Output           io.Writer
	Terminal         *terminal.Runtime
	OpenURL          func(context.Context, string) error
}

// NewCmdPackageSettings creates the npm package-settings command.
func NewCmdPackageSettings(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runPackageSettings
	}
	var batch bool
	command := &cobra.Command{
		Use:   "package-settings",
		Short: "Open npm package settings from the current project or workspace",
		Example: "ycy npm open package-settings\n" +
			"ycy npm open package-settings --batch",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.WorkingDirectory == nil || factory.IOStreams.Out == nil || factory.Terminal == nil {
				return errors.New("npm open package-settings Factory is incomplete")
			}
			return runF(&Options{
				Context:          command.Context(),
				Batch:            batch,
				WorkingDirectory: factory.WorkingDirectory,
				Output:           factory.IOStreams.Out,
				Terminal:         factory.Terminal,
				OpenURL:          openURL,
			})
		},
	}
	command.Flags().BoolVar(&batch, "batch", false, "Open all listed packages without prompting")
	return command
}
