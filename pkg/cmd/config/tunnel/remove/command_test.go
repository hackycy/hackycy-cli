package remove

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestRunRemoveAutomationRequiresIDAndForceBeforeResolvingStore(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		id    string
		force bool
	}{
		{name: "missing both"},
		{name: "missing force", id: tunnelRemoveTestID('a')},
		{name: "missing id", force: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			storeCalls := 0
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
				Input:        panicRemoveReader{},
			})
			err := runRemove(&Options{
				ConnectionID: testCase.id,
				Force:        testCase.force,
				Store: func() (Reader, Writer, error) {
					storeCalls++
					return nil, nil, nil
				},
				Terminal: experience,
			})
			if !errors.Is(err, errTunnelRemoveAutomationRequiresID) || storeCalls != 0 {
				t.Fatalf("runRemove() = %v, store calls = %d", err, storeCalls)
			}
		})
	}
}

func TestRunRemoveAutomationDeletesExactConnectionWithoutPrompting(t *testing.T) {
	target := tunnelRemoveConnection('a', "https://tunnel.example", "2026-09-19T00:00:00.000Z")
	store := &tunnelRemoveStore{connections: []appconfig.TunnelConnectionSummary{target}}
	var output, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Input:        panicRemoveReader{},
		Output:       &output,
		Diagnostics:  &diagnostics,
	})

	err := runRemove(&Options{ConnectionID: target.ID, Force: true, Store: removeStoreProvider(store), Terminal: experience})
	if err != nil {
		t.Fatalf("runRemove() error = %v", err)
	}
	if len(store.removed) != 1 || store.removed[0] != target.ID {
		t.Fatalf("removed IDs = %#v", store.removed)
	}
	for _, expected := range []string{target.ID, target.Server, "server-side Trusted Client was not deleted"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("stdout = %q, missing %q", output.String(), expected)
		}
	}
	if diagnostics.Len() != 0 || terminaltest.ContainsTerminalControl(output.Bytes()) {
		t.Fatalf("Automation streams = (%q, %q)", output.String(), diagnostics.String())
	}
}

func TestRunRemoveInteractiveSelectsAndConfirms(t *testing.T) {
	first := tunnelRemoveConnection('a', "https://first.example", "2026-09-19T00:00:00.000Z")
	second := tunnelRemoveConnection('b', "https://second.example", "2026-09-18T00:00:00.000Z")
	store := &tunnelRemoveStore{connections: []appconfig.TunnelConnectionSummary{first, second}}
	var output, diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("2\ny\n"),
		Output:       &output,
		Diagnostics:  &diagnostics,
	})

	if err := runRemove(&Options{Store: removeStoreProvider(store), Terminal: experience}); err != nil {
		t.Fatalf("runRemove() error = %v", err)
	}
	if len(store.removed) != 1 || store.removed[0] != second.ID {
		t.Fatalf("removed IDs = %#v", store.removed)
	}
	for _, expected := range []string{"Select a remembered Tunnel connection", first.ID, second.ID, second.Server} {
		if !strings.Contains(diagnostics.String(), expected) {
			t.Fatalf("diagnostics = %q, missing %q", diagnostics.String(), expected)
		}
	}
	if !strings.Contains(output.String(), second.ID) || terminaltest.ContainsTerminalControl(append(output.Bytes(), diagnostics.Bytes()...)) {
		t.Fatalf("streams = (%q, %q)", output.String(), diagnostics.String())
	}
}

func TestRunRemoveDeclineAndCancellationDoNotMutate(t *testing.T) {
	target := tunnelRemoveConnection('a', "https://tunnel.example", "2026-09-19T00:00:00.000Z")
	for _, testCase := range []struct {
		name  string
		id    string
		input string
	}{
		{name: "direct decline", id: target.ID, input: "n\n"},
		{name: "selection cancelled", input: "q\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &tunnelRemoveStore{connections: []appconfig.TunnelConnectionSummary{target}}
			var output, diagnostics bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
				Input:        strings.NewReader(testCase.input),
				Output:       &output,
				Diagnostics:  &diagnostics,
			})
			if err := runRemove(&Options{ConnectionID: testCase.id, Store: removeStoreProvider(store), Terminal: experience}); err != nil {
				t.Fatalf("runRemove() error = %v", err)
			}
			if len(store.removed) != 0 || !strings.Contains(output.String(), "No local Tunnel connection was removed") {
				t.Fatalf("removed = %#v, stdout = %q", store.removed, output.String())
			}
			if testCase.id != "" && (!strings.Contains(diagnostics.String(), target.Server) || !strings.Contains(diagnostics.String(), target.LastAuthenticatedAt)) {
				t.Fatalf("direct confirmation diagnostics = %q", diagnostics.String())
			}
		})
	}
}

