package use

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type StoreProvider func() (UseWriter, error)

type Options struct {
	Context  context.Context
	Profile  string
	Store    StoreProvider
	Terminal *terminalexperience.Runtime
}

func NewCmdUse(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runUse
	}
	return &cobra.Command{Use: "use <profile>", Short: "Set the default commit message provider profile", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, arguments []string) error {
		if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
			return errors.New("config cm use Factory is incomplete")
		}
		return runF(&Options{Context: command.Context(), Profile: arguments[0], Store: func() (UseWriter, error) {
			store, err := factory.ConfigStore()
			if err != nil {
				return nil, err
			}
			return store, nil
		}, Terminal: factory.Terminal})
	}}
}

// profile is held only by the constructor closure; Options remains the leaf's semantic boundary.
// The command runner receives it through the private field below.
func runUse(options *Options) error { return executeUse(options) }

func executeUse(options *Options) error {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return errors.New("config cm use options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, cmUseConsoleDescriptor())
	if err != nil {
		return err
	}
	defer run.Close()

	caps := options.Terminal.Capabilities()
	if caps.Interaction == terminalexperience.PlainInteractive {
		if err := run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleActive,
			Text: "Setting default CM profile...",
		}}}); err != nil {
			return finishCMUse(run, terminalexperience.Failed, nil, err)
		}
	}

	var updates chan terminalexperience.OperationPhase
	var trackDone chan error
	if caps.Interaction == terminalexperience.RichInteractive {
		updates = make(chan terminalexperience.OperationPhase, 4)
		trackDone = make(chan error, 1)
		go func() {
			trackDone <- run.Track(terminalexperience.TrackedOperation{
				ID:    "config-cm-use",
				Label: "Set default CM profile",
				Phases: []terminalexperience.PhaseDefinition{{
					ID:   cmUsePhaseID,
					Name: cmUsePhaseName,
				}},
				Updates: updates,
			})
		}()
		updates <- terminalexperience.OperationPhase{ID: cmUsePhaseID, State: terminalexperience.PhaseActive, Detail: "Checking profile and saving selection · Profile: " + safeCMUseProfile(options.Profile)}
	}

	result, workErr := func() (UseResult, error) {
		writer, err := options.Store()
		if err != nil {
			return UseResult{}, err
		}
		module, err := NewUse(UseDependencies{Writer: writer})
		if err != nil {
			return UseResult{}, err
		}
		return module.Run(ctx, UseRequest{Profile: options.Profile})
	}()

	if caps.Interaction == terminalexperience.RichInteractive {
		state := terminalexperience.PhaseCompleted
		detail := "Profile: " + safeCMUseProfile(options.Profile)
		if workErr != nil {
			state = terminalexperience.PhaseFailed
			detail = "Unable to set default CM profile"
		}
		updates <- terminalexperience.OperationPhase{ID: cmUsePhaseID, State: state, Detail: detail}
		close(updates)
		if trackErr := <-trackDone; trackErr != nil {
			workErr = errors.Join(workErr, trackErr)
		}
	}
	if workErr != nil {
		return finishCMUse(run, terminalexperience.Failed, nil, workErr)
	}

	document := terminalCMUseDocument(result)
	if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
		document = terminalCMUseRichDocument(result)
	}
	return run.Finish(terminalCMUseFinishRequest(terminalexperience.Succeeded), &document)
}

const (
	cmUsePhaseID   = "set-default-cm-profile"
	cmUsePhaseName = "Set default CM profile"
)

func cmUseConsoleDescriptor() terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / config cm use",
		Target:  "profile selection",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{{
			Label: "scope",
			Value: "commit message configuration",
		}},
	}
}

func finishCMUse(run terminalexperience.ExperienceRun, outcome terminalexperience.FinishOutcome, document *terminalexperience.PresentationDocument, workErr error) error {
	return errors.Join(workErr, run.Finish(terminalCMUseFinishRequest(outcome), document))
}

func terminalCMUseFinishRequest(outcome terminalexperience.FinishOutcome) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminalexperience.Succeeded:
		request.Summary = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleSuccess,
			Text: "Default CM profile set",
		}}}
	case terminalexperience.Failed:
		request.Summary = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleError,
			Text: "Unable to set default CM profile",
		}}}
	}
	return request
}

func terminalCMUseDocument(result UseResult) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleSuccess, Text: fmt.Sprintf("Default CM profile set to %s", result.Profile)}}}
}

func terminalCMUseRichDocument(result UseResult) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / config cm use"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Set default CM profile"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Choose the stored profile used for commit message generation"},
		{Role: terminalexperience.VisualRoleSuccess, Text: "Default CM profile set to " + safeCMUseProfile(result.Profile)},
	}}
}

func safeCMUseProfile(value string) string {
	if !utf8.ValidString(value) {
		return "Requested profile"
	}
	var output strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) {
			return "Requested profile"
		}
		output.WriteRune(character)
	}
	value = strings.TrimSpace(output.String())
	if value == "" {
		return "Requested profile"
	}
	runes := []rune(value)
	if len(runes) > 256 {
		return string(runes[:256]) + "..."
	}
	return value
}

var _ UseWriter = (*appconfig.Store)(nil)
