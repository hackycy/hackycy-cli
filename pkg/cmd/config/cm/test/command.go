package test

import (
	"context"
	"errors"
	"net/http"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type StoreProvider func() (TestProfileResolver, error)

type Options struct {
	Context  context.Context
	Profile  string
	Store    StoreProvider
	HTTP     *http.Client
	Terminal *terminalexperience.Runtime
}

func NewCmdTest(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runTest
	}
	return &cobra.Command{Use: "test [profile]", Short: "Test a commit message provider profile", Args: cobra.MaximumNArgs(1), RunE: func(command *cobra.Command, arguments []string) error {
		if factory == nil || factory.ConfigStore == nil || factory.HTTPClient == nil || factory.Terminal == nil {
			return errors.New("config cm test Factory is incomplete")
		}
		profile := ""
		if len(arguments) == 1 {
			profile = arguments[0]
		}
		return runF(&Options{Context: command.Context(), Profile: profile, Store: func() (TestProfileResolver, error) {
			store, err := factory.ConfigStore()
			if err != nil {
				return nil, err
			}
			return store, nil
		}, HTTP: factory.HTTPClient, Terminal: factory.Terminal})
	}}
}

var _ TestProfileResolver = (*appconfig.Store)(nil)
