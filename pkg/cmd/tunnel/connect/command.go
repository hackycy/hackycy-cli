package connect

import (
	"errors"

	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// NewCmdConnect creates the Tunnel connect leaf with an optional test runner.
func NewCmdConnect(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runConnect
	}
	server := ""
	token := ""
	command := &cobra.Command{
		Use:   "connect",
		Short: "Connect a native trusted client to a Tunnel Control Plane",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.EnvironmentLookup == nil || factory.Terminal == nil || factory.Logging == nil {
				return errors.New("tunnel connect Factory is incomplete")
			}
			return runF(&Options{
				Context: command.Context(),
				Input: ClientOptionInput{
					Server: connectCommandOption(command, "server", server),
					Token:  connectCommandOption(command, "token", token),
				},
				ConfigStore: func() (ConnectionStore, error) {
					store, err := factory.ConfigStore()
					if err != nil {
						return nil, err
					}
					return store, nil
				},
				Environment: ClientEnvironment(factory.EnvironmentLookup),
				Terminal:    factory.Terminal,
				Logger:      factory.Logging.Logger("tunnel.client"),
				YCYVersion:  factory.Version,
				Now:         factory.Now,
			})
		},
	}
	command.Flags().StringVar(&server, "server", server, "Tunnel Control Plane origin")
	command.Flags().StringVar(&token, "token", token, "Client Token")
	return command
}

func connectCommandOption(command *cobra.Command, name, value string) *string {
	if !command.Flags().Changed(name) {
		return nil
	}
	return &value
}
