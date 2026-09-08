package pulse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

var errGitPulseRequiresInteractive = errors.New("git pulse requires an interactive terminal")

const (
	pulseWorkCatalogID  = "git-pulse-work"
	pulsePreparePhaseID = "prepare-workspace"
	pulseScanPhaseID    = "scan-repositories"
	pulseFetchPhaseID   = "fetch-commits"
	pulseBuildPhaseID   = "build-commit-tree"
	pulseDateFormID     = "date-range"
	pulseAuthorFormID   = "author-filter"

	pulsePreparePhaseName = "Prepare workspace"
	pulseScanPhaseName    = "Scan repositories"
	pulseFetchPhaseName   = "Fetch commits"
	pulseBuildPhaseName   = "Build commit tree"
	pulseRichDefaultWidth = 80
	pulseRichNarrowWidth  = 80
	pulseWarningPathLimit = 5
)

func runPulse(options *Options) error {
	if options == nil || options.Context == nil || options.WorkingDirectory == nil || options.Terminal == nil || options.Git == nil || options.Now == nil {
		return errors.New("git pulse options are incomplete")
	}
	ctx, cancel := context.WithCancel(options.Context)
	defer cancel()
	run, err := options.Terminal.OpenConsole(ctx, terminalPulseConsoleDescriptor(options))
	if err != nil {
		return err
	}
	defer run.Close()
	adapter := newTerminalPulseAdapter(run, cancel, terminalPulseAdapterConfig{
		Capabilities: options.Terminal.Capabilities(),
		Diagnostics:  options.Terminal.DiagnosticWriter(),
		Width:        options.Width,
	})
	module, err := New(Dependencies{
		WorkingDirectory: options.WorkingDirectory,
		Stater:           osPathStater{},
		Reader:           osDirectoryReader{},
		Yield:            runtime.Gosched,
		Git:              gitRunnerAdapter{runner: options.Git},
		Prompter:         adapter,
		Presenter:        adapter,
		Tracker:          adapter,
		Now:              options.Now,
	})
	if err != nil {
		return errors.Join(err, run.Finish(adapter.FinishRequest(terminalexperience.Failed), nil))
	}
	_, workErr := module.Run(ctx, Input{Directory: options.Directory, Days: options.Days})
	workErr = errors.Join(workErr, adapter.CloseWork())
	if presentationErr := adapter.PresentationError(); presentationErr != nil {
		workErr = errors.Join(workErr, presentationErr)
	}
	outcome := terminalexperience.Failed
	var document *terminalexperience.PresentationDocument
	if workErr == nil {
		outcome = adapter.FinishOutcome()
		document = adapter.FinishDocument()
	} else if isPulseCancellation(workErr) {
		outcome = terminalexperience.Cancelled
	}
	return errors.Join(workErr, run.Finish(adapter.FinishRequest(outcome), document))
}

func terminalPulseConsoleDescriptor(options *Options) terminalexperience.ConsoleDescriptor {
	directory := "workspace"
	rangeLabel := "interactive date range"
	formCatalog := []terminalexperience.ConsoleFormStep{
		{ID: pulseAuthorFormID, Name: "Filter by authors", Detail: "choose authors"},
	}
	if options != nil {
		directory = pulseDescriptorDirectory(options.Directory)
		if options.Days != nil {
			rangeLabel = fmt.Sprintf("%d days", *options.Days)
		} else {
			formCatalog = append([]terminalexperience.ConsoleFormStep{{
				ID:     pulseDateFormID,
				Name:   "Select date range",
				Detail: "choose calendar range",
			}}, formCatalog...)
		}
	} else {
		formCatalog = append([]terminalexperience.ConsoleFormStep{{
			ID:     pulseDateFormID,
			Name:   "Select date range",
			Detail: "choose calendar range",
		}}, formCatalog...)
	}
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / git pulse",
		Target:  "workspace commit activity",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "scope", Value: "workspace Git history"},
			{Label: "directory", Value: directory},
			{Label: "range", Value: safePulseField(rangeLabel, 80)},
		},
		FormCatalog: formCatalog,
	}
}

func pulseDescriptorDirectory(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "workspace"
	}
	if filepath.IsAbs(value) {
		base := filepath.Base(filepath.Clean(value))
		if base == string(filepath.Separator) || base == "." || base == "" {
			return "workspace"
		}
		return safePulseField(base, 120)
	}
	return safePulseField(filepath.Clean(value), 120)
}

type osPathStater struct{}

func (osPathStater) Stat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}

type osDirectoryReader struct{}

func (osDirectoryReader) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

