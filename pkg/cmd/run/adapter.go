package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

var errRunRequiresInteractive = errors.New("run requires an interactive terminal")

func runRun(options *Options) error {
	if options == nil || options.Terminal == nil || options.WorkingDirectory == nil || options.Reader == nil || options.Exists == nil || options.Runner == nil {
		return errors.New("run options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, runConsoleDescriptor(options.Directory))
	if err != nil {
		return err
	}
	closed := false
	adapter := newTerminalRunAdapter(run)
	adapter.enableDetailed()
	closeRun := func() error {
		if closed {
			return nil
		}
		outcome := terminalexperience.Succeeded
		var document *terminalexperience.PresentationDocument
		if adapter.wasCancelled() {
			outcome = terminalexperience.Cancelled
			document = adapter.finalDocument(outcome)
		}
		return adapter.releaseTerminal(func() error {
			closed = true
			return run.Finish(adapter.finishRequest(outcome), document)
		})
	}
	defer run.Close()
	defer closeRun()

	module, err := New(Dependencies{
		WorkingDirectory: options.WorkingDirectory,
		Reader:           options.Reader,
		Exists:           options.Exists,
		Prompter:         adapter,
		Runner: releasedRunChildRunner{
			release: closeRun,
			runner:  options.Runner,
		},
		Presenter: adapter,
	})
	if err != nil {
		_ = adapter.finishDetailed()
		_ = run.Finish(adapter.finishRequest(terminalexperience.Failed), nil)
		closed = true
		return err
	}
	result, err := module.Run(ctx, Input{Directory: options.Directory})
	if err != nil {
		if adapter.wasReleased() {
			// The child boundary already handed ownership to the runner; a
			// start/exit error must retain its original contract and cannot
			// submit a second parent result.
			return err
		}
		_ = adapter.finishDetailed()
		outcome := terminalexperience.Failed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = terminalexperience.Cancelled
		}
		finishErr := run.Finish(adapter.finishRequest(outcome), adapter.finalDocument(outcome))
		closed = true
		return errors.Join(err, finishErr)
	}
	// A child runner is expected to release the parent before starting. A
	// custom runner may return without doing so; retain the old behavior while
	// ensuring no parent-owned result is emitted after handoff.
	if result.ExitCode != 0 {
		return &runChildOutcome{code: result.ExitCode}
	}
	return nil
}

type releasedRunChildRunner struct {
	release func() error
	runner  ChildRunner
}

func (runner releasedRunChildRunner) Run(ctx context.Context, request ChildRequest) (Result, error) {
	if err := runner.release(); err != nil {
		return Result{}, err
	}
	return runner.runner.Run(ctx, request)
}

type osRunFileReader struct{}

func (osRunFileReader) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func osRunPathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

type terminalRunAdapter struct {
	run terminalexperience.ExperienceRun

	mu              sync.Mutex
	detailed        bool
	work            terminalexperience.WorkSession
	workStart       chan struct{}
	workStarted     bool
	workClosed      bool
	workErr         error
	presentationErr error
	milestones      []terminalexperience.PresentationDocument
	cancelled       *terminalexperience.PresentationDocument
	released        bool
}

func newTerminalRunAdapter(run terminalexperience.ExperienceRun) *terminalRunAdapter {
	return &terminalRunAdapter{run: run}
}

func (adapter *terminalRunAdapter) enableDetailed() {
	adapter.mu.Lock()
	adapter.detailed = true
	adapter.mu.Unlock()
}

func (adapter *terminalRunAdapter) SelectScript(prompt ScriptPrompt) (string, bool, error) {
	answer, cancelled, err := adapter.ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionSelect,
		Message:         prompt.Message,
		PlainLead:       prompt.Message,
		PlainPrompt:     "> ",
		Options:         runScriptOptions(prompt.Options),
		CancelValues:    []string{"", "q", "quit", "cancel"},
		ConsoleStepID:   runScriptFormID,
		TranscriptLabel: "Script",
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string {
			return safeRunText(answer.Value, "selected script")
		},
		ParsePlain: func(value string) (terminalexperience.InteractionAnswer, error) {
			return parseRunSelection(value, runScriptOptions(prompt.Options))
		},
	})
	return answer.Value, cancelled, err
}

