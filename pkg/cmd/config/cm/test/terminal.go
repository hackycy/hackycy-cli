package test

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

const (
	cmTestResolvePhaseID    = "resolve-cm-test-profile"
	cmTestResolvePhaseName  = "Resolve CM test profile"
	cmTestProviderPhaseID   = "test-cm-provider"
	cmTestProviderPhaseName = "Test CM provider"
)

func runTest(options *Options) error {
	if options == nil || options.Store == nil || options.HTTP == nil || options.Terminal == nil {
		return errors.New("config cm test options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	run, err := options.Terminal.OpenConsole(ctx, terminalCMTestConsoleDescriptor(options.Profile))
	if err != nil {
		return err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	phases := newCMTestPhaseSink(run, cancel)
	var presentationErr error
	finish := func(outcome terminalexperience.FinishOutcome, summary terminalexperience.PresentationDocument, document *terminalexperience.PresentationDocument, workErr error) error {
		presentationErr = errors.Join(presentationErr, phases.close())
		return errors.Join(workErr, presentationErr, run.Finish(terminalCMTestFinishRequest(outcome, summary), document))
	}

	if caps.Interaction == terminalexperience.RichInteractive {
		if err := run.Milestone(terminalCMTestIntroDocument()); err != nil {
			presentationErr = errors.Join(presentationErr, err)
		}
	}

	phases.begin(cmTestResolvePhaseID, cmTestResolvePhaseName, "Resolving CM test profile...")
	if err := ctx.Err(); err != nil {
		presentationErr = errors.Join(presentationErr, phases.end(terminalexperience.PhaseCancelled, "Cancelled while resolving CM test profile"))
		return finish(terminalexperience.Cancelled, terminalCMTestCancellationSummary("Cancelled while resolving CM test profile"), nil, err)
	}
	type resolutionResult struct {
		module  *TestModule
		profile appconfig.ResolvedCMProfile
		err     error
	}
	resolution := make(chan resolutionResult, 1)
	go func() {
		resolver, err := options.Store()
		if err != nil {
			resolution <- resolutionResult{err: err}
			return
		}
		module, err := NewTest(TestDependencies{Resolver: resolver, Transport: options.HTTP})
		if err != nil {
			resolution <- resolutionResult{err: err}
			return
		}
		profile, err := module.resolveProfile(TestRequest{Profile: options.Profile})
		resolution <- resolutionResult{module: module, profile: profile, err: err}
	}()
	var resolved resolutionResult
	select {
	case resolved = <-resolution:
	case <-ctx.Done():
		// Prefer a result that was already published over a simultaneous
		// cancellation so the phase state reflects completed resolution.
		select {
		case resolved = <-resolution:
		default:
			presentationErr = errors.Join(presentationErr, phases.end(terminalexperience.PhaseCancelled, "Cancelled while resolving CM test profile"))
			return finish(terminalexperience.Cancelled, terminalCMTestCancellationSummary("Cancelled while resolving CM test profile"), nil, ctx.Err())
		}
	}
	err = resolved.err
	module := resolved.module
	profile := resolved.profile
	if err != nil {
		state := terminalexperience.PhaseFailed
		outcome := terminalexperience.Failed
		detail := "Unable to resolve CM test profile (" + cmTestResolverFailureKind(err) + ")"
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			state = terminalexperience.PhaseCancelled
			outcome = terminalexperience.Cancelled
			detail = "Cancelled while resolving CM test profile"
		}
		presentationErr = errors.Join(presentationErr, phases.end(state, detail))
		if outcome == terminalexperience.Cancelled {
			return finish(outcome, terminalCMTestCancellationSummary(detail), nil, err)
		}
		return finish(outcome, terminalCMTestFailureSummary(detail), nil, err)
	}
	presentationErr = errors.Join(presentationErr, phases.end(terminalexperience.PhaseCompleted, "Profile: "+safeCMTestProfile(redactCMTestText(profile.Name, profile.APIKey))))

	providerDetail := fmt.Sprintf(
		"Provider: %s; Base URL: %s; Model: %s; waiting for response",
		safeCMTestProfile(redactCMTestText(profile.Name, profile.APIKey)),
		safeCMTestURL(redactCMTestText(profile.BaseURL, profile.APIKey)),
		safeCMTestModel(redactCMTestText(profile.Model, profile.APIKey)),
	)
	phases.begin(cmTestProviderPhaseID, cmTestProviderPhaseName, providerDetail)
	if err := ctx.Err(); err != nil {
		presentationErr = errors.Join(presentationErr, phases.end(terminalexperience.PhaseCancelled, "Cancelled while testing CM provider"))
		return finish(terminalexperience.Cancelled, terminalCMTestCancellationSummary("Cancelled while testing CM provider"), nil, err)
	}
	result, runErr := module.testProvider(ctx, profile)
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
			presentationErr = errors.Join(presentationErr, phases.end(terminalexperience.PhaseCancelled, "Cancelled while testing CM provider"))
			return finish(terminalexperience.Cancelled, terminalCMTestCancellationSummary("Cancelled while testing CM provider"), nil, runErr)
		}
		category := cmTestProviderFailureKind(runErr)
		failureSummary := "Provider request failed (" + string(category) + ")"
		presentationErr = errors.Join(presentationErr, phases.end(terminalexperience.PhaseFailed, failureSummary))
		if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
			document := terminalCMTestRichFailureDocument(result, string(category))
			return finish(terminalexperience.Failed, terminalCMTestFailureSummary(failureSummary), &document, runErr)
		}
		document := terminalCMTestDocument(result)
		return finish(terminalexperience.Failed, terminalCMTestFailureSummary(failureSummary), &document, runErr)
	}
	presentationErr = errors.Join(presentationErr, phases.end(terminalexperience.PhaseCompleted, "Response received"))
	var document terminalexperience.PresentationDocument
	if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
		document = terminalCMTestRichDocument(result)
	} else {
		document = terminalCMTestDocument(result)
	}
	return finish(terminalexperience.Succeeded, terminalCMTestResponseSummaryDocument(result), &document, nil)
}

