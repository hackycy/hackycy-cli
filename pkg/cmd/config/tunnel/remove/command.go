package remove

import (
	"context"
	"errors"
	"fmt"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

var (
	errTunnelConnectionNotFound         = errors.New("Tunnel connection not found")
	errTunnelRemoveAutomationRequiresID = errors.New("config tunnel remove requires a connection ID and --force in a non-interactive environment")
)

const (
	tunnelRemoveSelectionStepID    = "tunnel-connection-selection"
	tunnelRemoveConfirmationStepID = "tunnel-connection-confirmation"
)

// Reader supplies the secret-safe remembered connections available for removal.
type Reader interface {
	ListTunnelConnections() ([]appconfig.TunnelConnectionSummary, error)
}

// Writer owns the semantic deletion of one remembered connection.
type Writer interface {
	RemoveTunnelConnection(string) (bool, error)
}

// StoreProvider resolves both configuration capabilities under one store lifetime.
type StoreProvider func() (Reader, Writer, error)

// Options contains the parsed remove request and leaf-owned capabilities.
type Options struct {
	Context      context.Context
	ConnectionID string
	Force        bool
	Store        StoreProvider
	Terminal     *terminalexperience.Runtime
}

// NewCmdRemove creates the config tunnel remove command with an optional test runner.
func NewCmdRemove(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runRemove
	}
	var force bool
	command := &cobra.Command{
		Use:   "remove [connection-id]",
		Short: "Remove a locally remembered Tunnel connection",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
				return errors.New("config tunnel remove Factory is incomplete")
			}
			connectionID := ""
			if len(arguments) == 1 {
				connectionID = arguments[0]
			}
			return runF(&Options{
				Context:      command.Context(),
				ConnectionID: connectionID,
				Force:        force,
				Store: func() (Reader, Writer, error) {
					store, err := factory.ConfigStore()
					if err != nil {
						return nil, nil, err
					}
					return store, store, nil
				},
				Terminal: factory.Terminal,
			})
		},
	}
	command.Flags().BoolVarP(&force, "force", "f", false, "Skip the removal confirmation")
	return command
}

func runRemove(options *Options) error {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return errors.New("config tunnel remove options are incomplete")
	}
	if options.Terminal.Capabilities().Interaction == terminalexperience.Automation && (options.ConnectionID == "" || !options.Force) {
		return errTunnelRemoveAutomationRequiresID
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalTunnelRemoveConsoleDescriptor())
	if err != nil {
		return err
	}
	defer run.Close()

	reader, writer, err := options.Store()
	if err != nil {
		return finishTunnelRemove(run, terminalexperience.Failed, nil, err)
	}
	if reader == nil || writer == nil {
		return finishTunnelRemove(run, terminalexperience.Failed, nil, errors.New("config tunnel remove store is incomplete"))
	}
	connections, err := reader.ListTunnelConnections()
	if err != nil {
		return finishTunnelRemove(run, terminalexperience.Failed, nil, err)
	}
	if err := ctx.Err(); err != nil {
		return finishTunnelRemove(run, terminalexperience.Cancelled, nil, err)
	}

	connectionID := options.ConnectionID
	if connectionID == "" {
		if len(connections) == 0 {
			document := tunnelRemoveDocument(terminalexperience.VisualRoleWarning, "No remembered Tunnel connections configured. Nothing to remove.")
			return run.Finish(terminalTunnelRemoveFinishRequest(terminalexperience.Succeeded), &document)
		}
		connectionID, err = selectTunnelConnection(run, connections)
		if errors.Is(err, terminalexperience.ErrInteractionCancelled) {
			return finishTunnelRemoveCancellation(run)
		}
		if err != nil {
			return finishTunnelRemove(run, terminalexperience.Failed, nil, err)
		}
	}
	target, found := tunnelConnectionByID(connections, connectionID)
	if !found {
		return finishTunnelRemove(run, terminalexperience.Failed, nil, errTunnelConnectionNotFound)
	}

	if !options.Force {
		confirmed, err := confirmTunnelConnectionRemoval(run, target)
		if errors.Is(err, terminalexperience.ErrInteractionCancelled) {
			return finishTunnelRemoveCancellation(run)
		}
		if err != nil {
			return finishTunnelRemove(run, terminalexperience.Failed, nil, err)
		}
		if !confirmed {
			return finishTunnelRemoveCancellation(run)
		}
	}
	if err := ctx.Err(); err != nil {
		return finishTunnelRemove(run, terminalexperience.Cancelled, nil, err)
	}
	removed, err := writer.RemoveTunnelConnection(target.ID)
	if err != nil {
		return finishTunnelRemove(run, terminalexperience.Failed, nil, err)
	}
	if !removed {
		return finishTunnelRemove(run, terminalexperience.Failed, nil, errTunnelConnectionNotFound)
	}
	document := tunnelRemoveDocument(
		terminalexperience.VisualRoleSuccess,
		fmt.Sprintf("Removed local remembered Tunnel connection %s (%s). The server-side Trusted Client was not deleted.", target.ID, target.Server),
	)
	return run.Finish(terminalTunnelRemoveFinishRequest(terminalexperience.Succeeded), &document)
}