func (adapter *terminalRunAdapter) SelectPackageManager(prompt PackageManagerPrompt) (PackageManager, bool, error) {
	answer, cancelled, err := adapter.ask(terminalexperience.InteractionRequest{
		Kind:            terminalexperience.InteractionSelect,
		Message:         prompt.Message,
		PlainLead:       prompt.Message,
		PlainPrompt:     "> ",
		Options:         runPackageManagerOptions(prompt.Options),
		CancelValues:    []string{"", "q", "quit", "cancel"},
		ConsoleStepID:   runManagerFormID,
		TranscriptLabel: "Package manager",
		TranscriptProject: func(answer terminalexperience.InteractionAnswer) string {
			return safeRunText(answer.Value, "selected manager")
		},
		ParsePlain: func(value string) (terminalexperience.InteractionAnswer, error) {
			return parseRunSelection(value, runPackageManagerOptions(prompt.Options))
		},
	})
	return PackageManager(answer.Value), cancelled, err
}

func (adapter *terminalRunAdapter) Intro(message string) {
	adapter.mu.Lock()
	detailed := adapter.detailed
	adapter.mu.Unlock()
	if detailed {
		// Plain fallback retains the established heading. Rich has an explicit
		// descriptor, so this bounded notice is only transient context there.
		_ = adapter.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
			{Role: terminalexperience.VisualRoleTitle, Text: "HACKYCY CLI"},
			{Role: terminalexperience.VisualRoleActive, Text: safeRunText(message, "Run Script")},
		}})
		return
	}
	_ = adapter.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleTitle, Text: "HACKYCY CLI"},
		{Role: terminalexperience.VisualRoleActive, Text: message},
	}})
}

func (adapter *terminalRunAdapter) Info(message string) {
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleActive, Text: safeRunText(message, "child command")}}})
		adapter.mu.Unlock()
		return
	}
	adapter.mu.Unlock()
	_ = adapter.run.Notice(terminalRunDocument(message, terminalexperience.VisualRoleActive))
}

func (adapter *terminalRunAdapter) Blank() {
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.mu.Unlock()
		return
	}
	adapter.mu.Unlock()
	_ = adapter.run.Notice(terminalRunDocument("\n", terminalexperience.VisualRolePlain))
}

func (adapter *terminalRunAdapter) Cancel(message string) {
	adapter.mu.Lock()
	if adapter.detailed {
		document := terminalRunDocument(message, terminalexperience.VisualRoleWarning)
		adapter.cancelled = &document
		adapter.mu.Unlock()
		return
	}
	adapter.mu.Unlock()
	_ = adapter.run.Result(terminalRunDocument(message, terminalexperience.VisualRoleWarning))
}

func (adapter *terminalRunAdapter) reportRunPhase(id string, state terminalexperience.PhaseState, detail string) {
	adapter.mu.Lock()
	detailed := adapter.detailed
	adapter.mu.Unlock()
	if !detailed {
		return
	}
	work, err := adapter.ensureWork()
	if err != nil {
		adapter.recordPresentationError(err)
		return
	}
	if work == nil {
		return
	}
	err = work.Update(terminalexperience.OperationPhase{
		ID:     id,
		State:  state,
		Detail: safeRunText(detail, "working"),
	})
	adapter.recordPresentationError(err)
}

func (adapter *terminalRunAdapter) reportRunMilestone(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	adapter.mu.Lock()
	if adapter.detailed {
		adapter.milestones = append(adapter.milestones, terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleWarning, Text: safeRunText(text, "Run update")}}})
	}
	adapter.mu.Unlock()
}

