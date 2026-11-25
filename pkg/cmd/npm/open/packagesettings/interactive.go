package packagesettings

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
)

const (
	selectStep   = "select-packages"
	continueStep = "continue-opening"
)

var packageChecklist = terminal.WorkChecklistLabels{
	Active: "Opening", Completed: "Opened", Skipped: "Not selected", Confirm: "Open next", Decline: "Stop",
}

func openInteractively(options *Options, names []string) (runErr error) {
	ctx, cancel := context.WithCancel(options.Context)
	defer cancel()
	interactiveOptions := *options
	interactiveOptions.Context = ctx
	run, err := options.Terminal.OpenConsole(ctx, terminal.ConsoleDescriptor{
		Command: "ycy npm open package-settings",
		FormCatalog: []terminal.ConsoleFormStep{
			{ID: selectStep, Name: "Select packages"},
			{ID: continueStep, Name: "Open settings"},
		},
	})
	if err != nil {
		return err
	}

	rows := make([]terminal.OperationPhase, len(names))
	for index, name := range names {
		rows[index] = terminal.OperationPhase{ID: name, Name: name, State: terminal.PhasePending}
	}
	var failures []terminal.PresentationBlock
	stopped := false
	defer func() {
		outcome := terminal.Succeeded
		if len(failures) > 0 {
			runErr = errors.Join(runErr, fmt.Errorf("failed to open %d npm package settings page(s)", len(failures)))
		}
		switch {
		case ctx.Err() != nil:
			outcome = terminal.Cancelled
			// The Work session can also cancel an in-flight opener on Esc.
			if parentErr := options.Context.Err(); parentErr != nil && !errors.Is(runErr, parentErr) {
				runErr = errors.Join(runErr, parentErr)
			}
		case runErr != nil:
			outcome = terminal.Failed
		case stopped:
			outcome = terminal.Cancelled
		}
		summary, result := packageResults(rows)
		runErr = errors.Join(runErr, run.Finish(terminal.FinishRequest{Outcome: outcome, Summary: summary}, &result), run.Close())
		if len(failures) > 0 {
			runErr = errors.Join(runErr, terminal.WritePlain(options.Terminal.DiagnosticWriter(), terminal.PresentationDocument{Blocks: failures}))
		}
	}()

	answer, err := run.Ask(packageSelection(names))
	if errors.Is(err, terminal.ErrInteractionCancelled) {
		stopped = true
		return nil
	}
	if err != nil {
		return err
	}
	selected := make(map[string]bool, len(answer.Values))
	for _, name := range answer.Values {
		selected[name] = true
	}
	phases := make([]terminal.PhaseDefinition, len(rows))
	for index := range rows {
		row := &rows[index]
		phases[index] = terminal.PhaseDefinition{ID: row.ID, Name: row.Name, Skipped: !selected[row.ID]}
		if !selected[row.ID] {
			row.State = terminal.PhaseSkipped
		}
	}
	if len(selected) == 0 {
		return nil
	}
	work, err := terminal.StartWork(run, terminal.WorkCatalog{
		ID: "open-settings", Label: "npm package settings", Phases: phases, RequestCancel: cancel, Checklist: &packageChecklist,
	})
	if err != nil {
		return err
	}

	for index := range rows {
		row := &rows[index]
		if row.State == terminal.PhaseSkipped {
			continue
		}
		answer, err := run.Ask(terminal.InteractionRequest{
			Kind: terminal.InteractionConfirm, Message: "Open settings for " + row.Name + "?",
			PlainPrompt: "Open settings for " + row.Name + "? [Enter to open, n to stop]: ",
			HasDefault:  true, Default: terminal.InteractionAnswer{Confirmed: true},
			ConsoleStepID: continueStep, TranscriptLabel: "Open next package",
		})
		if errors.Is(err, terminal.ErrInteractionCancelled) || (err == nil && !answer.Confirmed) {
			stopped = true
			return nil
		}
		if ctx.Err() != nil && (err == nil || errors.Is(err, ctx.Err())) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := work.Update(terminal.OperationPhase{ID: row.ID, State: terminal.PhaseActive}); err != nil {
			return err
		}
		openErr := openPackage(&interactiveOptions, row.Name)
		switch {
		case openErr == nil:
			row.State = terminal.PhaseCompleted
		case ctx.Err() != nil:
			// An interrupted launch was not confirmed by the system.
			return nil
		default:
			row.State = terminal.PhaseFailed
			failures = append(failures, terminal.PresentationBlock{Text: logging.RedactDiagnostic(openErr.Error())})
		}
		if ctx.Err() != nil {
			return nil
		}
		if err := work.Update(terminal.OperationPhase{ID: row.ID, State: row.State}); err != nil {
			return err
		}
	}
	return nil
}

func packageSelection(names []string) terminal.InteractionRequest {
	choices := make([]terminal.InteractionOption, len(names))
	for index, name := range names {
		choices[index] = terminal.InteractionOption{Label: name, Value: name}
	}
	return terminal.InteractionRequest{
		Kind: terminal.InteractionMultiSelect, Message: "Select npm packages",
		Description: fmt.Sprintf("%d found · All selected by default", len(names)),
		Options:     choices, HasDefault: true, Default: terminal.InteractionAnswer{Values: names},
		ConsoleStepID: selectStep, TranscriptLabel: "Select packages",
		PlainPrompt:  "Packages to open [Enter for all, comma-separated numbers, none, q to cancel]: ",
		CancelValues: []string{"q"},
		ParsePlain:   func(line string) (terminal.InteractionAnswer, error) { return parsePackageSelection(line, names) },
	}
}

func parsePackageSelection(line string, names []string) (terminal.InteractionAnswer, error) {
	if strings.EqualFold(line, "none") {
		return terminal.InteractionAnswer{}, nil
	}
	var answer terminal.InteractionAnswer
	for item := range strings.SplitSeq(line, ",") {
		number, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || number < 1 || number > len(names) {
			return terminal.InteractionAnswer{}, fmt.Errorf("enter package numbers between 1 and %d, or none", len(names))
		}
		answer.Values = append(answer.Values, names[number-1])
	}
	return answer, nil
}

func packageResults(rows []terminal.OperationPhase) (terminal.PresentationDocument, terminal.PresentationDocument) {
	var opened, pending, failed, skipped int
	result := terminal.PresentationDocument{Blocks: make([]terminal.PresentationBlock, 1, len(rows)+1)}
	for _, row := range rows {
		var status string
		var role terminal.VisualRole
		switch row.State {
		case terminal.PhaseCompleted:
			status, role = "Opened", terminal.VisualRoleSuccess
			opened++
		case terminal.PhaseFailed:
			status, role = "Failed", terminal.VisualRoleError
			failed++
		case terminal.PhaseSkipped:
			status, role = "Not selected", terminal.VisualRoleMuted
			skipped++
		default:
			status, role = "Pending", terminal.VisualRoleMuted
			pending++
		}
		result.Blocks = append(result.Blocks, terminal.PresentationBlock{Role: role, Text: fmt.Sprintf("  %-12s  %s", status, row.Name)})
	}
	result.Blocks[0] = terminal.PresentationBlock{
		Role: terminal.VisualRoleTitle,
		Text: fmt.Sprintf("Opened %d · Pending %d · Failed %d · Not selected %d", opened, pending, failed, skipped),
	}
	return terminal.PresentationDocument{Blocks: result.Blocks[:1]}, result
}
