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

type StoreProvider func() (Reader, error)

type Options struct {
	Context  context.Context
	Store    StoreProvider
	Terminal *terminal.Runtime
}

func NewCmdList(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runList
	}
	return &cobra.Command{
		Use:   "list",
		Short: "List configured CM profiles",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
				return errors.New("config cm list Factory is incomplete")
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
}

func runList(options *Options) error {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return errors.New("config cm list options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, cmListConsoleDescriptor())
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
				ID:    "config-cm-list",
				Label: "Commit message profiles",
				Phases: []terminal.PhaseDefinition{{
					ID:   cmListPhaseID,
					Name: cmListPhaseName,
				}},
				Updates: updates,
			})
		}()
		updates <- terminal.OperationPhase{ID: cmListPhaseID, State: terminal.PhaseActive, Detail: "Loading CM profiles"}
	} else if caps.Interaction == terminal.PlainInteractive {
		if err := run.Notice(terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{
			Role: terminal.VisualRoleActive,
			Text: "Loading CM profiles...",
		}}}); err != nil {
			return finishCMList(run, terminal.Failed, nil, err)
		}
	}
	result, workErr := func() (Result, error) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		reader, err := options.Store()
		if err != nil {
			return Result{}, err
		}
		module, err := New(Dependencies{Reader: reader})
		if err != nil {
			return Result{}, err
		}
		return module.Run(ctx, Input{})
	}()

	terminalState := terminal.PhaseCompleted
	detail := fmt.Sprintf("Loaded %d CM profiles", len(result.Profiles))
	if len(result.Profiles) == 1 {
		detail = "Loaded 1 CM profile"
	}
	if workErr != nil {
		terminalState = terminal.PhaseFailed
		detail = "Unable to load CM profiles"
		if errors.Is(workErr, context.Canceled) || errors.Is(workErr, context.DeadlineExceeded) {
			terminalState = terminal.PhaseCancelled
			detail = "Cancelled while loading CM profiles"
		}
	} else if err := ctx.Err(); err != nil {
		workErr = err
		terminalState = terminal.PhaseCancelled
		detail = "Cancelled while loading CM profiles"
	}
	var trackErr error
	if caps.Interaction == terminal.RichInteractive {
		updates <- terminal.OperationPhase{ID: cmListPhaseID, State: terminalState, Detail: detail}
		close(updates)
		trackErr = <-trackDone
	}
	if err := errors.Join(workErr, trackErr); err != nil {
		outcome := terminal.Failed
		if terminalState == terminal.PhaseCancelled {
			outcome = terminal.Cancelled
		}
		return finishCMList(run, outcome, nil, err)
	}
	document := terminalCMListDocument(result)
	if caps.Interaction == terminal.RichInteractive && caps.Stdout.Terminal {
		document = terminalCMListRichDocument(result)
	}
	return run.Finish(terminalCMListFinishRequest(terminal.Succeeded, &result), &document)
}

const (
	cmListPhaseID   = "load-cm-profiles"
	cmListPhaseName = "Load CM profiles"
)

func cmListConsoleDescriptor() terminal.ConsoleDescriptor {
	return terminal.ConsoleDescriptor{
		Command: "YCY / config cm list",
		Target:  "profile inventory",
		Status:  "READY",
		Metadata: []terminal.ConsoleMetadata{{
			Label: "scope",
			Value: "commit message configuration",
		}},
	}
}

func finishCMList(run terminal.ExperienceRun, outcome terminal.FinishOutcome, document *terminal.PresentationDocument, workErr error) error {
	return errors.Join(workErr, run.Finish(terminalCMListFinishRequest(outcome, nil), document))
}

func terminalCMListFinishRequest(outcome terminal.FinishOutcome, result *Result) terminal.FinishRequest {
	request := terminal.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminal.Succeeded:
		if result != nil {
			request.Summary = terminalCMListFinishSummaryDocument(*result)
		}
	case terminal.Cancelled:
		request.Summary = terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{
			Role: terminal.VisualRoleWarning,
			Text: "Cancelled while loading CM profiles",
		}}}
	case terminal.Failed:
		request.Summary = terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{
			Role: terminal.VisualRoleError,
			Text: "Unable to load CM profiles",
		}}}
	}
	return request
}

var _ Reader = (*appconfig.Store)(nil)
