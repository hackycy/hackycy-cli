package terminaltest

import "sync"

// OperationKind identifies the semantic operation recorded by a test run.
type OperationKind string

const (
	// AskOperation records an interaction request.
	AskOperation OperationKind = "ask"
	// NoticeOperation records transient command context.
	NoticeOperation OperationKind = "notice"
	// ResultOperation records a durable presentation request.
	ResultOperation OperationKind = "result"
	// ResultCheckpointOperation records an identified service result checkpoint.
	ResultCheckpointOperation OperationKind = "result-checkpoint"
	// MilestoneOperation records an explicit durable checkpoint.
	MilestoneOperation OperationKind = "milestone"
	// FinishOperation records a finite command outcome.
	FinishOperation OperationKind = "finish"
	// TrackOperation records a tracked-operation request.
	TrackOperation OperationKind = "track"
	// StartWorkOperation records one controlled Work Catalog declaration.
	StartWorkOperation OperationKind = "start-work"
	// WorkUpdateOperation records an update submitted to a controlled Work Catalog.
	WorkUpdateOperation OperationKind = "work-update"
	// WorkCloseOperation records controlled Work Catalog completion.
	WorkCloseOperation OperationKind = "work-close"
	// CloseOperation records terminal cleanup requested by a caller.
	CloseOperation OperationKind = "close"
)

// Operation captures one intent-level operation without imposing production
// terminal types before the Terminal Experience Module exists.
type Operation struct {
	Kind  OperationKind
	Value any
}

// Answer is a scripted response for a recorded interaction request.
type Answer struct {
	Value     any
	Cancelled bool
	Err       error
}

// RecordingRun records the semantic requests an adapter sends to an
// Experience Run. Its generic payloads keep the fixture test-only until the
// production terminal types are introduced.
type RecordingRun struct {
	mu         sync.Mutex
	answers    []Answer
	operations []Operation
}

// NewRecordingRun creates a recorder with scripted answers in request order.
func NewRecordingRun(answers ...Answer) *RecordingRun {
	return &RecordingRun{answers: append([]Answer(nil), answers...)}
}

// Ask records an interaction request and returns its next scripted answer.
func (run *RecordingRun) Ask(request any) Answer {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.operations = append(run.operations, Operation{Kind: AskOperation, Value: request})
	if len(run.answers) == 0 {
		return Answer{}
	}
	answer := run.answers[0]
	run.answers = run.answers[1:]
	return answer
}

// Notice records transient command context.
func (run *RecordingRun) Notice(document any) {
	run.record(NoticeOperation, document)
}

// Result records a durable presentation request.
func (run *RecordingRun) Result(document any) {
	run.record(ResultOperation, document)
}

// ResultCheckpoint records an identified service result checkpoint.
func (run *RecordingRun) ResultCheckpoint(id string, document any) {
	run.record(ResultCheckpointOperation, Checkpoint{ID: id, Document: document})
}

// Track records a tracked-operation request.
func (run *RecordingRun) Track(operation any) {
	run.record(TrackOperation, operation)
}

// Close records requested terminal cleanup.
func (run *RecordingRun) Close() {
	run.record(CloseOperation, nil)
}

// Operations returns a snapshot so assertions cannot mutate recorder state.
func (run *RecordingRun) Operations() []Operation {
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]Operation(nil), run.operations...)
}

func (run *RecordingRun) record(kind OperationKind, value any) {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.operations = append(run.operations, Operation{Kind: kind, Value: value})
}

// Checkpoint is a generic recorded service result checkpoint.
type Checkpoint struct {
	ID       string
	Document any
}