func (adapter *terminalRunAdapter) finishDetailed() error {
	workErr := adapter.closeWork()
	adapter.mu.Lock()
	milestones := append([]terminalexperience.PresentationDocument(nil), adapter.milestones...)
	presentationErr := adapter.presentationErr
	adapter.milestones = nil
	adapter.mu.Unlock()
	err := errors.Join(workErr, presentationErr)
	for _, milestone := range milestones {
		err = errors.Join(err, adapter.run.Milestone(milestone))
	}
	return err
}

func (adapter *terminalRunAdapter) ensureWork() (terminalexperience.WorkSession, error) {
	adapter.mu.Lock()
	if adapter.workStarted {
		ready := adapter.workStart
		work := adapter.work
		err := adapter.workErr
		adapter.mu.Unlock()
		if ready != nil && work == nil && err == nil {
			<-ready
			adapter.mu.Lock()
			work, err = adapter.work, adapter.workErr
			adapter.mu.Unlock()
		}
		return work, err
	}
	adapter.workStarted = true
	adapter.workStart = make(chan struct{})
	ready := adapter.workStart
	adapter.mu.Unlock()

	work, err := terminalexperience.StartWork(adapter.run, runWorkCatalog())
	adapter.mu.Lock()
	adapter.work = work
	adapter.workErr = err
	close(ready)
	adapter.mu.Unlock()
	return work, err
}

func (adapter *terminalRunAdapter) closeWork() error {
	adapter.mu.Lock()
	if !adapter.workStarted {
		adapter.mu.Unlock()
		return nil
	}
	ready := adapter.workStart
	adapter.mu.Unlock()
	if ready != nil {
		<-ready
	}
	adapter.mu.Lock()
	if adapter.workClosed {
		err := adapter.workErr
		adapter.mu.Unlock()
		return err
	}
	adapter.workClosed = true
	work := adapter.work
	err := adapter.workErr
	adapter.mu.Unlock()
	if work != nil {
		err = errors.Join(err, work.Close())
	}
	adapter.mu.Lock()
	adapter.workErr = err
	adapter.mu.Unlock()
	return err
}

func (adapter *terminalRunAdapter) recordPresentationError(err error) {
	if err == nil {
		return
	}
	adapter.mu.Lock()
	adapter.presentationErr = errors.Join(adapter.presentationErr, err)
	adapter.mu.Unlock()
}

func (adapter *terminalRunAdapter) finalDocument(outcome terminalexperience.FinishOutcome) *terminalexperience.PresentationDocument {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if outcome == terminalexperience.Cancelled && adapter.cancelled != nil {
		document := *adapter.cancelled
		return &document
	}
	return nil
}

func (adapter *terminalRunAdapter) finishRequest(outcome terminalexperience.FinishOutcome) terminalexperience.FinishRequest {
	summary := "Run handoff complete."
	if outcome == terminalexperience.Cancelled {
		summary = "Run selection cancelled."
	} else if outcome == terminalexperience.Failed {
		summary = "Unable to prepare run handoff."
	}
	return terminalexperience.FinishRequest{
		Outcome: outcome,
		Summary: terminalRunDocument(summary, terminalexperience.VisualRoleMuted),
	}
}

func (adapter *terminalRunAdapter) releaseTerminal(finish func() error) error {
	adapter.mu.Lock()
	if adapter.released {
		adapter.mu.Unlock()
		return nil
	}
	adapter.released = true
	adapter.mu.Unlock()
	adapter.reportRunPhase(runReleaseTerminalPhaseID, terminalexperience.PhaseActive, "Restoring primary screen and terminal modes")
	adapter.reportRunPhase(runReleaseTerminalPhaseID, terminalexperience.PhaseCompleted, "Terminal released")
	if err := adapter.finishDetailed(); err != nil {
		return err
	}
	return finish()
}

