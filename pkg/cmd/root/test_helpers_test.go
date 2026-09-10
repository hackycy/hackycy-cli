package root

import (
	"io"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

// BuildInfo keeps package-local tests focused on their existing command facts.
type BuildInfo struct {
	Version string
}

type testDependencies struct {
	Out               io.Writer
	Err               io.Writer
	Environment       func(string) string
	EnvironmentLookup func(string) (string, bool)
	Logging           *logging.Runtime
	Capabilities      terminal.Capabilities
}

func newTestApp(build BuildInfo, dependencies testDependencies) (*App, error) {
	factory := commandfactory.New(commandfactory.Options{
		Version: build.Version,
		IOStreams: cmdutil.IOStreams{
			Out:    dependencies.Out,
			ErrOut: dependencies.Err,
		},
		Environment:       dependencies.Environment,
		EnvironmentLookup: dependencies.EnvironmentLookup,
		Capabilities:      dependencies.Capabilities,
	})
	if dependencies.Logging != nil {
		factory.Logging = dependencies.Logging
	}
	return New(factory)
}
