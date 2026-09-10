package upgrade

import (
	"context"
	"errors"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/updater"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options contains the parsed Upgrade request and leaf-owned presentation.
type Options struct {
	Context        context.Context
	Terminal       *terminal.Runtime
	CurrentVersion string
	run            func(context.Context, updater.UpgradeOptions) (updater.UpgradeResult, error)
}

// NewCmdUpgrade creates the Upgrade leaf with an optional test runner.
func NewCmdUpgrade(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runUpgrade
	}
	return &cobra.Command{
		Use:          "upgrade",
		Short:        "Update ycy to the latest release",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.Terminal == nil || factory.Version == "" {
				return errors.New("upgrade Factory is incomplete")
			}
			return runF(&Options{
				Context:        command.Context(),
				Terminal:       factory.Terminal,
				CurrentVersion: factory.Version,
			})
		},
	}
}

func runUpgrade(options *Options) error {
	if options == nil || options.Terminal == nil {
		return errors.New("upgrade options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	run, err := options.Terminal.OpenConsole(ctx, terminalUpgradeConsoleDescriptor(options.CurrentVersion))
	if err != nil {
		return err
	}
	defer run.Close()
	if options.Terminal.Capabilities().Interaction == terminal.RichInteractive {
		if err := run.Notice(terminalUpgradeIntroDocument()); err != nil {
			return errors.Join(err, run.Finish(terminalUpgradeFinishRequest(terminal.Failed, "Unable to start upgrade.", terminal.VisualRoleError), nil))
		}
	}
	sink := newUpgradePhaseSink(run, options.Terminal.Capabilities(), cancel)
	runner := options.run
	if runner == nil {
		runner = updater.RunUpgrade
	}
	result, resultErr := runner(ctx, updater.UpgradeOptions{
		Resolver: updater.ReleaseResolverOptions{CurrentVersion: options.CurrentVersion},
		Observer: sink.observer(),
	})
	resultErr = errors.Join(resultErr, sink.close(), sink.err())
	return finishUpgradeRun(run, options.Terminal.DiagnosticWriter(), sink.previousDocument(), result, resultErr)
}

func terminalUpgradeConsoleDescriptor(currentVersion string) terminal.ConsoleDescriptor {
	current := terminalUpgradeSafeDetail(currentVersion)
	if current == "" {
		current = "current release"
	}
	return terminal.ConsoleDescriptor{
		Command: "YCY / upgrade",
		Target:  "release update",
		Status:  "READY",
		Metadata: []terminal.ConsoleMetadata{
			{Label: "scope", Value: "detached updater"},
			{Label: "current", Value: current},
		},
	}
}