type terminalPulseAdapter struct {
	run           terminalexperience.ExperienceRun
	requestCancel context.CancelFunc
	capabilities  terminalexperience.Capabilities
	diagnostics   io.Writer
	width         int

	mu              sync.Mutex
	presentationErr error
	finishOutcome   terminalexperience.FinishOutcome
	finishDocument  *terminalexperience.PresentationDocument
	root            string
	work            terminalexperience.WorkSession
	workStarted     bool
	workClosed      bool
	workErr         error
	lastPhase       PhaseKind
	hasLastPhase    bool
	finishSummary   string
}

type terminalPulseAdapterConfig struct {
	Capabilities terminalexperience.Capabilities
	Diagnostics  io.Writer
	Width        int
}

func newTerminalPulseAdapter(run terminalexperience.ExperienceRun, requestCancel context.CancelFunc, configurations ...terminalPulseAdapterConfig) *terminalPulseAdapter {
	configuration := terminalPulseAdapterConfig{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Diagnostics:  io.Discard,
	}
	if len(configurations) > 0 {
		configuration = configurations[0]
		if configuration.Diagnostics == nil {
			configuration.Diagnostics = io.Discard
		}
	}
	return &terminalPulseAdapter{
		run:           run,
		requestCancel: requestCancel,
		capabilities:  configuration.Capabilities,
		diagnostics:   configuration.Diagnostics,
		width:         configuration.Width,
	}
}

func (adapter *terminalPulseAdapter) SelectDays(prompt DayPrompt) (int, bool, error) {
	answer, cancelled, err := adapter.ask(pulseDayRequest(prompt))
	if cancelled {
		adapter.pulseMilestone("Date range selection cancelled")
		return 0, true, nil
	}
	if err != nil {
		return 0, cancelled, err
	}
	days, err := strconv.Atoi(answer.Value)
	if err != nil {
		return 0, false, err
	}
	return days, false, nil
}

func (adapter *terminalPulseAdapter) SelectAuthors(prompt AuthorPrompt) ([]string, bool, error) {
	answer, cancelled, err := adapter.ask(pulseAuthorRequest(prompt))
	if cancelled {
		adapter.pulseMilestone("Author filter cancelled")
		return nil, true, nil
	}
	if err != nil {
		return nil, cancelled, err
	}
	return append([]string(nil), answer.Values...), false, nil
}

func (adapter *terminalPulseAdapter) Introduction(root string) {
	adapter.mu.Lock()
	adapter.root = root
	adapter.mu.Unlock()
	document := terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / git pulse"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Workspace commit activity"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Inspect repositories and group recent commits"},
	}}
	adapter.recordPresentation(adapter.run.Milestone(document))
	adapter.recordPresentation(adapter.run.Notice(pulseDocument("Workspace: "+safePulseField(root, 160), terminalexperience.VisualRoleMuted)))
}

func (adapter *terminalPulseAdapter) RepositoriesFound(count int) {
	adapter.pulseMilestone(fmt.Sprintf("Found %d %s", count, pulsePlural(count, "repository", "repositories")))
}

func (adapter *terminalPulseAdapter) NoRepositories() {
	adapter.setFinish(terminalexperience.Succeeded, pulseDocument("No Git repositories found.", terminalexperience.VisualRoleWarning), "No Git repositories found.")
}

func (adapter *terminalPulseAdapter) NoCommits() {
	adapter.setFinish(terminalexperience.Succeeded, pulseDocument("No commits found in the specified date range.", terminalexperience.VisualRoleWarning), "No commits found in the specified date range.")
}

func (adapter *terminalPulseAdapter) Cancelled() {
	adapter.setFinish(terminalexperience.Cancelled, pulseDocument("Operation cancelled.", terminalexperience.VisualRoleWarning), "Operation cancelled.")
}

func (adapter *terminalPulseAdapter) Present(report Report) {
	summary := fmt.Sprintf("Found %d %s in %d %s", report.CommitCount, pulsePlural(report.CommitCount, "commit", "commits"), len(report.Repositories), pulsePlural(len(report.Repositories), "repository", "repositories"))
	if adapter.richOutput() {
		adapter.setFinish(terminalexperience.Succeeded, terminalPulseRichDocumentForWidth(adapter.Root(), report, adapter.width), summary)
		return
	}
	adapter.setFinish(terminalexperience.Succeeded, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRolePlain,
		Text: pulseReportText(report),
	}}}, summary)
}

