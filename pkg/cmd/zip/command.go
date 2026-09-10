package zip

import (
	"context"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options contains the parsed zip request and its leaf-owned dependencies.
type Options struct {
	Context   context.Context
	Directory string
	Open      bool
	WithDir   string
	Terminal  *terminal.Runtime
}

// NewCmdZIP creates the zip command with an optional test runner.
func NewCmdZIP(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runZIP
	}
	var withoutOpen bool
	var withDir string
	command := &cobra.Command{
		Use:   "zip [directory]",
		Short: "Zip a directory into a zip file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			directory := ""
			if len(arguments) == 1 {
				directory = arguments[0]
			}
			return runF(&Options{
				Context:   command.Context(),
				Directory: directory,
				Open:      !withoutOpen,
				WithDir:   withDir,
				Terminal:  factory.Terminal,
			})
		},
	}
	command.Flags().BoolVarP(&withoutOpen, "without-open", "w", false, "Do not open the zip file after creation")
	command.Flags().StringVarP(&withDir, "with-dir", "d", "", "Include the directory name as a top-level folder in the zip")
	return command
}
