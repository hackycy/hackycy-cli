package root

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/logging"
	configcommand "github.com/hackycy/hackycy-cli/pkg/cmd/config"
	diffcommand "github.com/hackycy/hackycy-cli/pkg/cmd/diff"
	exportcommand "github.com/hackycy/hackycy-cli/pkg/cmd/export"
	fscommand "github.com/hackycy/hackycy-cli/pkg/cmd/fs"
	gitcommand "github.com/hackycy/hackycy-cli/pkg/cmd/git"
	rmcommand "github.com/hackycy/hackycy-cli/pkg/cmd/rm"
	runcommand "github.com/hackycy/hackycy-cli/pkg/cmd/run"
	tunnelcommand "github.com/hackycy/hackycy-cli/pkg/cmd/tunnel"
	upgradecommand "github.com/hackycy/hackycy-cli/pkg/cmd/upgrade"
	zipcommand "github.com/hackycy/hackycy-cli/pkg/cmd/zip"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Outcome leaves process exit ownership with cmd/ycy.
type Outcome struct {
	Code int
	Err  error
}

// App owns Cobra, global options, diagnostics, and fresh root-tree construction.
type App struct {
	factory *cmdutil.Factory
}

// New creates the lifted root with the bounded Factory.
func New(factory *cmdutil.Factory) (*App, error) {
	if factory == nil {
		return nil, errors.New("command Factory is required")
	}
	if strings.TrimSpace(factory.Version) == "" {
		return nil, errors.New("CLI build version is required")
	}
	if factory.IOStreams.Out == nil || factory.Terminal == nil || factory.Logging == nil || factory.Environment == nil || factory.EnvironmentLookup == nil {
		return nil, errors.New("command Factory is incomplete")
	}
	return &App{factory: factory}, nil
}

func (app *App) output() io.Writer {
	return app.factory.IOStreams.Out
}

func (app *App) diagnostics() io.Writer {
	return app.factory.Terminal.DiagnosticWriter()
}

// Execute builds a fresh Cobra tree for every invocation and never exits the process itself.
func (app *App) Execute(context context.Context, arguments []string) Outcome {
	arguments = normalizeDiagnosticAliases(arguments)
	arguments = gitcommand.NormalizeArguments(arguments)
	controls := collectDiagnosticControls(arguments)
	var presentationErr error
	root := app.rootCommandWithPresentationError(controls, func(err error) {
		if err != nil && presentationErr == nil {
			presentationErr = err
		}
	})
	return app.execute(func() error {
		root.SetArgs(arguments)
		err := root.ExecuteContext(context)
		if presentationErr != nil {
			return presentationErr
		}
		return normalizeCobraError(root, arguments, err)
	})
}

func (app *App) execute(invoke func() error) (outcome Outcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("%v", recovered)
			app.reportRuntimeError(err)
			outcome = Outcome{Code: 1, Err: err}
		}
	}()

	if err := invoke(); err != nil {
		var exitOutcome exitCodedError
		if errors.As(err, &exitOutcome) {
			return Outcome{Code: exitOutcome.ExitCode()}
		}
		if errors.Is(err, errHelpRequested) {
			return Outcome{Code: 1, Err: err}
		}
		if !alreadyReported(err) {
			app.reportError(err)
		}
		return Outcome{Code: 1, Err: err}
	}
	return Outcome{}
}

type alreadyReportedError interface {
	AlreadyReported() bool
}

func alreadyReported(err error) bool {
	var reported alreadyReportedError
	return errors.As(err, &reported) && reported.AlreadyReported()
}

type exitCodedError interface {
	error
	ExitCode() int
}

const (
	rootDiagnosticLimit    = 1024
	rootDiagnosticOmission = " [truncated]"
)

func (app *App) rootCommand() *cobra.Command {
	return app.rootCommandWithPresentationError(diagnosticControls{}, nil)
}

func (app *App) rootCommandWithDiagnosticControls(controls diagnosticControls) *cobra.Command {
	return app.rootCommandWithPresentationError(controls, nil)
}