func (adapter *terminalPulseAdapter) Start(_ context.Context, kind PhaseKind) (PhaseReporter, error) {
	work, err := adapter.ensureWork()
	if err != nil {
		adapter.recordPresentation(err)
		if adapter.requestCancel != nil {
			adapter.requestCancel()
		}
		return nil, err
	}
	return &terminalPulsePhaseReporter{adapter: adapter, work: work, kind: kind}, nil
}

func (adapter *terminalPulseAdapter) ensureWork() (terminalexperience.WorkSession, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.workStarted {
		return adapter.work, adapter.workErr
	}
	adapter.workStarted = true
	work, err := terminalexperience.StartWork(adapter.run, terminalexperience.WorkCatalog{
		ID:            pulseWorkCatalogID,
		Label:         "Git Pulse",
		Phases:        pulseWorkPhaseCatalog(),
		RequestCancel: adapter.requestCancel,
	})
	adapter.work = work
	adapter.workErr = err
	return work, err
}

func (adapter *terminalPulseAdapter) CloseWork() error {
	adapter.mu.Lock()
	if adapter.workClosed {
		err := adapter.workErr
		adapter.mu.Unlock()
		return err
	}
	adapter.workClosed = true
	work := adapter.work
	adapter.mu.Unlock()
	if work == nil {
		return nil
	}
	err := work.Close()
	adapter.mu.Lock()
	adapter.workErr = errors.Join(adapter.workErr, err)
	err = adapter.workErr
	adapter.mu.Unlock()
	if err != nil && adapter.requestCancel != nil {
		adapter.requestCancel()
	}
	return err
}

func pulseWorkPhaseCatalog() []terminalexperience.PhaseDefinition {
	return []terminalexperience.PhaseDefinition{
		{ID: pulsePreparePhaseID, Name: pulsePreparePhaseName},
		{ID: pulseScanPhaseID, Name: pulseScanPhaseName},
		{ID: pulseFetchPhaseID, Name: pulseFetchPhaseName},
		{ID: pulseBuildPhaseID, Name: pulseBuildPhaseName},
	}
}

func (adapter *terminalPulseAdapter) ask(request terminalexperience.InteractionRequest) (terminalexperience.InteractionAnswer, bool, error) {
	answer, err := adapter.run.Ask(request)
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) || errors.Is(err, context.Canceled) {
		return terminalexperience.InteractionAnswer{}, true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return terminalexperience.InteractionAnswer{}, false, errGitPulseRequiresInteractive
	}
	if err != nil {
		return terminalexperience.InteractionAnswer{}, false, err
	}
	return answer, false, nil
}

func (adapter *terminalPulseAdapter) PulseDateSelection(days int, explicit bool, boundary string) {
	label := fmt.Sprintf("%d days", days)
	if !explicit {
		label = pulseDayLabel(days)
	}
	adapter.pulseMilestone(fmt.Sprintf("Date range: %s (since %s)", label, safePulseValue(boundary)))
}

func (adapter *terminalPulseAdapter) PulseAuthorFilterAll(authorCount int) {
	adapter.pulseMilestone(fmt.Sprintf("Author filter: All commits (%d authors)", authorCount))
}

func (adapter *terminalPulseAdapter) PulseScanWarning(root string, paths []string) {
	adapter.pulseWarning(fmt.Sprintf("Skipped %d unreadable directories: %s", len(paths), pulseWarningPaths(root, paths)))
}

func (adapter *terminalPulseAdapter) PulseFetchWarning(root string, paths []string) {
	adapter.pulseWarning(fmt.Sprintf("Skipped %d repositories while reading commits: %s", len(paths), pulseWarningPaths(root, paths)))
}

func (adapter *terminalPulseAdapter) pulseMilestone(text string) {
	if adapter.automation() {
		return
	}
	adapter.recordPresentation(adapter.run.Milestone(pulseDocument(text, terminalexperience.VisualRoleMuted)))
}

func (adapter *terminalPulseAdapter) pulseWarning(text string) {
	document := pulseDocument(text, terminalexperience.VisualRoleWarning)
	if adapter.automation() {
		adapter.recordPresentation(terminalexperience.WritePlain(adapter.diagnostics, document))
		return
	}
	adapter.recordPresentation(adapter.run.Milestone(document))
}

func (adapter *terminalPulseAdapter) automation() bool {
	return adapter.capabilities.Interaction == terminalexperience.Automation
}

func (adapter *terminalPulseAdapter) richOutput() bool {
	return adapter.capabilities.Interaction == terminalexperience.RichInteractive && adapter.capabilities.Stdout.Terminal
}

