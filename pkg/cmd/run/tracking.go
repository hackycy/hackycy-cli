package run

import (
	"context"
	"errors"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

const (
	runResolveProjectPhaseID  = "resolve-project"
	runResolveManagerPhaseID  = "resolve-package-manager"
	runPrepareCommandPhaseID  = "prepare-child-command"
	runReleaseTerminalPhaseID = "release-terminal"
	runScriptFormID           = "script-selection"
	runManagerFormID          = "package-manager-selection"
	runWorkCatalogID          = "run-handoff"
)

var runPhaseDefinitions = []terminalexperience.PhaseDefinition{
	{ID: runResolveProjectPhaseID, Name: "Resolve project"},
	{ID: runResolveManagerPhaseID, Name: "Resolve package manager"},
	{ID: runPrepareCommandPhaseID, Name: "Prepare child command"},
	{ID: runReleaseTerminalPhaseID, Name: "Release terminal"},
}

func runPhaseDefinitionsFor(id string) []terminalexperience.PhaseDefinition {
	for _, definition := range runPhaseDefinitions {
		if definition.ID == id {
			return []terminalexperience.PhaseDefinition{definition}
		}
	}
	return nil
}

func runWorkCatalog() terminalexperience.WorkCatalog {
	return terminalexperience.WorkCatalog{
		ID:     runWorkCatalogID,
		Label:  "Run handoff",
		Phases: append([]terminalexperience.PhaseDefinition(nil), runPhaseDefinitions...),
	}
}

type runDetailedObserver interface {
	reportRunPhase(string, terminalexperience.PhaseState, string)
	reportRunMilestone(string)
}

func detailedRunObserver(presenter Presenter) runDetailedObserver {
	observer, _ := presenter.(runDetailedObserver)
	return observer
}

func reportRunPhase(observer runDetailedObserver, id string, state terminalexperience.PhaseState, detail string) {
	if observer != nil {
		observer.reportRunPhase(id, state, detail)
	}
}

func reportRunMilestone(observer runDetailedObserver, text string) {
	if observer != nil && text != "" {
		observer.reportRunMilestone(text)
	}
}

func runPhaseError(ctx context.Context, err error) terminalexperience.PhaseState {
	if err != nil && ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return terminalexperience.PhaseCancelled
	}
	return terminalexperience.PhaseFailed
}
