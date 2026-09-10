package env

import (
	"context"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options contains the parsed export env request and leaf-owned dependencies.
type Options struct {
	Context          context.Context
	Directory        string
	Environment      string
	Merge            bool
	Output           string
	WorkingDirectory func() (string, error)
	Terminal         *terminal.Runtime
	Reader           FileReader
	Writer           FileWriter
}

// NewCmdEnv creates the export env command with an optional test runner.
func NewCmdEnv(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runEnv
	}
	var environment string
	var merge bool
	var output string

	envCommand := &cobra.Command{
		Use:   "env [dir]",
		Short: "Export .env file contents as JSON",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			var directory string
			if len(arguments) == 1 {
				directory = arguments[0]
			}
			return runF(&Options{
				Context:          command.Context(),
				Directory:        directory,
				Environment:      environment,
				Merge:            merge,
				Output:           output,
				WorkingDirectory: factory.WorkingDirectory,
				Terminal:         factory.Terminal,
				Reader:           osExportEnvReader{},
				Writer:           osExportEnvWriter{},
			})
		},
	}
	envCommand.Flags().StringVarP(&environment, "env", "e", "", "Environment name, skip interactive selection (e.g. local, prod)")
	envCommand.Flags().BoolVar(&merge, "merge", false, "Merge .env with the selected environment file")
	envCommand.Flags().StringVarP(&output, "out", "o", "", "Write output to file instead of stdout")
	return envCommand
}