func (adapter *terminalPulseAdapter) setFinish(outcome terminalexperience.FinishOutcome, document terminalexperience.PresentationDocument, summary string) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.finishDocument != nil {
		return
	}
	adapter.finishOutcome = outcome
	adapter.finishDocument = &document
	adapter.finishSummary = summary
}

func (adapter *terminalPulseAdapter) FinishOutcome() terminalexperience.FinishOutcome {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.finishOutcome == 0 {
		return terminalexperience.Succeeded
	}
	return adapter.finishOutcome
}

func (adapter *terminalPulseAdapter) FinishDocument() *terminalexperience.PresentationDocument {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.finishDocument != nil {
		document := *adapter.finishDocument
		return &document
	}
	return nil
}

func (adapter *terminalPulseAdapter) FinishRequest(outcome terminalexperience.FinishOutcome) terminalexperience.FinishRequest {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	summary := adapter.finishSummary
	if summary == "" {
		switch outcome {
		case terminalexperience.Cancelled:
			summary = "Workspace inspection cancelled."
		case terminalexperience.Failed:
			summary = "Unable to inspect workspace commit activity."
		default:
			summary = "Workspace commit activity complete."
		}
	}
	request := terminalexperience.FinishRequest{
		Outcome: outcome,
		Summary: pulseDocument(summary, terminalexperience.VisualRoleMuted),
	}
	if outcome != terminalexperience.Succeeded && adapter.hasLastPhase {
		_, request.Location = terminalPulsePhaseDefinition(adapter.lastPhase)
	}
	return request
}

func (adapter *terminalPulseAdapter) Root() string {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.root
}

func (adapter *terminalPulseAdapter) recordPresentation(err error) {
	if err == nil {
		return
	}
	adapter.mu.Lock()
	adapter.presentationErr = errors.Join(adapter.presentationErr, err)
	adapter.mu.Unlock()
}

func (adapter *terminalPulseAdapter) recordPhase(phase Phase) {
	adapter.mu.Lock()
	adapter.lastPhase = phase.Kind
	adapter.hasLastPhase = true
	adapter.mu.Unlock()
}

func (adapter *terminalPulseAdapter) PresentationError() error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.presentationErr
}

func pulseDayRequest(prompt DayPrompt) terminalexperience.InteractionRequest {
	options := make([]terminalexperience.InteractionOption, 0, len(prompt.Options))
	for _, option := range prompt.Options {
		options = append(options, terminalexperience.InteractionOption{Label: option.Label, Value: strconv.Itoa(option.Value)})
	}
	return terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionSelect,
		Message:         prompt.Message,
		PlainLead:       prompt.Message,
		PlainPrompt:     "> ",
		Options:         options,
		CancelValues:    []string{"", "q", "quit", "cancel"},
		ConsoleStepID:   pulseDateFormID,
		TranscriptLabel: "Date range",
		ParsePlain: func(value string) (terminalexperience.InteractionAnswer, error) {
			index, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || index < 1 || index > len(options) {
				return terminalexperience.InteractionAnswer{}, errors.New("Invalid selection")
			}
			return terminalexperience.InteractionAnswer{Value: options[index-1].Value}, nil
		},
	}
}

func pulseAuthorRequest(prompt AuthorPrompt) terminalexperience.InteractionRequest {
	options := pulseAuthorInteractionOptions(prompt.Options)
	request := terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionMultiSelect,
		Message:         prompt.Message,
		PlainLead:       prompt.Message,
		PlainPrompt:     "> ",
		Options:         options,
		CancelValues:    []string{"q", "quit", "cancel"},
		ConsoleStepID:   pulseAuthorFormID,
		TranscriptLabel: "Author filter",
		ParsePlain: func(value string) (terminalexperience.InteractionAnswer, error) {
			indices := strings.FieldsFunc(value, func(character rune) bool {
				return character == ',' || character == ' ' || character == '\t'
			})
			if len(indices) == 0 {
				if prompt.Required {
					return terminalexperience.InteractionAnswer{}, errors.New("At least one author is required.")
				}
				return terminalexperience.InteractionAnswer{Values: []string{}}, nil
			}
			selected := make([]string, 0, len(indices))
			seen := make(map[int]struct{}, len(indices))
			for _, value := range indices {
				index, err := strconv.Atoi(value)
				if err != nil || index < 1 || index > len(options) {
					return terminalexperience.InteractionAnswer{}, errors.New("Invalid selection")
				}
				index--
				if _, duplicate := seen[index]; duplicate {
					continue
				}
				seen[index] = struct{}{}
				selected = append(selected, options[index].Value)
			}
			return terminalexperience.InteractionAnswer{Values: selected}, nil
		},
	}
	if len(prompt.InitialValues) > 0 {
		request.HasDefault = true
		request.Default = terminalexperience.InteractionAnswer{Values: append([]string(nil), prompt.InitialValues...)}
	}
	return request
}