func terminalCMTestConsoleDescriptor(profile string) terminalexperience.ConsoleDescriptor {
	metadata := []terminalexperience.ConsoleMetadata{{
		Label: "scope",
		Value: "non-mutating provider check",
	}}
	if strings.TrimSpace(profile) != "" {
		metadata = append(metadata, terminalexperience.ConsoleMetadata{
			Label: "profile",
			Value: safeCMTestProfile(profile),
		})
	}
	return terminalexperience.ConsoleDescriptor{
		Command:  "YCY / config cm test",
		Target:   "provider connection",
		Status:   "READY",
		Metadata: metadata,
	}
}

type cmTestPhaseSink struct {
	run           terminalexperience.ExperienceRun
	requestCancel context.CancelFunc
	updates       chan terminalexperience.OperationPhase
	done          chan error
	currentID     string
	started       bool
	closed        bool
	closeErr      error
}

func newCMTestPhaseSink(run terminalexperience.ExperienceRun, requestCancel context.CancelFunc) *cmTestPhaseSink {
	return &cmTestPhaseSink{
		run:           run,
		requestCancel: requestCancel,
		updates:       make(chan terminalexperience.OperationPhase, 8),
		done:          make(chan error, 1),
	}
}

func (sink *cmTestPhaseSink) begin(id, _ string, detail string) {
	if sink.closed {
		return
	}
	sink.start()
	sink.currentID = id
	sink.updates <- terminalexperience.OperationPhase{ID: id, State: terminalexperience.PhaseActive, Detail: detail}
}

func (sink *cmTestPhaseSink) end(state terminalexperience.PhaseState, detail string) error {
	if sink.closed || sink.currentID == "" {
		return nil
	}
	sink.updates <- terminalexperience.OperationPhase{ID: sink.currentID, State: state, Detail: detail}
	sink.currentID = ""
	return nil
}

func (sink *cmTestPhaseSink) start() {
	if sink.started {
		return
	}
	sink.started = true
	go func() {
		sink.done <- sink.run.Track(terminalexperience.TrackedOperation{
			ID:    "config-cm-test",
			Label: "Test commit message provider",
			Phases: []terminalexperience.PhaseDefinition{
				{ID: cmTestResolvePhaseID, Name: cmTestResolvePhaseName},
				{ID: cmTestProviderPhaseID, Name: cmTestProviderPhaseName},
			},
			Updates:       sink.updates,
			RequestCancel: sink.requestCancel,
		})
	}()
}

