package terminal

import (
	"context"
	"io"
)

// InteractionKind identifies the answer shape a terminal interaction needs.
type InteractionKind uint8

const (
	InteractionText InteractionKind = iota
	InteractionSecret
	InteractionSelect
	InteractionMultiSelect
	InteractionConfirm
)

// InteractionOption is one semantic choice supplied by a command adapter.
type InteractionOption struct {
	Label       string
	Value       string
	Description string
}

// InteractionAnswer contains the value returned by one semantic interaction.
// The request kind determines which field is meaningful.
type InteractionAnswer struct {
	Value     string
	Values    []string
	Confirmed bool
}

// ConsoleMetadata is one command-owned, safe display field in a Rich Console.
// The terminal owns the bounded projection and never receives a Charm model.
type ConsoleMetadata struct {
	Label string
	Value string
}

// ConsoleFormStep is one command-owned entry in the complete pre-work form
// catalog. It is a safe projection only; the terminal owns its state symbol
// and interactive control rendering.
type ConsoleFormStep struct {
	ID        string
	Name      string
	Detail    string
	Sensitive bool
}

// ConsoleDescriptor supplies the semantic identity and safe context for one
// Rich Console run. Command adapters must provide bounded safe projections;
// terminal owns all rendering and terminal-mode behavior.
type ConsoleDescriptor struct {
	Command     string
	Target      string
	Status      string
	Metadata    []ConsoleMetadata
	FormCatalog []ConsoleFormStep
}

// FinishOutcome is the durable semantic outcome for one finite command run.
type FinishOutcome uint8

const (
	// Succeeded reports that the command completed its intended work.
	Succeeded FinishOutcome = iota + 1
	// Cancelled reports that the caller or user cancelled the command.
	Cancelled
	// Failed reports that the command finished with a known failure.
	Failed
)

func (outcome FinishOutcome) String() string {
	switch outcome {
	case Succeeded:
		return "succeeded"
	case Cancelled:
		return "cancelled"
	case Failed:
		return "failed"
	default:
		return "unknown"
	}
}

// FinishRequest is the bounded, command-owned semantic completion request.
// Summary is the safe outcome projection used by both the final Live View and
// the Interaction Transcript; it is never derived from a durable Result.
type FinishRequest struct {
	Outcome  FinishOutcome
	Location string
	Summary  PresentationDocument
}

// InteractionRequest describes command intent without choosing a prompt toolkit.
type InteractionRequest struct {
	Kind         InteractionKind
	Message      string
	Description  string
	Placeholder  string
	Options      []InteractionOption
	Default      InteractionAnswer
	HasDefault   bool
	CancelValues []string
	PlainLead    string
	PlainPrompt  string
	// ConsoleStepID identifies the corresponding entry in the complete Form
	// Catalog. An empty ID is retained for legacy adapters without a catalog.
	ConsoleStepID string
	// TranscriptLabel is the safe label used for the completed answer marker.
	TranscriptLabel string
	// TranscriptProject optionally maps a completed answer to a command-owned,
	// safe transcript value without changing the value returned to the command.
	TranscriptProject func(InteractionAnswer) string
	// Sensitive prevents the request value from entering a Live View or transcript.
	Sensitive bool
	// ParsePlain preserves a command-owned established Plain Interactive input grammar.
	// It is not used by Rich Interactive forms or Automation mode.
	ParsePlain func(string) (InteractionAnswer, error)
	Validate   func(InteractionAnswer) error
}

// VisualRole identifies the semantic meaning of presentation text.
type VisualRole uint8

const (
	VisualRolePlain VisualRole = iota
	VisualRoleTitle
	VisualRoleActive
	VisualRoleSuccess
	VisualRoleWarning
	VisualRoleError
	VisualRoleMuted
)

// PresentationBlock is one block of terminal presentation text.
type PresentationBlock struct {
	Role      VisualRole
	Text      string
	Sensitive bool
}

// PresentationDocument is presentation content assembled from semantic roles.
type PresentationDocument struct {
	Blocks []PresentationBlock
}

// PhaseDefinition is one immutable command-defined entry in a tracked phase catalog.
type PhaseDefinition struct {
	ID   string
	Name string
}

// Phase is a compatibility alias for callers that use the shorter catalog name.
type Phase = PhaseDefinition

// PhaseState describes one command-owned state in a tracked operation.
type PhaseState uint8

const (
	PhasePending PhaseState = iota
	PhaseActive
	PhaseCompleted
	PhaseCancelled
	PhaseFailed
)

func (state PhaseState) String() string {
	switch state {
	case PhasePending:
		return "pending"
	case PhaseActive:
		return "active"
	case PhaseCompleted:
		return "completed"
	case PhaseCancelled:
		return "cancelled"
	case PhaseFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// OperationPhase is one externally meaningful progress update from a command.
type OperationPhase struct {
	ID      string
	PhaseID string
	Name    string
	Detail  string
	State   PhaseState
}

// TrackedOperation supplies terminal presentation with command-owned updates.
// It never carries a business-work callback: command orchestration remains outside
// the terminal module.
type TrackedOperation struct {
	ID               string
	OperationID      string
	Label            string
	Phases           []PhaseDefinition
	PhaseDefinitions []PhaseDefinition
	Updates          <-chan OperationPhase
	RequestCancel    func()
}

// WorkCatalog declares one complete, immutable phase catalog for a controlled
// Work session. Callers publish updates through the returned WorkSession so a
// catalog can yield to a declared form and later resume without starting a
// second logical operation.
type WorkCatalog struct {
	ID            string
	Label         string
	Phases        []PhaseDefinition
	RequestCancel func()
}

// WorkSession accepts serialized updates for one previously declared Work
// Catalog. Close releases the session while retaining its terminal phases.
type WorkSession interface {
	Update(OperationPhase) error
	Close() error
}

// WorkSessionStarter is the opt-in shared lifecycle seam for commands that
// need one Work Catalog to alternate with declared forms.
type WorkSessionStarter interface {
	StartWork(WorkCatalog) (WorkSession, error)
}

// StartWork opens an opt-in controlled Work session without widening the
// stable ExperienceRun contract used by existing command adapters.
func StartWork(run ExperienceRun, catalog WorkCatalog) (WorkSession, error) {
	starter, ok := run.(WorkSessionStarter)
	if !ok {
		return nil, ErrWorkSessionUnavailable
	}
	return starter.StartWork(catalog)
}

// Experience opens independently closable terminal runs and owns diagnostics.
type Experience interface {
	Open(context.Context) ExperienceRun
	OpenConsole(context.Context, ConsoleDescriptor) (ExperienceRun, error)
	DiagnosticWriter() io.Writer
}

// ExperienceRun exposes only semantic terminal operations for one command context.
type ExperienceRun interface {
	Ask(InteractionRequest) (InteractionAnswer, error)
	Track(TrackedOperation) error
	Notice(PresentationDocument) error
	Milestone(PresentationDocument) error
	// Finish accepts a bounded FinishRequest and an optional durable Result.
	// The optional document remains a separate stdout channel; it is never
	// derived into the Live View or Interaction Transcript. A FinishOutcome
	// first argument retains source compatibility for existing adapters.
	Finish(any, ...*PresentationDocument) error
	// ResultCheckpoint writes one identified service-command result without
	// closing the run or entering the interaction transcript.
	ResultCheckpoint(string, PresentationDocument) error
	// Result remains for command adapters that have not yet migrated to Finish.
	Result(PresentationDocument) error
	Close() error
}
