package fork

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModuleUsesTheArchiveAfterDefaultBranchResolution(t *testing.T) {
	events := []string{}
	module := newTestModule(t, &testModuleDependencies{
		events:    &events,
		provider:  &fakeProvider{events: &events, defaultBranch: "main", archive: []byte("archive")},
		extractor: &fakeArchiveExtractor{events: &events},
	})

	result, err := module.Run(context.Background(), Input{Repository: "owner/project"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result, (Result{
		Repository:      Repository{Host: "github.com", Scheme: "https", Owner: "owner", Name: "project", ProviderType: providerGitHub},
		Destination:     "project",
		DestinationPath: testForkPath("project"),
		Ref:             "main",
		Acquisition:     acquisitionArchive,
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Run() result = %#v, want %#v", got, want)
	}
	if got, want := events, []string{"read:" + testForkPath("project"), "branch", "archive:main", "extract:" + testForkPath("project")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event order = %#v, want %#v", got, want)
	}
}

func TestModuleEmitsOneTypedAcquisitionPhaseLedger(t *testing.T) {
	tracker := &recordingForkTracker{}
	module := newTestModule(t, &testModuleDependencies{
		provider:  &fakeProvider{defaultBranch: "main", archive: []byte("archive")},
		extractor: &fakeArchiveExtractor{},
		tracker:   tracker,
	})

	if _, err := module.Run(context.Background(), Input{Repository: "owner/project"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if tracker.closed != 1 {
		t.Fatalf("closed phase reporters = %d, want 1", tracker.closed)
	}
	if got, want := forkPhaseKinds(tracker.phases), []PhaseKind{PhaseResolve, PhaseResolve, PhaseDefaultBranch, PhaseDefaultBranch, PhaseArchive, PhaseArchive, PhaseReady}; !reflect.DeepEqual(got, want) {
		t.Fatalf("phase kinds = %#v, want %#v", got, want)
	}
	if tracker.phases[0].State != PhaseActive || tracker.phases[1].State != PhaseCompleted || tracker.phases[3].Ref != "main" || tracker.phases[len(tracker.phases)-1].State != PhaseCompleted {
		t.Fatalf("phase ledger = %#v", tracker.phases)
	}
}

func forkPhaseKinds(phases []Phase) []PhaseKind {
	kinds := make([]PhaseKind, 0, len(phases))
	for _, phase := range phases {
		kinds = append(kinds, phase.Kind)
	}
	return kinds
}

func TestModulePresentsTheObservableAcquisitionMilestones(t *testing.T) {
	events := []string{}
	module := newTestModule(t, &testModuleDependencies{
		events:    &events,
		provider:  &fakeProvider{events: &events, defaultBranch: "main", archive: []byte("archive")},
		extractor: &fakeArchiveExtractor{events: &events},
		presenter: recordingForkPresenter{events: &events},
	})
	if _, err := module.Run(context.Background(), Input{Repository: "owner/project"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := events, []string{
		"intro", "read:" + testForkPath("project"), "branch", "archive:main", "extract:" + testForkPath("project"), "outcome:project",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("presentation and operation order = %#v, want %#v", got, want)
	}

	events = nil
	cancelled := newTestModule(t, &testModuleDependencies{
		events:      &events,
		directories: fakeDirectoryReader{events: &events, entries: []fs.DirEntry{testDirectoryEntry{}}},
		prompter:    &fakeOverwritePrompter{events: &events, cancelled: true},
		presenter:   recordingForkPresenter{events: &events},
	})
	result, err := cancelled.Run(context.Background(), Input{Repository: "owner/project"})
	if err != nil || !result.Cancelled {
		t.Fatalf("cancelled Run() = (%#v, %v)", result, err)
	}
	if got, want := events, []string{"intro", "read:" + testForkPath("project"), "prompt", "cancelled"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cancellation order = %#v, want %#v", got, want)
	}
}

func TestModuleFallsBackToCloneForArchiveAndDefaultBranchFailures(t *testing.T) {
	tests := []struct {
		name       string
		input      Input
		provider   *fakeProvider
		wantRef    string
		wantEvents []string
	}{
		{
			name:       "archive failure after explicit ref",
			input:      Input{Repository: "owner/project#release/topic", Destination: "chosen"},
			provider:   &fakeProvider{archiveErr: errors.New("archive unavailable")},
			wantRef:    "release/topic",
			wantEvents: []string{"read:" + testForkPath("chosen"), "archive:release/topic", "clone", "remove:" + testForkPath("chosen", ".git")},
		},
		{
			name:       "default branch failure uses remote default clone",
			input:      Input{Repository: "owner/project"},
			provider:   &fakeProvider{defaultErr: errors.New("default unavailable")},
			wantEvents: []string{"read:" + testForkPath("project"), "branch", "clone", "remove:" + testForkPath("project", ".git")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			test.provider.events = &events
			clone := &fakeCloneRunner{events: &events}
			module := newTestModule(t, &testModuleDependencies{events: &events, provider: test.provider, cloneRunner: clone})
			result, err := module.Run(context.Background(), test.input)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Acquisition != acquisitionClone || result.Ref != test.wantRef {
				t.Fatalf("Run() result = %#v", result)
			}
			if test.provider.defaultErr != nil && !errors.Is(result.DefaultBranchError, test.provider.defaultErr) {
				t.Fatalf("default branch error = %v", result.DefaultBranchError)
			}
			if test.provider.archiveErr != nil && !errors.Is(result.ArchiveError, test.provider.archiveErr) {
				t.Fatalf("archive error = %v", result.ArchiveError)
			}
			if got := events; !reflect.DeepEqual(got, test.wantEvents) {
				t.Fatalf("event order = %#v, want %#v", got, test.wantEvents)
			}
		})
	}
}

func TestModuleRetainsLegacyDestinationConfirmationAndReadErrorBehavior(t *testing.T) {
	tests := []struct {
		name            string
		input           Input
		entries         []fs.DirEntry
		readErr         error
		confirmed       bool
		cancelled       bool
		wantCancelled   bool
		wantEvents      []string
		wantRemovePaths []string
	}{
		{
			name:    "accepted nonempty destination is removed before provider work",
			input:   Input{Repository: "owner/project", Destination: "existing"},
			entries: []fs.DirEntry{testDirectoryEntry{}}, confirmed: true,
			wantEvents:      []string{"read:" + testForkPath("existing"), "prompt", "remove:" + testForkPath("existing"), "branch", "archive:main", "extract:" + testForkPath("existing")},
			wantRemovePaths: []string{testForkPath("existing")},
		},
		{
			name:          "declined replacement succeeds without mutation",
			input:         Input{Repository: "owner/project", Destination: "existing"},
			entries:       []fs.DirEntry{testDirectoryEntry{}},
			wantCancelled: true,
			wantEvents:    []string{"read:" + testForkPath("existing"), "prompt"},
		},
		{
			name:    "cancelled replacement succeeds without mutation",
			input:   Input{Repository: "owner/project", Destination: "existing"},
			entries: []fs.DirEntry{testDirectoryEntry{}}, cancelled: true,
			wantCancelled: true,
			wantEvents:    []string{"read:" + testForkPath("existing"), "prompt"},
		},
		{
			name:       "directory read errors act like a missing destination",
			input:      Input{Repository: "owner/project", Destination: "unreadable"},
			readErr:    errors.New("permission denied"),
			wantEvents: []string{"read:" + testForkPath("unreadable"), "branch", "archive:main", "extract:" + testForkPath("unreadable")},
		},
		{
			name:  "empty repository name defaults to the working directory",
			input: Input{Repository: "owner/"}, entries: []fs.DirEntry{testDirectoryEntry{}},
			wantCancelled: true,
			wantEvents:    []string{"read:" + testForkPath(), "prompt"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			remover := &fakeDirectoryRemover{events: &events}
			module := newTestModule(t, &testModuleDependencies{
				events:      &events,
				directories: fakeDirectoryReader{events: &events, entries: test.entries, err: test.readErr},
				prompter:    &fakeOverwritePrompter{events: &events, confirmed: test.confirmed, cancelled: test.cancelled},
				provider:    &fakeProvider{events: &events, defaultBranch: "main", archive: []byte("archive")},
				extractor:   &fakeArchiveExtractor{events: &events},
				remover:     remover,
			})
			result, err := module.Run(context.Background(), test.input)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Cancelled != test.wantCancelled {
				t.Fatalf("Run() cancelled = %t, want %t", result.Cancelled, test.wantCancelled)
			}
			if got := events; !reflect.DeepEqual(got, test.wantEvents) {
				t.Fatalf("event order = %#v, want %#v", got, test.wantEvents)
			}
			if got := remover.paths; !reflect.DeepEqual(got, test.wantRemovePaths) {
				t.Fatalf("removed paths = %#v, want %#v", got, test.wantRemovePaths)
			}
		})
	}
}

func TestModuleReturnsOverwriteInteractionFailureBeforeMutation(t *testing.T) {
	events := []string{}
	interactionFailure := errors.New("interactive terminal unavailable")
	remover := &fakeDirectoryRemover{events: &events}
	module := newTestModule(t, &testModuleDependencies{
		events:      &events,
		directories: fakeDirectoryReader{events: &events, entries: []fs.DirEntry{testDirectoryEntry{}}},
		prompter:    &fakeOverwritePrompter{events: &events, err: interactionFailure},
		remover:     remover,
	})

	if _, err := module.Run(context.Background(), Input{Repository: "owner/project", Destination: "existing"}); !errors.Is(err, interactionFailure) {
		t.Fatalf("Run() error = %v, want interaction failure", err)
	}
	if len(remover.paths) != 0 || !reflect.DeepEqual(events, []string{"read:" + testForkPath("existing"), "prompt"}) {
		t.Fatalf("interaction failure events = %#v, removed = %#v", events, remover.paths)
	}
}

func TestModuleReturnsCloneFailuresAndRequiresEveryBoundary(t *testing.T) {
	cloneFailure := errors.New("clone failed")
	module := newTestModule(t, &testModuleDependencies{
		provider:    &fakeProvider{archiveErr: errors.New("archive unavailable")},
		cloneRunner: &fakeCloneRunner{err: cloneFailure},
	})
	if _, err := module.Run(context.Background(), Input{Repository: "owner/project#main"}); !errors.Is(err, cloneFailure) {
		t.Fatalf("Run() error = %v, want clone failure", err)
	}

	valid := testModuleDependencies{}
	for _, test := range []struct {
		name  string
		apply func(*testModuleDependencies)
		want  string
	}{
		{name: "config", apply: func(dependencies *testModuleDependencies) { dependencies.config = nil }, want: "git fork config reader is required"},
		{name: "working directory", apply: func(dependencies *testModuleDependencies) { dependencies.workingDirectory = nil }, want: "git fork working directory is required"},
		{name: "directory reader", apply: func(dependencies *testModuleDependencies) { dependencies.directories = nil }, want: "git fork directory reader is required"},
		{name: "prompter", apply: func(dependencies *testModuleDependencies) { dependencies.prompter = nil }, want: "git fork overwrite prompter is required"},
		{name: "provider", apply: func(dependencies *testModuleDependencies) { dependencies.provider = nil }, want: "git fork provider is required"},
		{name: "extractor", apply: func(dependencies *testModuleDependencies) { dependencies.extractor = nil }, want: "git fork archive extractor is required"},
		{name: "clone runner", apply: func(dependencies *testModuleDependencies) { dependencies.cloneRunner = nil }, want: "git fork clone runner is required"},
		{name: "remover", apply: func(dependencies *testModuleDependencies) { dependencies.remover = nil }, want: "git fork directory remover is required"},
		{name: "presenter", apply: func(dependencies *testModuleDependencies) { dependencies.presenter = nil }, want: "git fork presenter is required"},
		{name: "tracker", apply: func(dependencies *testModuleDependencies) { dependencies.tracker = nil }, want: "git fork tracker is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dependencies := valid.withDefaults(nil)
			test.apply(&dependencies)
			if _, err := New(dependencies.dependencies()); err == nil || err.Error() != test.want {
				t.Fatalf("New() error = %v, want %q", err, test.want)
			}
		})
	}
}

type testModuleDependencies struct {
	events           *[]string
	config           ConfigReader
	workingDirectory func() (string, error)
	directories      DirectoryReader
	prompter         OverwritePrompter
	provider         Provider
	extractor        ArchiveExtractor
	cloneRunner      CloneRunner
	remover          DirectoryRemover
	presenter        Presenter
	tracker          Tracker
}

func newTestModule(t *testing.T, overrides *testModuleDependencies) *Module {
	t.Helper()
	dependencies := overrides.withDefaults(overrides.events)
	module, err := New(dependencies.dependencies())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return module
}

func (dependencies testModuleDependencies) withDefaults(events *[]string) testModuleDependencies {
	if dependencies.events == nil {
		dependencies.events = events
	}
	if dependencies.config == nil {
		dependencies.config = fakeConfigReader{}
	}
	if dependencies.workingDirectory == nil {
		dependencies.workingDirectory = func() (string, error) { return "/workspace", nil }
	}
	if dependencies.directories == nil {
		dependencies.directories = fakeDirectoryReader{events: dependencies.events}
	}
	if dependencies.prompter == nil {
		dependencies.prompter = &fakeOverwritePrompter{events: dependencies.events}
	}
	if dependencies.provider == nil {
		dependencies.provider = &fakeProvider{events: dependencies.events, defaultBranch: "main", archive: []byte("archive")}
	}
	if dependencies.extractor == nil {
		dependencies.extractor = &fakeArchiveExtractor{events: dependencies.events}
	}
	if dependencies.cloneRunner == nil {
		dependencies.cloneRunner = &fakeCloneRunner{events: dependencies.events}
	}
	if dependencies.remover == nil {
		dependencies.remover = &fakeDirectoryRemover{events: dependencies.events}
	}
	if dependencies.presenter == nil {
		dependencies.presenter = noopForkPresenter{}
	}
	if dependencies.tracker == nil {
		dependencies.tracker = noopForkTracker{}
	}
	return dependencies
}

func (dependencies testModuleDependencies) dependencies() Dependencies {
	return Dependencies{
		Config: dependencies.config, WorkingDirectory: dependencies.workingDirectory, Directories: dependencies.directories,
		Prompter: dependencies.prompter, Provider: dependencies.provider, Extractor: dependencies.extractor,
		CloneRunner: dependencies.cloneRunner, Remover: dependencies.remover, Presenter: dependencies.presenter, Tracker: dependencies.tracker,
	}
}

type fakeDirectoryReader struct {
	events  *[]string
	entries []fs.DirEntry
	err     error
}

func (reader fakeDirectoryReader) ReadDir(path string) ([]fs.DirEntry, error) {
	recordTestEvent(reader.events, "read:"+path)
	return reader.entries, reader.err
}

type fakeOverwritePrompter struct {
	events    *[]string
	confirmed bool
	cancelled bool
	err       error
}

func (prompter *fakeOverwritePrompter) ConfirmOverwrite(prompt OverwritePrompt) (bool, bool, error) {
	recordTestEvent(prompter.events, "prompt")
	return prompter.confirmed, prompter.cancelled, prompter.err
}

type fakeProvider struct {
	events        *[]string
	defaultBranch string
	defaultErr    error
	archive       []byte
	archiveErr    error
}

func (provider *fakeProvider) DefaultBranch(context.Context, Repository) (string, error) {
	recordTestEvent(provider.events, "branch")
	return provider.defaultBranch, provider.defaultErr
}

func (provider *fakeProvider) DownloadArchive(_ context.Context, _ Repository, ref string) ([]byte, error) {
	recordTestEvent(provider.events, "archive:"+ref)
	return provider.archive, provider.archiveErr
}

type fakeArchiveExtractor struct {
	events *[]string
	err    error
}

func (extractor *fakeArchiveExtractor) Extract(destination string, _ []byte) error {
	recordTestEvent(extractor.events, "extract:"+destination)
	return extractor.err
}

func recordTestEvent(events *[]string, event string) {
	if events != nil {
		*events = append(*events, event)
	}
}

func testForkPath(parts ...string) string {
	return filepath.Join(append([]string{"/workspace"}, parts...)...)
}

type testDirectoryEntry struct{}

func (testDirectoryEntry) Name() string               { return "entry" }
func (testDirectoryEntry) IsDir() bool                { return false }
func (testDirectoryEntry) Type() fs.FileMode          { return 0 }
func (testDirectoryEntry) Info() (fs.FileInfo, error) { return nil, nil }

type noopForkPresenter struct{}

func (noopForkPresenter) Introduction()  {}
func (noopForkPresenter) Cancelled()     {}
func (noopForkPresenter) Outcome(Result) {}

type noopForkTracker struct{}

func (noopForkTracker) Start(context.Context) (PhaseReporter, error) {
	return noopForkPhaseReporter{}, nil
}

type noopForkPhaseReporter struct{}

func (noopForkPhaseReporter) Report(Phase) {}

func (noopForkPhaseReporter) Close() error { return nil }

type recordingForkPresenter struct {
	events *[]string
}

type recordingForkTracker struct {
	phases []Phase
	closed int
}

func (tracker *recordingForkTracker) Start(context.Context) (PhaseReporter, error) {
	return recordingForkPhaseReporter{tracker: tracker}, nil
}

type recordingForkPhaseReporter struct {
	tracker *recordingForkTracker
}

func (reporter recordingForkPhaseReporter) Report(phase Phase) {
	reporter.tracker.phases = append(reporter.tracker.phases, phase)
}

func (reporter recordingForkPhaseReporter) Close() error {
	reporter.tracker.closed++
	return nil
}

func (presenter recordingForkPresenter) Introduction() {
	recordTestEvent(presenter.events, "intro")
}

func (presenter recordingForkPresenter) Cancelled() {
	recordTestEvent(presenter.events, "cancelled")
}

func (presenter recordingForkPresenter) Outcome(result Result) {
	recordTestEvent(presenter.events, "outcome:"+result.Destination)
}
