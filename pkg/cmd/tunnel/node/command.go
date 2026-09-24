package node

import (
	"context"
	"errors"
	"io"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type Options struct {
	Context context.Context
	Config  Config
	Logger  logging.Logger
	Output  io.Writer
}

// NewCmdNode registers the single foreground Node operation.
func NewCmdNode(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runNode
	}
	var bindAddress, port, dataDir string
	command := &cobra.Command{
		Use:   "node",
		Short: "Run a remote Tunnel Node management listener",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.Logging == nil || factory.EnvironmentLookup == nil {
				return errors.New("tunnel node Factory is incomplete")
			}
			config, err := ResolveConfig(Input{
				ManagementBindAddress: flagValue(command, "management-bind-address", bindAddress),
				ManagementPort:        flagValue(command, "management-port", port),
				DataDir:               flagValue(command, "data-dir", dataDir),
			}, factory.EnvironmentLookup)
			if err != nil {
				return err
			}
			return runF(&Options{Context: command.Context(), Config: config, Logger: factory.Logging.Logger("tunnel.node"), Output: factory.IOStreams.Out})
		},
	}
	command.Flags().StringVar(&bindAddress, "management-bind-address", "", "Management HTTP bind IP")
	command.Flags().StringVar(&port, "management-port", "", "Management HTTP port")
	command.Flags().StringVar(&dataDir, "data-dir", "", "Private Node state directory")
	return command
}

func flagValue(command *cobra.Command, name, value string) *string {
	if !command.Flags().Changed(name) {
		return nil
	}
	return &value
}

func runNode(options *Options) error {
	if options == nil {
		return errors.New("Tunnel Node options are required")
	}
	return Run(options.Context, options.Config, options.Output)
}
