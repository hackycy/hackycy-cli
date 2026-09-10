package list

import (
	"context"
	"errors"
	"fmt"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// StoreProvider resolves the appconfig boundary only when the command runs.
// Keeping it as a function preserves the Factory's lazy configuration contract.
type StoreProvider func() (Reader, error)

// Options contains the parsed list request and leaf-owned capabilities.
type Options struct {
	Context  context.Context
	Store    StoreProvider
	Terminal *terminal.Runtime
}

// NewCmdList creates the config fork list command with an optional test runner.
func NewCmdList(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runList
	}
	command := &cobra.Command{
		Use:   "list",
		Short: "List configured provider instances",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
				return errors.New("config fork list Factory is incomplete")
			}
			return runF(&Options{
				Context: command.Context(),
				Store: func() (Reader, error) {
					store, err := factory.ConfigStore()
					if err != nil {
						return nil, err
					}
					return store, nil
				},
				Terminal: factory.Terminal,
			})
		},
	}
	return command
}

func runList(options *Options) error {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return errors.New("config fork list options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, forkListConsoleDescriptor())
	if err != nil {
		return err
	}
	defer run.Close()

	caps := options.Terminal.Capabilities()
	var updates chan terminal.OperationPhase
	var trackDone chan error
	if caps.Interaction == terminal.RichInteractive {
		updates = make(chan terminal.OperationPhase, 4)
		trackDone = make(chan error, 1)
		go func() {
			trackDone <- run.Track(terminal.TrackedOperation{
				ID:    "config-fork-list",
				Label: "Fork provider instances",
				Phases: []terminal.PhaseDefinition{{
					ID:   forkListPhaseID,
					Name: forkListPhaseName,
				}},
				Updates: updates,
			})
		}()
		updates <- terminal.OperationPhase{ID: forkListPhaseID, State: terminal.PhaseActive, Detail: "Loading fork provider instances"}
	} else if caps.Interaction == terminal.PlainInteractive {
		if err := run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{
			Role: terminal.VisualRoleActive,
			Text: "Loading fork provider instances...",
		}}}); err != nil {
			return finishForkList(run, terminal.Failed, nil, err)
		}
	}
	result, workErr := func() (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		store, err := options.Store()
		if err != nil {
			return Result{}, err
		}
		module, err := New(Dependencies{Reader: store})
		if err != nil {
			return Result{}, err
		}
		return module.Run(ctx, Input{})
	}()

	terminalState := terminal.PhaseCompleted
	detail := fmt.Sprintf("Loaded %d fork provider instances", len(result.Instances))
	if workErr != nil {
		terminalState = terminal.PhaseFailed
		detail = "Unable to load fork provider instances"
		if errors.Is(workErr, context.Canceled) || errors.Is(workErr, context.DeadlineExceeded) {
			terminalState = terminal.PhaseCancelled
			detail = "Cancelled while loading fork provider instances"
		}
	} else if err := ctx.Err(); err != nil {
		workErr = err
		terminalState = terminal.PhaseCancelled
		detail = "Cancelled while loading fork provider instances"
	}
	var trackErr error
	if caps.Interaction == terminal.RichInteractive {
		updates <- terminal.OperationPhase{ID: forkListPhaseID, State: terminalState, Detail: detail}
		close(updates)
		trackErr = <-trackDone
	}
	if err := errors.Join(workErr, trackErr); err != nil {
		outcome := terminal.Failed
		if terminalState == terminal.PhaseCancelled {
			outcome = terminal.Cancelled
		}
		return finishForkList(run, outcome, nil, err)
	}

	document := terminalForkListDocument(result)
	if caps.Interaction == terminal.RichInteractive && caps.Stdout.Terminal {
		document = terminalForkListRichDocument(result)
	}
	return run.Finish(terminalForkListFinishRequest(terminal.Succeeded, &result), &document)
}

const (
	forkListPhaseID   = "load-fork-provider-instances"
	forkListPhaseName = "Load fork provider instances"
)

func forkListConsoleDescriptor() terminal.ConsoleDescriptor {
	return terminal.ConsoleDescriptor{
		Command: "YCY / config fork list",
		Target:  "provider inventory",
		Status:  "READY",
		Metadata: []terminal.ConsoleMetadata{{
			Label: "scope",
			Value: "git fork configuration",
		}},
	}
}

func finishForkList(run terminal.ExperienceRun, outcome terminal.FinishOutcome, document *terminal.PresentationDocument, workErr error) error {
	return errors.Join(workErr, run.Finish(terminalForkListFinishRequest(outcome, nil), document))
}

func terminalForkListFinishRequest(outcome terminal.FinishOutcome, result *Result) terminal.FinishRequest {
	request := terminal.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminal.Succeeded:
		if result != nil {
			request.Summary = terminalForkListFinishSummaryDocument(*result)
		}
	case terminal.Cancelled:
		request.Summary = terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{
			Role: terminal.VisualRoleWarning,
			Text: "Cancelled while loading fork provider instances",
		}}}
	case terminal.Failed:
		request.Summary = terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{
			Role: terminal.VisualRoleError,
			Text: "Unable to load fork provider instances",
		}}}
	}
	return request
}

// Ensure the Factory's concrete Store remains the intended implementation of
// the narrow list Reader boundary.
var _ Reader = (*appconfig.Store)(nil)
