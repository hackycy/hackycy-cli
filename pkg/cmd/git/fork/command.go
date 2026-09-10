package fork

import (
	"context"
	"errors"
	"net/http"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// ConfigStore resolves Git Fork's decrypted provider credentials at execution time.
type ConfigStore func() (ConfigReader, error)

// Options contains the parsed Git Fork request and its leaf-owned adapters.
type Options struct {
	Context          context.Context
	Repository       string
	Destination      string
	Config           ConfigStore
	WorkingDirectory func() (string, error)
	HTTP             *http.Client
	Terminal         *terminal.Runtime
	Git              *gitprocess.Runner
}

// NewCmdFork creates the git fork leaf with an optional test runner.
func NewCmdFork(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runFork
	}
	return &cobra.Command{
		Use:   "fork <repo> [dest]",
		Short: "Download a repo without git history (supports GitHub/GitLab, public/private)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(command *cobra.Command, arguments []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.WorkingDirectory == nil || factory.HTTPClient == nil || factory.Terminal == nil || factory.GitRunner == nil {
				return errors.New("git fork Factory is incomplete")
			}
			options := &Options{
				Context:          command.Context(),
				Repository:       arguments[0],
				WorkingDirectory: factory.WorkingDirectory,
				HTTP:             factory.HTTPClient,
				Terminal:         factory.Terminal,
				Git:              factory.GitRunner(),
				Config: func() (ConfigReader, error) {
					store, err := factory.ConfigStore()
					if err != nil {
						return nil, err
					}
					return store, nil
				},
			}
			if len(arguments) == 2 {
				options.Destination = arguments[1]
			}
			return runF(options)
		},
	}
}