func (sink *cmTestPhaseSink) close() error {
	if sink.closed {
		return sink.closeErr
	}
	sink.closed = true
	if !sink.started {
		return nil
	}
	close(sink.updates)
	sink.closeErr = <-sink.done
	return sink.closeErr
}

func terminalCMTestFinishRequest(outcome terminalexperience.FinishOutcome, summary terminalexperience.PresentationDocument) terminalexperience.FinishRequest {
	return terminalexperience.FinishRequest{Outcome: outcome, Summary: summary}
}

func terminalCMTestCancellationSummary(text string) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleWarning,
		Text: text,
	}}}
}

func terminalCMTestFailureSummary(text string) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleError,
		Text: text,
	}}}
}

func terminalCMTestIntroDocument() terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / config cm test"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Test commit message provider"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Verify the resolved profile can answer a connection check"},
	}}
}

func terminalCMTestDocument(result TestResult) terminalexperience.PresentationDocument {
	if result.Diagnostic != nil {
		document := terminalCMTestFailureDocument(*result.Diagnostic, terminalexperience.VisualRoleMuted)
		document.Blocks = append([]terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleTitle, Text: "Commit message provider test"}, {Role: terminalexperience.VisualRoleWarning, Text: "Provider request failed"}}, document.Blocks...)
		return document
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleTitle, Text: "Commit message provider test"}, {Role: terminalexperience.VisualRolePlain, Text: "Response:\n" + result.Content}, {Role: terminalexperience.VisualRoleSuccess, Text: "Done"}}}
}

func terminalCMTestRichDocument(result TestResult) terminalexperience.PresentationDocument {
	intro := terminalCMTestIntroDocument().Blocks
	intro = append(intro, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRolePlain, Text: "Response:\n" + safeCMTestResponse(result.Content)})
	if usage := terminalCMTestUsageSummary(result.usage); usage != "" {
		intro = append(intro, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: usage})
	}
	intro = append(intro, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleSuccess, Text: "Done"})
	return terminalexperience.PresentationDocument{Blocks: intro}
}

func terminalCMTestResponseSummaryDocument(result TestResult) terminalexperience.PresentationDocument {
	blocks := []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleSuccess, Text: "Response received"}}
	if usage := terminalCMTestUsageSummary(result.usage); usage != "" {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: usage})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalCMTestUsageSummary(usage *cmTestTokenUsage) string {
	if usage == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	promptTokens, hasPromptTokens := cmTestUsageValue(usage.PromptTokens)
	completionTokens, hasCompletionTokens := cmTestUsageValue(usage.CompletionTokens)
	totalTokens, hasTotalTokens := cmTestUsageValue(usage.TotalTokens)
	if !hasTotalTokens && hasPromptTokens && hasCompletionTokens {
		totalTokens = promptTokens + completionTokens
		hasTotalTokens = true
	}
	if hasPromptTokens {
		parts = append(parts, fmt.Sprintf("Prompt tokens: %g", promptTokens))
	}
	if hasCompletionTokens {
		parts = append(parts, fmt.Sprintf("Completion tokens: %g", completionTokens))
	}
	if hasTotalTokens {
		parts = append(parts, fmt.Sprintf("Total tokens: %g", totalTokens))
	}
	return strings.Join(parts, "  ")
}

func cmTestUsageValue(value *float64) (float64, bool) {
	if value == nil {
		return 0, false
	}
	return cmTestUsageNumber(*value)
}

func terminalCMTestRichFailureDocument(result TestResult, category string) terminalexperience.PresentationDocument {
	blocks := terminalCMTestIntroDocument().Blocks
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: "Provider request failed"})
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: "Category: " + safeCMTestField(category, "provider")})
	if result.Diagnostic != nil {
		blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: fmt.Sprintf("Provider: %s\nBase URL: %s\nModel: %s", safeCMTestProfile(result.Diagnostic.Provider), safeCMTestURL(result.Diagnostic.BaseURL), safeCMTestModel(result.Diagnostic.Model))})
	}
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalCMTestFailureDocument(diagnostic TestDiagnostic, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: fmt.Sprintf("Provider: %s\nBase URL: %s\nModel: %s", safeCMTestProfile(diagnostic.Provider), safeCMTestURL(diagnostic.BaseURL), safeCMTestModel(diagnostic.Model))}}}
}