func selectTunnelConnection(run terminalexperience.ExperienceRun, connections []appconfig.TunnelConnectionSummary) (string, error) {
	options := make([]terminalexperience.InteractionOption, len(connections))
	for index, connection := range connections {
		options[index] = terminalexperience.InteractionOption{
			Label:       connection.Server,
			Value:       connection.ID,
			Description: connection.LastAuthenticatedAt + "  " + connection.ID,
		}
	}
	answer, err := run.Ask(terminalexperience.InteractionRequest{
		Kind:              terminalexperience.InteractionSelect,
		Message:           "Select a remembered Tunnel connection to remove",
		PlainLead:         "Select a remembered Tunnel connection to remove",
		PlainPrompt:       "> ",
		ConsoleStepID:     tunnelRemoveSelectionStepID,
		Options:           options,
		CancelValues:      []string{"", "q", "quit", "cancel"},
		TranscriptLabel:   "Selected Tunnel connection",
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string { return answer.Value },
	})
	if err != nil {
		return "", err
	}
	return answer.Value, nil
}

func confirmTunnelConnectionRemoval(run terminalexperience.ExperienceRun, connection appconfig.TunnelConnectionSummary) (bool, error) {
	answer, err := run.Ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionConfirm,
		Message:         "Remove this local remembered Tunnel connection?",
		Description:     fmt.Sprintf("%s  last authenticated %s  %s\nThis does not delete the server-side Trusted Client or stop a running client.", connection.Server, connection.LastAuthenticatedAt, connection.ID),
		ConsoleStepID:   tunnelRemoveConfirmationStepID,
		HasDefault:      true,
		Default:         terminalexperience.InteractionAnswer{Confirmed: false},
		CancelValues:    []string{"q", "quit", "cancel"},
		TranscriptLabel: "Removal confirmation",
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string {
			if answer.Confirmed {
				return "confirmed"
			}
			return "declined"
		},
	})
	if err != nil {
		return false, err
	}
	return answer.Confirmed, nil
}

func tunnelConnectionByID(connections []appconfig.TunnelConnectionSummary, id string) (appconfig.TunnelConnectionSummary, bool) {
	for _, connection := range connections {
		if connection.ID == id {
			return connection, true
		}
	}
	return appconfig.TunnelConnectionSummary{}, false
}

func terminalTunnelRemoveConsoleDescriptor() terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command:  "YCY / config tunnel remove",
		Target:   "local remembered Tunnel connection",
		Status:   "READY",
		Metadata: []terminalexperience.ConsoleMetadata{{Label: "scope", Value: "local configuration"}},
		FormCatalog: []terminalexperience.ConsoleFormStep{
			{ID: tunnelRemoveSelectionStepID, Name: "Selection", Detail: "remembered connection"},
			{ID: tunnelRemoveConfirmationStepID, Name: "Confirmation", Detail: "default No"},
		},
	}
}

func terminalTunnelRemoveFinishRequest(outcome terminalexperience.FinishOutcome) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminalexperience.Succeeded:
		request.Summary = tunnelRemoveDocument(terminalexperience.VisualRoleSuccess, "Local Tunnel connection configuration updated")
	case terminalexperience.Cancelled:
		request.Summary = tunnelRemoveDocument(terminalexperience.VisualRoleWarning, "Tunnel connection removal cancelled")
	case terminalexperience.Failed:
		request.Summary = tunnelRemoveDocument(terminalexperience.VisualRoleError, "Unable to remove remembered Tunnel connection")
	}
	return request
}

func finishTunnelRemoveCancellation(run terminalexperience.ExperienceRun) error {
	document := tunnelRemoveDocument(terminalexperience.VisualRoleWarning, "Cancelled. No local Tunnel connection was removed.")
	return run.Finish(terminalTunnelRemoveFinishRequest(terminalexperience.Cancelled), &document)
}

func finishTunnelRemove(run terminalexperience.ExperienceRun, outcome terminalexperience.FinishOutcome, document *terminalexperience.PresentationDocument, workErr error) error {
	return errors.Join(workErr, run.Finish(terminalTunnelRemoveFinishRequest(outcome), document))
}

func tunnelRemoveDocument(role terminalexperience.VisualRole, text string) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

var _ Reader = (*appconfig.Store)(nil)
var _ Writer = (*appconfig.Store)(nil)