func (app *App) rootCommandWithPresentationError(controls diagnosticControls, capturePresentationError func(error)) *cobra.Command {
	if capturePresentationError == nil {
		capturePresentationError = func(error) {}
	}
	var logLevel string
	var logFormat string
	var verbose bool
	var quiet bool
	var showVersion bool
	configureDiagnostics := func() error {
		return app.configureDiagnosticLogging(controls)
	}
	discovery := newTerminalDiscoveryAdapter(app.factory.Terminal)
	presentDiscovery := func(command *cobra.Command) error {
		return discovery.PresentDiscovery(command.Context(), newDiscoveryDocument(command))
	}
	root := &cobra.Command{
		Use:           "ycy",
		Short:         "Ycy command line interface",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if showVersion {
				_, _ = fmt.Fprintln(app.output(), app.factory.Version)
				return nil
			}
			if err := presentDiscovery(command); err != nil {
				return err
			}
			return errHelpRequested
		},
	}
	root.SetOut(app.output())
	root.SetErr(app.diagnostics())
	root.PersistentPreRunE = func(command *cobra.Command, _ []string) error {
		switch command.CommandPath() {
		case "ycy diff", "ycy fs", "ycy rm", "ycy export env", "ycy run", "ycy zip", "ycy tunnel server", "ycy tunnel connect",
			"ycy upgrade",
			"ycy git heat", "ycy git pulse", "ycy git fork", "ycy git cm",
			"ycy config fork list", "ycy config fork add", "ycy config fork remove",
			"ycy config cm list", "ycy config cm add", "ycy config cm use",
			"ycy config cm set", "ycy config cm remove", "ycy config cm test":
			return configureDiagnostics()
		}
		return nil
	}
	root.PersistentFlags().StringVar(&logLevel, "log-level", "", "Log level: debug, info, warn, or error")
	root.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable debug diagnostics")
	root.PersistentFlags().BoolVar(&quiet, "quiet", false, "Only emit error diagnostics (short form: -q)")
	root.PersistentFlags().StringVar(&logFormat, "log-format", "", "Diagnostic format: text or json")
	root.Flags().BoolVarP(&showVersion, "version", "V", false, "Print version")
	root.AddCommand(exportcommand.NewCmdExport(app.factory))
	configCommand := configcommand.NewCmdConfig(app.factory)
	root.AddCommand(configCommand)
	root.AddCommand(rmcommand.NewCmdRM(app.factory, nil))
	root.AddCommand(runcommand.NewCmdRun(app.factory, nil))
	root.AddCommand(zipcommand.NewCmdZIP(app.factory, nil))
	root.AddCommand(gitcommand.NewCmdGit(app.factory))
	root.AddCommand(diffcommand.NewCmdDiff(app.factory, nil))
	root.AddCommand(fscommand.NewCmdFS(app.factory, nil))
	root.AddCommand(tunnelcommand.NewCmdTunnel(app.factory))
	root.AddCommand(upgradecommand.NewCmdUpgrade(app.factory, nil))
	root.SetHelpFunc(func(command *cobra.Command, _ []string) {
		capturePresentationError(presentDiscovery(command))
	})
	return root
}

func (app *App) configureDiagnosticLogging(controls diagnosticControls) error {
	configuration, err := logging.ParseConfiguration(logging.ConfigurationInput{
		LogLevels:    controls.logLevels,
		LogFormats:   controls.logFormats,
		VerboseCount: controls.verboseCount,
		QuietCount:   controls.quietCount,
		LookupEnv:    app.factory.EnvironmentLookup,
	})
	if err != nil {
		return err
	}
	app.factory.Logging.SetLevel(configuration.Level)
	app.factory.Logging.SetFormat(configuration.Format)
	return nil
}

func (app *App) reportError(err error) {
	_, _ = fmt.Fprintf(app.diagnostics(), "error: %s\n", rootDiagnostic(err))
}

func (app *App) reportRuntimeError(err error) {
	_, _ = fmt.Fprintln(app.output())
	_, _ = fmt.Fprintf(app.diagnostics(), "error: %s\n", rootDiagnostic(err))
	if app.debugEnabled() {
		_, _ = app.diagnostics().Write(debug.Stack())
	}
}

func rootDiagnostic(err error) string {
	value := logging.RedactDiagnostic(err.Error())
	runes := []rune(value)
	if len(runes) <= rootDiagnosticLimit {
		return value
	}
	return string(runes[:rootDiagnosticLimit-len(rootDiagnosticOmission)]) + rootDiagnosticOmission
}

func (app *App) debugEnabled() bool {
	return app.factory.Environment("DEBUG") != "" || strings.EqualFold(app.factory.Environment("NODE_ENV"), "development")
}

var errHelpRequested = errors.New("help requested")
