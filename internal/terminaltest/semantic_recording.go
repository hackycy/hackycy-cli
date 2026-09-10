package terminaltest

import (
	"context"
	"io"
	"sync"

	"github.com/hackycy/hackycy-cli/internal/terminal"
)

// SemanticAnswer is a scripted response for a terminal InteractionRequest.
type SemanticAnswer struct {
	Value terminal.InteractionAnswer
	Err   error
}

// RecordingExperience records calls made through terminal's semantic seam.
type RecordingExperience struct {
	Run         *RecordingSemanticRun
	diagnostics io.Writer
}

// NewRecordingExperience creates a recording Experience with scripted answers.
func NewRecordingExperience(answers ...SemanticAnswer) *RecordingExperience {
	return &RecordingExperience{
		Run:         NewRecordingSemanticRun(answers...),
		diagnostics: io.Discard,
	}
}

// Open returns the recording run. The context is intentionally not interpreted.
func (experience *RecordingExperience) Open(_ context.Context) terminal.ExperienceRun {
	return experience.Run
}

// OpenConsole records no renderer implementation detail and returns the same
// semantic run after validating the command-owned Console descriptor.
func (experience *RecordingExperience) OpenConsole(_ context.Context, descriptor terminal.ConsoleDescriptor) (terminal.ExperienceRun, error) {
	if descriptor.Command == "" {
		return nil, terminal.ErrInvalidConsoleDescriptor
	}
	return experience.Run, nil
}

// DiagnosticWriter returns a discard sink for semantic tests.
func (experience *RecordingExperience) DiagnosticWriter() io.Writer {
	return experience.diagnostics
}

// RecordingSemanticRun records typed terminal operations.
type RecordingSemanticRun struct {
	mu         sync.Mutex
	answers    []SemanticAnswer
	operations []Operation
}

// Finish records a finite command outcome and its optional result document.
func (run *RecordingSemanticRun) Finish(value any, documents ...*terminal.PresentationDocument) error {
	finish := Finish{Value: value, Documents: append([]*terminal.PresentationDocument(nil), documents...)}
	if outcome, ok := value.(terminal.FinishOutcome); ok {
		finish.Outcome = outcome
		if len(documents) == 1 {
			finish.Document = documents[0]
		}
	}
	if request, ok := value.(terminal.FinishRequest); ok {
		finish.Request = request
	}
	run.record(FinishOperation, finish)
	return nil
}

// NewRecordingSemanticRun creates a typed semantic recorder.
func NewRecordingSemanticRun(answers ...SemanticAnswer) *RecordingSemanticRun {
	return &RecordingSemanticRun{answers: append([]SemanticAnswer(nil), answers...)}
}

// Ask records an interaction request and returns the next scripted response.
func (run *RecordingSemanticRun) Ask(request terminal.InteractionRequest) (terminal.InteractionAnswer, error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.operations = append(run.operations, Operation{Kind: AskOperation, Value: request})
	if len(run.answers) == 0 {
		return terminal.InteractionAnswer{}, nil
	}
	answer := run.answers[0]
	run.answers = run.answers[1:]
	return answer.Value, answer.Err
}

// Notice records transient command context.
func (run *RecordingSemanticRun) Notice(document terminal.PresentationDocument) error {
	run.record(NoticeOperation, document)
	return nil
}

// Milestone records an explicit durable checkpoint.
func (run *RecordingSemanticRun) Milestone(document terminal.PresentationDocument) error {
	run.record(MilestoneOperation, document)
	return nil
}

// Result records a durable presentation document.
func (run *RecordingSemanticRun) Result(document terminal.PresentationDocument) error {
	run.record(ResultOperation, document)
	return nil
}

// ResultCheckpoint records an identified service result checkpoint.
func (run *RecordingSemanticRun) ResultCheckpoint(id string, document terminal.PresentationDocument) error {
	run.record(ResultCheckpointOperation, Checkpoint{ID: id, Document: document})
	return nil
}

// Track records a tracked operation.
func (run *RecordingSemanticRun) Track(operation terminal.TrackedOperation) error {
	run.record(TrackOperation, operation)
	return nil
}

// StartWork records one controlled Work Catalog and returns a synchronous
// recorder for its updates.
func (run *RecordingSemanticRun) StartWork(catalog terminal.WorkCatalog) (terminal.WorkSession, error) {
	run.record(StartWorkOperation, catalog)
	return &recordingWorkSession{run: run}, nil
}

// Close records terminal cleanup.
func (run *RecordingSemanticRun) Close() error {
	run.record(CloseOperation, nil)
	return nil
}

// Operations returns a snapshot of typed semantic calls.
func (run *RecordingSemanticRun) Operations() []Operation {
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]Operation(nil), run.operations...)
}

func (run *RecordingSemanticRun) record(kind OperationKind, value any) {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.operations = append(run.operations, Operation{Kind: kind, Value: value})
}

type recordingWorkSession struct {
	run *RecordingSemanticRun
}

func (session *recordingWorkSession) Update(phase terminal.OperationPhase) error {
	session.run.record(WorkUpdateOperation, phase)
	return nil
}

func (session *recordingWorkSession) Close() error {
	session.run.record(WorkCloseOperation, nil)
	return nil
}

var _ terminal.WorkSessionStarter = (*RecordingSemanticRun)(nil)

// Finish is one recorded finite command completion request.
type Finish struct {
	Value     any
	Documents []*terminal.PresentationDocument
	Outcome   terminal.FinishOutcome
	Document  *terminal.PresentationDocument
	Request   terminal.FinishRequest
}
