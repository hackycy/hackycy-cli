package cm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestModuleRunGeneratesFromTheResolvedScopeAndSafeProfileProjection(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "src/value.go")
	runner := newModuleRunner(root, "?? src/value.go\x00")
	resolver := &recordingCMResolver{profile: commitMessageProfile()}
	transport := &recordingProviderTransport{response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"feat(cm): generate a message"}}],"usage":{"prompt_tokens":3}}`)),
	}}
	module := newModule(t, runner, resolver, transport)
	timeout := 1234.0

	result, err := module.Run(context.Background(), Input{Profile: "work", TimeoutMS: &timeout})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.RepositoryRoot != root || result.Scope != ScopeAllUncommitted || result.Cancelled || result.NoChanges || result.Generated == nil || result.Generated.Message != "feat(cm): generate a message" {
		t.Fatalf("result = %#v", result)
	}
	if result.Profile != (ProfileDiagnostic{Name: "work", BaseURL: "https://provider.test/v1", Model: "provider-model"}) {
		t.Fatalf("profile = %#v", result.Profile)
	}
	if resolver.options != (appconfig.CMResolveOptions{ProfileName: "work", TimeoutOverrideMS: &timeout}) {
		t.Fatalf("resolver options = %#v", resolver.options)
	}
	if transport.calls != 1 {
		t.Fatalf("provider calls = %d", transport.calls)
	}
}

func TestModuleRunAppliesStagingModesBeforeCapturingTheSnapshot(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	t.Run("selection", func(t *testing.T) {
		runner := newModuleRunner(root, "M  value.go\x00")
		module := newModuleWithPrompter(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{useInitialValues: true})

		result, err := module.Run(context.Background(), Input{Stage: true})
		if err != nil || result.Scope != ScopeStaged || result.Generated == nil {
			t.Fatalf("Run() = (%#v, %v)", result, err)
		}
		requireSnapshotCalls(t, runner,
			[]string{"rev-parse", "--show-toplevel"},
			[]string{"-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all"},
			[]string{"-C", root, "ls-files", "--stage", "-z"},
			[]string{"-C", root, "add", "-A", "--", "value.go"},
		)
		runner.requireNoCall(t, []string{"-C", root, "diff", "--no-ext-diff", "--find-renames", "--unified=0"})
	})
	t.Run("all", func(t *testing.T) {
		runner := newModuleRunner(root, "M  value.go\x00")
		module := newModule(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport())

		result, err := module.Run(context.Background(), Input{StageAll: true})
		if err != nil || result.Scope != ScopeStaged || result.Generated == nil {
			t.Fatalf("Run() = (%#v, %v)", result, err)
		}
		requireSnapshotCalls(t, runner,
			[]string{"rev-parse", "--show-toplevel"},
			[]string{"-C", root, "add", "-A"},
		)
		runner.requireNoCall(t, []string{"-C", root, "diff", "--no-ext-diff", "--find-renames", "--unified=0"})
	})
}

func TestModuleEmitsTypedStageAndGeneratePhaseLedger(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	tracker := &recordingCMTracker{}
	runner := newModuleRunner(root, "M  value.go\x00")
	module := newModuleWithTracker(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{useInitialValues: true}, &recordingCommitPrompter{}, tracker)

	result, err := module.Run(context.Background(), Input{Stage: true})
	if err != nil || result.Generated == nil {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	if tracker.closed != 1 {
		t.Fatalf("closed phase reporters = %d, want 1", tracker.closed)
	}
	if got, want := cmPhaseKinds(tracker.phases), []PhaseKind{PhaseStage, PhaseStage, PhaseCollect, PhaseCollect, PhaseGenerate, PhaseGenerate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("phase kinds = %#v, want %#v", got, want)
	}
	if tracker.phases[0].State != PhaseActive || tracker.phases[1].State != PhaseCompleted || tracker.phases[2].State != PhaseActive || tracker.phases[3].FileCount != 1 || tracker.phases[4].State != PhaseActive || tracker.phases[5].State != PhaseCompleted {
		t.Fatalf("phase ledger = %#v", tracker.phases)
	}
}

func TestModuleEmitsTypedCommitAndPushPhaseLedger(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	tracker := &recordingCMTracker{}
	runner := newModuleRunner(root, "M  value.go\x00")
	runner.responses[snapshotRunnerKey([]string{"-C", root, "branch", "--show-current"})] = GitOutput{Stdout: []byte("main\n")}
	module := newModuleWithTracker(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{}, &recordingCommitPrompter{confirmed: true}, tracker)

	result, err := module.Run(context.Background(), Input{Staged: true, Push: modeString("origin")})
	if err != nil || !result.Committed || !result.Pushed || result.PushRemote != "origin" {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	if tracker.closed != 2 || len(tracker.segments) != 2 {
		t.Fatalf("tracker = %#v", tracker)
	}
	if got, want := cmPhaseKinds(tracker.segments[0]), []PhaseKind{PhaseCollect, PhaseCollect, PhaseGenerate, PhaseGenerate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("generation phase kinds = %#v, want %#v", got, want)
	}
	if got, want := tracker.segments[1], []Phase{
		{Kind: PhaseCommit, State: PhaseActive},
		{Kind: PhaseCommit, State: PhaseCompleted},
		{Kind: PhasePush, State: PhaseActive, Remote: "origin"},
		{Kind: PhasePush, State: PhaseCompleted, Remote: "origin"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commit/push phase ledger = %#v, want %#v", got, want)
	}
}

func TestModuleFinalizesPushPhaseAndKeepsCommitFactWhenPushFails(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	tracker := &recordingCMTracker{}
	runner := newModuleRunner(root, "M  value.go\x00")
	runner.responses[snapshotRunnerKey([]string{"-C", root, "branch", "--show-current"})] = GitOutput{Stdout: []byte("main\n")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "push", "-u", "origin", "main"})] = GitOutput{Stderr: []byte("remote rejected\n"), ExitCode: 1}
	module := newModuleWithTracker(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{}, &recordingCommitPrompter{confirmed: true}, tracker)

	result, err := module.Run(context.Background(), Input{Staged: true, Push: modeString("origin")})
	if err == nil || err.Error() != "remote rejected" || !result.Committed || result.Pushed || result.PushRemote != "" {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	if tracker.closed != 2 || len(tracker.segments) != 2 {
		t.Fatalf("tracker = %#v", tracker)
	}
	if got, want := tracker.segments[1][len(tracker.segments[1])-1], (Phase{Kind: PhasePush, State: PhaseFailed, Remote: "origin"}); got != want {
		t.Fatalf("final phase = %#v, want %#v", got, want)
	}
}

func TestModuleFinalizesActiveCMPhaseWhenGenerationFails(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	tracker := &recordingCMTracker{}
	failure := errors.New("provider unavailable")
	module := newModuleWithTracker(t, newModuleRunner(root, "?? value.go\x00"), &recordingCMResolver{profile: commitMessageProfile()}, providerTransportFunc(func(*http.Request) (*http.Response, error) {
		return nil, failure
	}), &stagePrompter{}, &recordingCommitPrompter{}, tracker)

	if _, err := module.Run(context.Background(), Input{}); !errors.Is(err, failure) {
		t.Fatalf("Run() error = %v, want provider failure", err)
	}
	if tracker.closed != 1 || len(tracker.phases) == 0 || tracker.phases[len(tracker.phases)-1] != (Phase{Kind: PhaseGenerate, State: PhaseFailed, FileCount: 1}) {
		t.Fatalf("failed phase ledger = %#v, closed = %d", tracker.phases, tracker.closed)
	}
}

func TestModuleRunConfirmsThenRechecksAndCommits(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	t.Run("declined confirmation keeps the generated message and index state", func(t *testing.T) {
		runner := newModuleRunner(root, "M  value.go\x00")
		committer := &recordingCommitPrompter{}
		module := newModuleWithPrompts(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{}, committer)

		result, err := module.Run(context.Background(), Input{Staged: true})
		if err != nil || !result.Cancelled || result.Committed || result.Generated == nil {
			t.Fatalf("Run() = (%#v, %v)", result, err)
		}
		if committer.prompt.Message != "Create this commit?" || committer.prompt.Generated.Message != result.Generated.Message || committer.prompt.Profile != result.Profile {
			t.Fatalf("prompt = %#v, result = %#v", committer.prompt, result)
		}
		runner.requireNoCall(t, []string{"-C", root, "commit", "-m", result.Generated.Message})
	})
	t.Run("confirmed confirmation commits after a second snapshot", func(t *testing.T) {
		runner := newModuleRunner(root, "M  value.go\x00")
		committer := &recordingCommitPrompter{confirmed: true}
		module := newModuleWithPrompts(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{}, committer)

		result, err := module.Run(context.Background(), Input{Staged: true})
		if err != nil || result.Cancelled || !result.Committed || result.Generated == nil {
			t.Fatalf("Run() = (%#v, %v)", result, err)
		}
		requireSnapshotCalls(t, runner, []string{"-C", root, "commit", "-m", result.Generated.Message})
	})
}

func TestModulePropagatesInteractionFailuresBeforeMutation(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	t.Run("stage selection", func(t *testing.T) {
		runner := newModuleRunner(root, " M value.go\x00")
		promptErr := errors.New("terminal unavailable")
		module := newModuleWithPrompts(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{err: promptErr}, &recordingCommitPrompter{})

		result, err := module.Run(context.Background(), Input{Stage: true})
		if !errors.Is(err, promptErr) || result.RepositoryRoot != root || result.Generated != nil {
			t.Fatalf("Run() = (%#v, %v)", result, err)
		}
		runner.requireNoCall(t, []string{"-C", root, "add", "-A", "--", "value.go"})
	})
	t.Run("commit confirmation", func(t *testing.T) {
		runner := newModuleRunner(root, "M  value.go\x00")
		promptErr := errors.New("terminal unavailable")
		module := newModuleWithPrompts(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{}, &recordingCommitPrompter{err: promptErr})

		result, err := module.Run(context.Background(), Input{Staged: true})
		if !errors.Is(err, promptErr) || result.Generated == nil || !result.PromptedCommit || result.Committed {
			t.Fatalf("Run() = (%#v, %v)", result, err)
		}
		runner.requireNoCall(t, []string{"-C", root, "commit", "-m", result.Generated.Message})
	})
}

func TestModuleRunReportsTheCommittedPartialResultWhenPushFails(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	runner := newModuleRunner(root, "M  value.go\x00")
	runner.responses[snapshotRunnerKey([]string{"-C", root, "branch", "--show-current"})] = GitOutput{Stdout: []byte("main\n")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "push", "-u", "origin", "main"})] = GitOutput{Stderr: []byte("remote rejected\n"), ExitCode: 1}
	module := newModuleWithPrompts(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{}, &recordingCommitPrompter{confirmed: true})

	result, err := module.Run(context.Background(), Input{Staged: true, Push: modeString("origin")})
	if err == nil || err.Error() != "remote rejected" || !result.Committed || result.Pushed || result.PushRemote != "" || result.Generated == nil {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
}

func TestModuleRunMarksASuccessfulPush(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	runner := newModuleRunner(root, "M  value.go\x00")
	runner.responses[snapshotRunnerKey([]string{"-C", root, "branch", "--show-current"})] = GitOutput{Stdout: []byte("main\n")}
	module := newModuleWithPrompts(t, runner, &recordingCMResolver{profile: commitMessageProfile()}, successfulProviderTransport(), &stagePrompter{}, &recordingCommitPrompter{confirmed: true})

	result, err := module.Run(context.Background(), Input{Staged: true, Push: modeString("origin")})
	if err != nil || !result.Committed || !result.Pushed || result.PushRemote != "origin" {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
}

func TestModuleRunReturnsNormalNoChangeAndSelectionCancellationOutcomesBeforeProfileResolution(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		root := t.TempDir()
		resolver := &recordingCMResolver{profile: commitMessageProfile()}
		module := newModule(t, newModuleRunner(root, ""), resolver, successfulProviderTransport())

		result, err := module.Run(context.Background(), Input{})
		if err != nil || result != (Result{RepositoryRoot: root, Scope: ScopeAllUncommitted, NoChanges: true, NoChangeScope: ScopeAllUncommitted}) || resolver.calls != 0 {
			t.Fatalf("Run() = (%#v, %v), resolver calls = %d", result, err, resolver.calls)
		}
	})
	t.Run("selection cancellation", func(t *testing.T) {
		root := t.TempDir()
		resolver := &recordingCMResolver{profile: commitMessageProfile()}
		module := newModuleWithPrompter(t, newModuleRunner(root, " M value.go\x00"), resolver, successfulProviderTransport(), &stagePrompter{cancelled: true})

		result, err := module.Run(context.Background(), Input{Stage: true})
		if err != nil || result != (Result{RepositoryRoot: root, Scope: ScopeStaged, Cancelled: true}) || resolver.calls != 0 {
			t.Fatalf("Run() = (%#v, %v), resolver calls = %d", result, err, resolver.calls)
		}
	})
}

func TestModuleRunResolvesProfileBeforeRejectingLanguageAndDoesNotCallProvider(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	resolver := &recordingCMResolver{profile: commitMessageProfile()}
	transport := &recordingProviderTransport{}
	module := newModule(t, newModuleRunner(root, "?? value.go\x00"), resolver, transport)

	result, err := module.Run(context.Background(), Input{Language: "fr"})
	if err == nil || err.Error() != "Unsupported language. Use \"en\" or \"zh\"." || result != (Result{}) || resolver.calls != 1 || transport.calls != 0 {
		t.Fatalf("Run() = (%#v, %v), resolver=%d, provider=%d", result, err, resolver.calls, transport.calls)
	}
}

func TestModuleRunPropagatesResolverAndProviderFailuresWithoutLeakingTheAPIKey(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	t.Run("resolver", func(t *testing.T) {
		failure := errors.New("no usable profile")
		module := newModule(t, newModuleRunner(root, "?? value.go\x00"), &recordingCMResolver{err: failure}, successfulProviderTransport())
		_, err := module.Run(context.Background(), Input{})
		if !errors.Is(err, failure) {
			t.Fatalf("Run() error = %v", err)
		}
	})
	t.Run("provider", func(t *testing.T) {
		profile := commitMessageProfile()
		module := newModule(t, newModuleRunner(root, "?? value.go\x00"), &recordingCMResolver{profile: profile}, providerTransportFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("provider-api-key rejected")
		}))
		result, err := module.Run(context.Background(), Input{})
		if err == nil || strings.Contains(err.Error(), profile.APIKey) || !strings.Contains(err.Error(), "[REDACTED]") || result.Profile != profileDiagnostic(profile) {
			t.Fatalf("Run() = (%#v, %v)", result, err)
		}
	})
}

func TestNewModuleRequiresEveryCommandOwnedDependency(t *testing.T) {
	runner := newModuleRunner(t.TempDir(), "")
	resolver := &recordingCMResolver{profile: commitMessageProfile()}
	transport := successfulProviderTransport()
	for _, dependencies := range []Dependencies{
		{Files: diskSnapshotFileSystem{}, Prompter: &stagePrompter{}, Committer: &recordingCommitPrompter{}, Resolver: resolver, Transport: transport},
		{Git: runner, Prompter: &stagePrompter{}, Committer: &recordingCommitPrompter{}, Resolver: resolver, Transport: transport},
		{Git: runner, Files: diskSnapshotFileSystem{}, Committer: &recordingCommitPrompter{}, Resolver: resolver, Transport: transport},
		{Git: runner, Files: diskSnapshotFileSystem{}, Prompter: &stagePrompter{}, Resolver: resolver, Transport: transport},
		{Git: runner, Files: diskSnapshotFileSystem{}, Prompter: &stagePrompter{}, Committer: &recordingCommitPrompter{}, Transport: transport},
		{Git: runner, Files: diskSnapshotFileSystem{}, Prompter: &stagePrompter{}, Committer: &recordingCommitPrompter{}, Resolver: resolver},
	} {
		if _, err := New(dependencies); err == nil {
			t.Fatalf("New(%#v) error = nil", dependencies)
		}
	}
}

func newModule(t *testing.T, runner *snapshotRunner, resolver ProfileResolver, transport ProviderTransport) *Module {
	t.Helper()
	return newModuleWithPrompts(t, runner, resolver, transport, &stagePrompter{}, &recordingCommitPrompter{})
}

func newModuleWithPrompter(t *testing.T, runner *snapshotRunner, resolver ProfileResolver, transport ProviderTransport, prompter StagePrompter) *Module {
	return newModuleWithPrompts(t, runner, resolver, transport, prompter, &recordingCommitPrompter{})
}

func newModuleWithPrompts(t *testing.T, runner *snapshotRunner, resolver ProfileResolver, transport ProviderTransport, prompter StagePrompter, committer CommitPrompter) *Module {
	return newModuleWithTracker(t, runner, resolver, transport, prompter, committer, nil)
}

func newModuleWithTracker(t *testing.T, runner *snapshotRunner, resolver ProfileResolver, transport ProviderTransport, prompter StagePrompter, committer CommitPrompter, tracker Tracker) *Module {
	t.Helper()
	if tracker == nil {
		tracker = discardCMTracker{}
	}
	module, err := New(Dependencies{Git: runner, Files: diskSnapshotFileSystem{}, Prompter: prompter, Committer: committer, Resolver: resolver, Transport: transport, Tracker: tracker})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return module
}

type discardCMTracker struct{}

func (discardCMTracker) Start(context.Context) (PhaseReporter, error) {
	return discardCMPhaseReporter{}, nil
}

type discardCMPhaseReporter struct{}

func (discardCMPhaseReporter) Report(Phase) {}

func (discardCMPhaseReporter) Close() error { return nil }

type recordingCommitPrompter struct {
	prompt    CommitPrompt
	confirmed bool
	cancelled bool
	err       error
}

func (prompter *recordingCommitPrompter) ConfirmCommit(prompt CommitPrompt) (bool, bool, error) {
	prompter.prompt = prompt
	return prompter.confirmed, prompter.cancelled, prompter.err
}

type recordingCMTracker struct {
	phases   []Phase
	segments [][]Phase
	closed   int
}

func (tracker *recordingCMTracker) Start(context.Context) (PhaseReporter, error) {
	tracker.segments = append(tracker.segments, nil)
	return recordingCMPhaseReporter{tracker: tracker, segment: len(tracker.segments) - 1}, nil
}

type recordingCMPhaseReporter struct {
	tracker *recordingCMTracker
	segment int
}

func (reporter recordingCMPhaseReporter) Report(phase Phase) {
	reporter.tracker.phases = append(reporter.tracker.phases, phase)
	reporter.tracker.segments[reporter.segment] = append(reporter.tracker.segments[reporter.segment], phase)
}

func (reporter recordingCMPhaseReporter) Close() error {
	reporter.tracker.closed++
	return nil
}

func cmPhaseKinds(phases []Phase) []PhaseKind {
	kinds := make([]PhaseKind, 0, len(phases))
	for _, phase := range phases {
		kinds = append(kinds, phase.Kind)
	}
	return kinds
}

func newModuleRunner(root, status string) *snapshotRunner {
	runner := newSnapshotRunner(root, status)
	setEmptySnapshotDiffs(runner, root)
	return runner
}

func successfulProviderTransport() ProviderTransport {
	return providerTransportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"feat(cm): generate a message"}}]}`))}, nil
	})
}

func requireSnapshotCalls(t *testing.T, runner *snapshotRunner, expected ...[]string) {
	t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, want := range expected {
		found := false
		for _, actual := range runner.calls {
			if reflect.DeepEqual(actual, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Git calls = %#v, missing %#v", runner.calls, want)
		}
	}
}

type recordingCMResolver struct {
	profile appconfig.ResolvedCMProfile
	options appconfig.CMResolveOptions
	err     error
	calls   int
}

func (resolver *recordingCMResolver) ResolveCMProfile(options appconfig.CMResolveOptions) (appconfig.ResolvedCMProfile, error) {
	resolver.calls++
	resolver.options = options
	return resolver.profile, resolver.err
}