func (adapter *terminalRunAdapter) wasReleased() bool {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.released
}

func (adapter *terminalRunAdapter) wasCancelled() bool {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.cancelled != nil
}

func (adapter *terminalRunAdapter) ask(request terminalexperience.InteractionRequest) (terminalexperience.InteractionAnswer, bool, error) {
	answer, err := adapter.run.Ask(request)
	if errors.Is(err, terminalexperience.ErrInteractionCancelled) || errors.Is(err, context.Canceled) {
		return terminalexperience.InteractionAnswer{}, true, nil
	}
	if errors.Is(err, terminalexperience.ErrAutomationInteraction) {
		return terminalexperience.InteractionAnswer{}, false, errRunRequiresInteractive
	}
	if err != nil {
		return terminalexperience.InteractionAnswer{}, false, err
	}
	return answer, false, nil
}

func terminalRunDocument(text string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func runScriptOptions(options []ScriptChoice) []terminalexperience.InteractionOption {
	result := make([]terminalexperience.InteractionOption, 0, len(options))
	for _, option := range options {
		result = append(result, terminalexperience.InteractionOption{
			Label:       option.Label,
			Value:       option.Value,
			Description: option.Hint,
		})
	}
	return result
}

func runPackageManagerOptions(options []PackageManagerChoice) []terminalexperience.InteractionOption {
	result := make([]terminalexperience.InteractionOption, 0, len(options))
	for _, option := range options {
		result = append(result, terminalexperience.InteractionOption{Label: option.Label, Value: string(option.Value)})
	}
	return result
}

func parseRunSelection(value string, options []terminalexperience.InteractionOption) (terminalexperience.InteractionAnswer, error) {
	index, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || index < 1 || index > len(options) {
		return terminalexperience.InteractionAnswer{}, errors.New("Invalid selection")
	}
	return terminalexperience.InteractionAnswer{Value: options[index-1].Value}, nil
}

func runConsoleDescriptor(directory string) terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / run",
		Target:  "Select and hand off a package script",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{{
			Label: "project",
			Value: runProjectLabel(directory),
		}},
		FormCatalog: []terminalexperience.ConsoleFormStep{
			{ID: runScriptFormID, Name: "Select script", Detail: "choose a package script"},
			{ID: runManagerFormID, Name: "Select package manager", Detail: "choose a package manager"},
		},
	}
}

func runProjectDetail(discovery Discovery) string {
	return fmt.Sprintf("%s; %d runnable scripts", runProjectLabel(discovery.Directory), len(discovery.Scripts))
}

func runProjectLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "current project"
	}
	base := filepath.Base(filepath.Clean(value))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "current project"
	}
	return safeRunText(base, "current project")
}

func runManagerDetail(managers []PackageManager) string {
	if len(managers) == 0 {
		return "no package managers"
	}
	return "order: " + safeRunText(strings.Join(packageManagerLabels(managers), ", "), "package managers")
}

func packageManagerLabels(managers []PackageManager) []string {
	labels := make([]string, 0, len(managers))
	for _, manager := range managers {
		labels = append(labels, safeRunText(string(manager), "manager"))
	}
	return labels
}

func runChildDetail(request ChildRequest) string {
	if request.Executable == "" {
		return "child command unavailable"
	}
	parts := []string{safeRunText(request.Executable, "manager")}
	for _, argument := range request.Arguments {
		parts = append(parts, safeRunText(argument, "argument"))
	}
	return strings.Join(parts, " ")
}

func safeRunText(value, fallback string) string {
	if !utf8.ValidString(value) {
		return fallback
	}
	var builder strings.Builder
	for _, r := range value {
		switch r {
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if unicode.IsControl(r) {
				return fallback
			}
			builder.WriteRune(r)
		}
	}
	value = strings.TrimSpace(builder.String())
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > 160 {
		return string(runes[:160]) + "..."
	}
	return value
}
