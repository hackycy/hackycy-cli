package list

import (
	"context"
	"errors"
	"fmt"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Reader supplies the secret-safe remembered Tunnel connection projection.
type Reader interface {
	ListTunnelConnections() ([]appconfig.TunnelConnectionSummary, error)
}

// StoreProvider resolves the shared configuration store only when the command runs.
type StoreProvider func() (Reader, error)

// Options contains the parsed list request and leaf-owned capabilities.
type Options struct {
	Context  context.Context
	Store    StoreProvider
	Terminal *terminalexperience.Runtime
}

// NewCmdList creates the config tunnel list command with an optional test runner.
func NewCmdList(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runList
	}
	return &cobra.Command{
		Use:   "list",
		Short: "List remembered Tunnel connections",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
				return errors.New("config tunnel list Factory is incomplete")
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
		return errors.New("config tunnel list options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalTunnelListConsoleDescriptor())
	if err != nil {
		return err
	}
	defer run.Close()

	reader, err := options.Store()
	if err != nil {
		return finishTunnelList(run, terminalexperience.Failed, nil, err)
	}
	if reader == nil {
		return finishTunnelList(run, terminalexperience.Failed, nil, errors.New("config tunnel list reader is nil"))
	}
	connections, err := reader.ListTunnelConnections()
	if err != nil {
		return finishTunnelList(run, terminalexperience.Failed, nil, err)
	}
	if err := ctx.Err(); err != nil {
		return finishTunnelList(run, terminalexperience.Cancelled, nil, err)
	}
	document := terminalTunnelListDocument(connections)
	return run.Finish(terminalTunnelListFinishRequest(terminalexperience.Succeeded, len(connections)), &document)
}

func terminalTunnelListConsoleDescriptor() terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / config tunnel list",
		Target:  "remembered Tunnel connections",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{{
			Label: "scope",
			Value: "local configuration",
		}},
	}
}

func terminalTunnelListDocument(connections []appconfig.TunnelConnectionSummary) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleTitle, Text: "Remembered Tunnel connections"}}
	if len(connections) == 0 {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "No remembered Tunnel connections configured."})
		return terminalexperience.PresentationDocument{Blocks: blocks}
	}
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "ID  SERVER  LAST AUTHENTICATED"})
	for _, connection := range connections {
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRolePlain,
			Text: fmt.Sprintf("%s  %s  %s", connection.ID, connection.Server, connection.LastAuthenticatedAt),
		})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalTunnelListFinishRequest(outcome terminalexperience.FinishOutcome, count int) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminalexperience.Succeeded:
		request.Summary = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleSuccess,
			Text: fmt.Sprintf("Loaded %d remembered Tunnel connection%s", count, pluralSuffix(count)),
		}}}
	case terminalexperience.Failed:
		request.Summary = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleError, Text: "Unable to list remembered Tunnel connections"}}}
	}
	return request
}

func finishTunnelList(run terminalexperience.ExperienceRun, outcome terminalexperience.FinishOutcome, document *terminalexperience.PresentationDocument, workErr error) error {
	return errors.Join(workErr, run.Finish(terminalTunnelListFinishRequest(outcome, 0), document))
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

var _ Reader = (*appconfig.Store)(nil)
