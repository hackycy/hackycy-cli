package list

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunListWritesDeterministicSecretSafeAutomationOutput(t *testing.T) {
	connections := []appconfig.TunnelConnectionSummary{
		{ID: tunnelListTestID('a'), Server: "https://newer.example", LastAuthenticatedAt: "2026-09-19T00:00:00.000Z"},
		{ID: tunnelListTestID('b'), Server: "https://older.example", LastAuthenticatedAt: "2026-09-18T00:00:00.000Z"},
	}
	var output, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Input:        panicListReader{},
		Output:       &output,
		Diagnostics:  &diagnostics,
	})

	err := runList(&Options{
		Context: context.Background(),
		Store: func() (Reader, error) {
			return tunnelListReader{connections: connections}, nil
		},
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("runList() error = %v", err)
	}
	want := "Remembered Tunnel connections\nID  SERVER  LAST AUTHENTICATED\n" +
		connections[0].ID + "  https://newer.example  2026-09-19T00:00:00.000Z\n" +
		connections[1].ID + "  https://older.example  2026-09-18T00:00:00.000Z\n"
	if output.String() != want || diagnostics.Len() != 0 {
		t.Fatalf("streams = (%q, %q), want stdout %q", output.String(), diagnostics.String(), want)
	}
	if strings.Contains(output.String(), "client-token") || terminaltest.ContainsTerminalControl(output.Bytes()) {
		t.Fatalf("unsafe Automation output = %q", output.String())
	}
}

func TestRunListReportsEmptyCatalog(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mode terminalexperience.InteractionMode
	}{
		{name: "plain", mode: terminalexperience.PlainInteractive},
		{name: "rich fallback", mode: terminalexperience.RichInteractive},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: testCase.mode},
				Output:       &output,
			})
			err := runList(&Options{
				Store:    func() (Reader, error) { return tunnelListReader{}, nil },
				Terminal: experience,
			})
			if err != nil {
				t.Fatalf("runList() error = %v", err)
			}
			if got, want := output.String(), "Remembered Tunnel connections\nNo remembered Tunnel connections configured.\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if terminaltest.ContainsTerminalControl(output.Bytes()) {
				t.Fatalf("output contains terminal control: %q", output.String())
			}
		})
	}
}

func TestRunListPropagatesStoreAndReadFailures(t *testing.T) {
	wantStore := errors.New("open configuration")
	wantRead := errors.New("read configuration")
	for _, testCase := range []struct {
		name  string
		store StoreProvider
		want  error
	}{
		{name: "store", store: func() (Reader, error) { return nil, wantStore }, want: wantStore},
		{name: "read", store: func() (Reader, error) { return tunnelListReader{err: wantRead}, nil }, want: wantRead},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation}})
			if err := runList(&Options{Store: testCase.store, Terminal: experience}); !errors.Is(err, testCase.want) {
				t.Fatalf("runList() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

type tunnelListReader struct {
	connections []appconfig.TunnelConnectionSummary
	err         error
}

func (reader tunnelListReader) ListTunnelConnections() ([]appconfig.TunnelConnectionSummary, error) {
	return append([]appconfig.TunnelConnectionSummary(nil), reader.connections...), reader.err
}

type panicListReader struct{}

func (panicListReader) Read([]byte) (int, error) {
	panic("config tunnel list attempted to read Automation input")
}

func tunnelListTestID(character byte) string {
	return "v1_" + strings.Repeat(string(character), 43)
}