func pulseAuthorInteractionOptions(choices []AuthorChoice) []terminalexperience.InteractionOption {
	labels := make([]string, len(choices))
	counts := make(map[string]int, len(choices))
	for index, choice := range choices {
		labels[index] = safePulseField(choice.Label, 160)
		counts[labels[index]]++
	}
	ordinals := make(map[string]int, len(counts))
	options := make([]terminalexperience.InteractionOption, 0, len(choices))
	for index, choice := range choices {
		label := labels[index]
		if counts[label] > 1 {
			ordinals[label]++
			label = fmt.Sprintf("%s (%d)", label, ordinals[label])
		}
		options = append(options, terminalexperience.InteractionOption{Label: label, Value: choice.Value})
	}
	return options
}

func pulseDocument(text string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func pulseReportText(report Report) string {
	var output strings.Builder
	output.WriteByte('\n')
	_, _ = fmt.Fprintf(&output, "Found %d %s in %d %s\n\n", report.CommitCount, pulsePlural(report.CommitCount, "commit", "commits"), len(report.Repositories), pulsePlural(len(report.Repositories), "repository", "repositories"))
	for groupIndex, repository := range report.Repositories {
		_, _ = fmt.Fprintf(&output, "%s (%d %s)\n", filepath.Base(repository.Path), len(repository.Commits), pulsePlural(len(repository.Commits), "commit", "commits"))
		_, _ = fmt.Fprintf(&output, "   %s%c\n", filepath.Dir(repository.Path), filepath.Separator)
		for commitIndex, commit := range repository.Commits {
			connector := "|-"
			if commitIndex == len(repository.Commits)-1 {
				connector = "`-"
			}
			_, _ = fmt.Fprintf(&output, "   %s %s | %s | %s\n", connector, commit.Date, commit.Author, commit.Subject)
		}
		if groupIndex < len(report.Repositories)-1 {
			_, _ = fmt.Fprintln(&output)
		}
	}
	return output.String()
}

type terminalPulsePhaseReporter struct {
	adapter *terminalPulseAdapter
	work    terminalexperience.WorkSession
	kind    PhaseKind

	mu     sync.Mutex
	closed bool
	err    error
}

func (reporter *terminalPulsePhaseReporter) Report(phase Phase) {
	reporter.mu.Lock()
	if reporter.closed {
		reporter.mu.Unlock()
		return
	}
	reporter.mu.Unlock()
	reporter.adapter.recordPhase(phase)
	if err := reporter.work.Update(terminalPulsePhase(phase)); err != nil {
		reporter.mu.Lock()
		reporter.err = errors.Join(reporter.err, err)
		reporter.mu.Unlock()
		if reporter.adapter.requestCancel != nil {
			reporter.adapter.requestCancel()
		}
	}
}

func (reporter *terminalPulsePhaseReporter) Close() error {
	reporter.mu.Lock()
	reporter.closed = true
	defer reporter.mu.Unlock()
	return reporter.err
}

func terminalPulsePhase(phase Phase) terminalexperience.OperationPhase {
	id, _ := terminalPulsePhaseDefinition(phase.Kind)
	return terminalexperience.OperationPhase{
		ID:     id,
		Detail: safePulseField(phase.Detail, 256),
		State:  terminalPulsePhaseState(phase.State),
	}
}

func terminalPulsePhaseDefinition(kind PhaseKind) (id, name string) {
	switch kind {
	case PhasePrepare:
		return pulsePreparePhaseID, pulsePreparePhaseName
	case PhaseFetch:
		return pulseFetchPhaseID, pulseFetchPhaseName
	case PhaseBuild:
		return pulseBuildPhaseID, pulseBuildPhaseName
	default:
		return pulseScanPhaseID, pulseScanPhaseName
	}
}

func terminalPulsePhaseState(state PhaseState) terminalexperience.PhaseState {
	switch state {
	case PhaseCompleted:
		return terminalexperience.PhaseCompleted
	case PhaseCancelled:
		return terminalexperience.PhaseCancelled
	case PhaseFailed:
		return terminalexperience.PhaseFailed
	default:
		return terminalexperience.PhaseActive
	}
}

func pulseRelativePath(root, repository string) string {
	relative, err := filepath.Rel(root, repository)
	if err != nil || relative == "" {
		return "."
	}
	return relative
}

func pulsePlural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
