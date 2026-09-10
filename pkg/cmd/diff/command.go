package diff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options contains the parsed Diff request and leaf-owned adapters.
type Options struct {
	Context context.Context
	Input   Input

	Terminal          *terminal.Runtime
	NetworkInterfaces func() ([]NetworkInterface, error)
	Logger            logging.Logger
	Now               func() time.Time
}

// NewCmdDiff creates the Diff leaf with an optional test runner.
func NewCmdDiff(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runDiff
	}
	port := "1205"
	exclusions := []string{}
	var public bool
	var noGitIgnore bool
	command := &cobra.Command{
		Use:   "diff <baseline-directory> <target-directory>",
		Short: "Compare two directories in a browser",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if factory == nil || factory.Terminal == nil || factory.Logging == nil {
				return errors.New("diff Factory is incomplete")
			}
			parsedPort, err := parseDiffPort(port)
			if err != nil {
				return err
			}
			return runF(&Options{
				Context: command.Context(),
				Input: Input{
					BaselineDirectory: arguments[0],
					TargetDirectory:   arguments[1],
					Port:              parsedPort,
					Public:            public,
					Exclusions:        append([]string{}, exclusions...),
					NoGitIgnore:       noGitIgnore,
				},
				Terminal:          factory.Terminal,
				NetworkInterfaces: osDiffNetworkInterfaces,
				Logger:            factory.Logging.Logger("diff"),
				Now:               factory.Now,
			})
		},
	}
	command.Flags().StringVarP(&port, "port", "p", port, "Port to serve on")
	command.Flags().BoolVar(&public, "public", false, "Make the diff available on the local network")
	command.Flags().StringArrayVarP(&exclusions, "exclude", "x", exclusions, "Add an exclusion")
	command.Flags().BoolVar(&noGitIgnore, "no-gitignore", false, "Do not apply Target Directory .gitignore files")
	return command
}

func parseDiffPort(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("'%s' is not a valid port", value)
	}
	port := 0
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("'%s' is not a valid port", value)
		}
		port = port*10 + int(character-'0')
		if port > 65535 {
			return 0, errors.New("Port must be between 0 and 65535")
		}
	}
	return port, nil
}
