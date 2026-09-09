package connect

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

func terminalTunnelConnectionSelectorFor(experience *terminalexperience.Runtime) ClientConnectionSelector {
	if experience == nil || experience.Capabilities().Interaction == terminalexperience.Automation {
		return nil
	}
	return func(ctx context.Context, connections []appconfig.TunnelConnection) (string, bool, error) {
		run, err := experience.OpenConsole(ctx, tunnelConnectionSelectionConsoleDescriptor(len(connections)))
		if err != nil {
			return "", false, err
		}
		selected, cancelled, selectErr := newTerminalTunnelConnectionAdapter(run).Select(ctx, connections)
		return selected, cancelled, errors.Join(selectErr, run.Close())
	}
}

const tunnelConnectionSelectionStepID = "tunnel-connection-selection"

func tunnelConnectionSelectionConsoleDescriptor(candidateCount int) terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY",
		Target:  "Tunnel connection",
		Status:  "SELECT",
		Metadata: []terminalexperience.ConsoleMetadata{{
			Label: "candidates",
			Value: strconv.Itoa(candidateCount),
		}},
		FormCatalog: []terminalexperience.ConsoleFormStep{{
			ID:     tunnelConnectionSelectionStepID,
			Name:   "Tunnel connection",
			Detail: strconv.Itoa(candidateCount) + " remembered connections",
		}},
	}
}

type terminalTunnelConnectionAdapter struct {
	run terminalexperience.ExperienceRun
}

func newTerminalTunnelConnectionAdapter(run terminalexperience.ExperienceRun) *terminalTunnelConnectionAdapter {
	return &terminalTunnelConnectionAdapter{run: run}
}

func (adapter *terminalTunnelConnectionAdapter) Select(_ context.Context, connections []appconfig.TunnelConnection) (string, bool, error) {
	answer, err := adapter.run.Ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionSelect,
		Message:         "Select a tunnel connection",
		PlainLead:       "Select a tunnel connection",
		PlainPrompt:     "> ",
		ConsoleStepID:   tunnelConnectionSelectionStepID,
		Options:         tunnelConnectionInteractionOptions(connections),
		CancelValues:    []string{"", "q", "quit", "cancel"},
		TranscriptLabel: "Selected tunnel connection",
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string {
			return tunnelConnectionSelectionLabel(answer.Value, connections)
		},
		ParsePlain: func(value string) (terminalexperience.InteractionAnswer, error) {
			return parseTunnelConnectionSelection(value, connections)
		},
	})
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) {
		_ = adapter.run.Milestone(terminalTunnelConnectionDocument("Tunnel connection selection cancelled"))
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	return answer.Value, false, nil
}

func tunnelConnectionInteractionOptions(connections []appconfig.TunnelConnection) []terminalexperience.InteractionOption {
	origins := make([]string, len(connections))
	counts := make(map[string]int, len(connections))
	for index, connection := range connections {
		origin := normalizedTunnelConnectionOrigin(connection.Server)
		origins[index] = origin
		counts[origin]++
	}
	ordinals := make(map[string]int, len(counts))
	options := make([]terminalexperience.InteractionOption, 0, len(connections))
	for index, connection := range connections {
		origin := origins[index]
		label := origin + "  last authenticated " + tunnelConnectionAuthenticatedAt(connection.LastAuthenticatedAt)
		if counts[origin] > 1 {
			ordinals[origin]++
			label += "  (" + strconv.Itoa(ordinals[origin]) + ")"
		}
		options = append(options, terminalexperience.InteractionOption{
			Label: label,
			Value: connection.ID,
		})
	}
	return options
}

func tunnelConnectionSelectionLabel(value string, connections []appconfig.TunnelConnection) string {
	for index, connection := range connections {
		if connection.ID == value {
			return tunnelConnectionInteractionOptions(connections)[index].Label
		}
	}
	return "selected connection"
}

func normalizedTunnelConnectionOrigin(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return "unknown origin"
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "unknown origin"
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "unknown origin"
	}
	port := parsed.Port()
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host
}

func tunnelConnectionAuthenticatedAt(value string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return "unknown"
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

func parseTunnelConnectionSelection(value string, connections []appconfig.TunnelConnection) (terminalexperience.InteractionAnswer, error) {
	index, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || index < 1 || index > len(connections) {
		return terminalexperience.InteractionAnswer{}, errors.New("Invalid selection")
	}
	return terminalexperience.InteractionAnswer{Value: connections[index-1].ID}, nil
}

func terminalTunnelConnectionDocument(text string) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: text}}}
}