func TestRunRemoveHandlesEmptyMissingAndConcurrentRemoval(t *testing.T) {
	target := tunnelRemoveConnection('a', "https://tunnel.example", "2026-09-19T00:00:00.000Z")

	t.Run("empty interactive catalog", func(t *testing.T) {
		store := &tunnelRemoveStore{}
		var output bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive}, Output: &output})
		if err := runRemove(&Options{Store: removeStoreProvider(store), Terminal: experience}); err != nil || len(store.removed) != 0 || !strings.Contains(output.String(), "Nothing to remove") {
			t.Fatalf("runRemove() = %v, removed = %#v, stdout = %q", err, store.removed, output.String())
		}
	})

	for _, testCase := range []struct {
		name  string
		store *tunnelRemoveStore
		id    string
	}{
		{name: "missing", store: &tunnelRemoveStore{connections: []appconfig.TunnelConnectionSummary{target}}, id: tunnelRemoveTestID('b')},
		{name: "concurrent removal", store: &tunnelRemoveStore{connections: []appconfig.TunnelConnectionSummary{target}, removeResult: boolPointer(false)}, id: target.ID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation}})
			err := runRemove(&Options{ConnectionID: testCase.id, Force: true, Store: removeStoreProvider(testCase.store), Terminal: experience})
			if !errors.Is(err, errTunnelConnectionNotFound) {
				t.Fatalf("runRemove() error = %v", err)
			}
		})
	}
}

func TestRunRemovePropagatesReadAndWriteFailuresWithoutSuccess(t *testing.T) {
	target := tunnelRemoveConnection('a', "https://tunnel.example", "2026-09-19T00:00:00.000Z")
	wantRead := errors.New("read configuration")
	wantWrite := errors.New("write configuration")
	for _, testCase := range []struct {
		name  string
		store *tunnelRemoveStore
		want  error
	}{
		{name: "read", store: &tunnelRemoveStore{readErr: wantRead}, want: wantRead},
		{name: "write", store: &tunnelRemoveStore{connections: []appconfig.TunnelConnectionSummary{target}, removeErr: wantWrite}, want: wantWrite},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
				Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
				Output:       &output,
			})
			err := runRemove(&Options{ConnectionID: target.ID, Force: true, Store: removeStoreProvider(testCase.store), Terminal: experience})
			if !errors.Is(err, testCase.want) || output.Len() != 0 {
				t.Fatalf("runRemove() = %v, stdout = %q", err, output.String())
			}
		})
	}
}

func TestNewCmdRemoveParsesOptionalIDAndForce(t *testing.T) {
	var captured *Options
	command := NewCmdRemove(&cmdutil.Factory{ConfigStore: func() (*appconfig.Store, error) { return nil, nil }, Terminal: terminalexperience.NewExperience(terminalexperience.ExperienceOptions{})}, func(options *Options) error {
		captured = options
		return nil
	})
	command.SetArgs([]string{tunnelRemoveTestID('a'), "--force"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if captured == nil || captured.ConnectionID != tunnelRemoveTestID('a') || !captured.Force {
		t.Fatalf("captured options = %#v", captured)
	}
}

type tunnelRemoveStore struct {
	connections  []appconfig.TunnelConnectionSummary
	readErr      error
	removeErr    error
	removeResult *bool
	removed      []string
}

func (store *tunnelRemoveStore) ListTunnelConnections() ([]appconfig.TunnelConnectionSummary, error) {
	return append([]appconfig.TunnelConnectionSummary(nil), store.connections...), store.readErr
}

func (store *tunnelRemoveStore) RemoveTunnelConnection(id string) (bool, error) {
	store.removed = append(store.removed, id)
	if store.removeResult != nil {
		return *store.removeResult, store.removeErr
	}
	return true, store.removeErr
}

func removeStoreProvider(store *tunnelRemoveStore) StoreProvider {
	return func() (Reader, Writer, error) { return store, store, nil }
}

type panicRemoveReader struct{}

func (panicRemoveReader) Read([]byte) (int, error) {
	panic("config tunnel remove attempted to read Automation input")
}

func tunnelRemoveConnection(character byte, server, authenticatedAt string) appconfig.TunnelConnectionSummary {
	return appconfig.TunnelConnectionSummary{ID: tunnelRemoveTestID(character), Server: server, LastAuthenticatedAt: authenticatedAt}
}

func tunnelRemoveTestID(character byte) string {
	return "v1_" + strings.Repeat(string(character), 43)
}

func boolPointer(value bool) *bool { return &value }
