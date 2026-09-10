package factory

import (
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

// Options supplies explicit process facts for Factory construction.
type Options struct {
	Version           string
	IOStreams         cmdutil.IOStreams
	Capabilities      terminal.Capabilities
	Environment       func(string) string
	EnvironmentLookup func(string) (string, bool)
	WorkingDirectory  func() (string, error)
	HTTPClient        *http.Client
	Now               func() time.Time

	newConfigStore func() (*appconfig.Store, error)
	newGitRunner   func() *gitprocess.Runner
}

// New constructs process-level command capabilities without touching config,
// Git, or the network. ConfigStore and GitRunner remain lazy and memoized.
func New(options Options) *cmdutil.Factory {
	options = normalizeOptions(options)
	terminalRuntime := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: options.Capabilities,
		Input:        options.IOStreams.In,
		Output:       options.IOStreams.Out,
		Diagnostics:  options.IOStreams.ErrOut,
	})
	loggingRuntime := logging.NewRuntime(logging.Options{
		Writer: terminalRuntime.DiagnosticWriter(),
		Now:    options.Now,
		Color:  options.Capabilities.Stderr.Color,
	})

	return &cmdutil.Factory{
		Version:           options.Version,
		IOStreams:         options.IOStreams,
		Terminal:          terminalRuntime,
		Logging:           loggingRuntime,
		Environment:       options.Environment,
		EnvironmentLookup: options.EnvironmentLookup,
		WorkingDirectory:  options.WorkingDirectory,
		HTTPClient:        options.HTTPClient,
		Now:               options.Now,
		ConfigStore:       memoizedConfigStore(options.newConfigStore),
		GitRunner:         memoizedGitRunner(options.newGitRunner),
	}
}

func normalizeOptions(options Options) Options {
	if options.IOStreams.In == nil {
		options.IOStreams.In = os.Stdin
	}
	if options.IOStreams.Out == nil {
		options.IOStreams.Out = os.Stdout
	}
	if options.IOStreams.ErrOut == nil {
		options.IOStreams.ErrOut = os.Stderr
	}
	providedEnvironment := options.Environment != nil
	if !providedEnvironment {
		options.Environment = os.Getenv
	}
	if options.EnvironmentLookup == nil {
		if !providedEnvironment {
			options.EnvironmentLookup = os.LookupEnv
		} else {
			options.EnvironmentLookup = func(key string) (string, bool) {
				value := options.Environment(key)
				return value, value != ""
			}
		}
	}
	if options.WorkingDirectory == nil {
		options.WorkingDirectory = os.Getwd
	}
	if options.HTTPClient == nil {
		options.HTTPClient = http.DefaultClient
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.newConfigStore == nil {
		options.newConfigStore = func() (*appconfig.Store, error) {
			return appconfig.New(appconfig.Dependencies{Environment: options.Environment})
		}
	}
	if options.newGitRunner == nil {
		options.newGitRunner = func() *gitprocess.Runner {
			return &gitprocess.Runner{}
		}
	}
	return options
}

func memoizedConfigStore(create func() (*appconfig.Store, error)) func() (*appconfig.Store, error) {
	var once sync.Once
	var store *appconfig.Store
	var err error
	return func() (*appconfig.Store, error) {
		once.Do(func() {
			store, err = create()
		})
		return store, err
	}
}

func memoizedGitRunner(create func() *gitprocess.Runner) func() *gitprocess.Runner {
	var once sync.Once
	var runner *gitprocess.Runner
	return func() *gitprocess.Runner {
		once.Do(func() {
			runner = create()
		})
		return runner
	}
}
