package server

import (
	"context"
	"errors"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options contains the parsed Tunnel server request and leaf-owned logger.
type Options struct {
	Context context.Context
	Config  ServerConfig
	Logger  logging.Logger
}

// NewCmdServer creates the Tunnel server leaf with an optional test runner.
func NewCmdServer(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runServer
	}
	address := ""
	controlPort := ""
	frpPort := ""
	httpPort := ""
	portRange := ""
	advertiseFRPAddr := ""
	dataDirectory := ""
	sessionIdleDays := ""
	command := &cobra.Command{
		Use:   "server",
		Short: "Run the Tunnel Control Plane and supervised frps process",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.Logging == nil || factory.EnvironmentLookup == nil {
				return errors.New("tunnel server Factory is incomplete")
			}
			logger := factory.Logging.Logger("tunnel.server")
			config, err := ResolveServerConfig(ServerOptionInput{
				Address:          commandOption(command, "address", address),
				ControlPort:      commandOption(command, "control-port", controlPort),
				FRPPort:          commandOption(command, "frp-port", frpPort),
				HTTPPort:         commandOption(command, "http-port", httpPort),
				PortRange:        commandOption(command, "port-range", portRange),
				AdvertiseFRPAddr: commandOption(command, "advertise-frp-addr", advertiseFRPAddr),
				DataDir:          commandOption(command, "data-dir", dataDirectory),
				SessionIdleDays:  commandOption(command, "session-idle-days", sessionIdleDays),
			}, factory.EnvironmentLookup)
			if err != nil {
				logger.Error("Could not resolve tunnel server configuration", map[string]any{"error": err.Error()})
				return err
			}
			return runF(&Options{Context: command.Context(), Config: config, Logger: logger})
		},
	}
	command.Flags().StringVar(&address, "address", address, "Address for all tunnel server listeners")
	command.Flags().StringVar(&controlPort, "control-port", controlPort, "Tunnel Control Plane port")
	command.Flags().StringVar(&frpPort, "frp-port", frpPort, "FRP bind port")
	command.Flags().StringVar(&httpPort, "http-port", httpPort, "FRP HTTP vhost port")
	command.Flags().StringVar(&portRange, "port-range", portRange, "Server Port Pool")
	command.Flags().StringVar(&advertiseFRPAddr, "advertise-frp-addr", advertiseFRPAddr, "FRP endpoint advertised to trusted clients")
	command.Flags().StringVar(&dataDirectory, "data-dir", dataDirectory, "Tunnel server state directory")
	command.Flags().StringVar(&sessionIdleDays, "session-idle-days", sessionIdleDays, "Session idle lifetime in days")
	return command
}

func commandOption(command *cobra.Command, name, value string) *string {
	if !command.Flags().Changed(name) {
		return nil
	}
	return &value
}

func runServer(options *Options) error {
	if options == nil {
		return errors.New("Tunnel server options are required")
	}
	return RunServer(options.Context, options.Config, ServerRunOptions{Logger: options.Logger})
}
