package terminal

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const (
	defaultTranscriptEvents    = 64
	defaultTranscriptBytes     = 16 * 1024
	defaultTranscriptFieldSize = 2 * 1024
	transcriptTruncatedText    = "... transcript truncated ..."
)

// TranscriptEventKind identifies the semantic item retained in an Interaction Transcript.
type TranscriptEventKind string

const (
	TranscriptAsk       TranscriptEventKind = "ask"
	TranscriptMilestone TranscriptEventKind = "milestone"
	TranscriptPhase     TranscriptEventKind = "phase"
	TranscriptOutcome   TranscriptEventKind = "outcome"
	TranscriptTruncated TranscriptEventKind = "truncated"
)

// TranscriptEvent is one safe, durable semantic item in a run's transcript.
type TranscriptEvent struct {
	Sequence uint64
	Kind     TranscriptEventKind
	Label    string
	Text     string
	PhaseID  string
	State    PhaseState
	Outcome  FinishOutcome
	// Location and Summary are the bounded completion projection. They are
	// retained separately from Text so the renderer can place them in the
	// optional AT and OUTCOME sections without consulting stdout.
	Location string
	Summary  string
}

// TranscriptOptions bounds one Interaction Transcript ledger.
type TranscriptOptions struct {
	MaxEvents    int
	MaxBytes     int
	MaxFieldSize int
}

// TranscriptLedger is an append-only, bounded semantic transcript.
type TranscriptLedger struct {
	maxEvents    int
	maxBytes     int
	maxFieldSize int
	events       []TranscriptEvent
	bytes        int
	nextSequence uint64
	truncated    bool
	outcomeSeen  bool
	frozen       bool
}

// NewTranscriptLedger creates a ledger with the approved defaults or overrides.
func NewTranscriptLedger(options TranscriptOptions) *TranscriptLedger {
	if options.MaxEvents <= 0 {
		options.MaxEvents = defaultTranscriptEvents
	}
	// A bounded ledger must be able to retain both its truncation marker and a
	// final outcome event. Two slots is the smallest meaningful configuration.
	if options.MaxEvents < 2 {
		options.MaxEvents = 2
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = defaultTranscriptBytes
	}
	if options.MaxFieldSize <= 0 {
		options.MaxFieldSize = defaultTranscriptFieldSize
	}
	// The truncation marker and final outcome are mandatory semantic events. A
	// caller-supplied budget below their encoded size is normalized upward so
	// the ledger never loses its final status or emits a partial marker.
	options.MaxFieldSize = max(options.MaxFieldSize, len(transcriptTruncatedText))
	options.MaxBytes = max(options.MaxBytes, mandatoryTranscriptBytes())
	return &TranscriptLedger{
		maxEvents:    options.MaxEvents,
		maxBytes:     options.MaxBytes,
		maxFieldSize: options.MaxFieldSize,
	}
}

// Append adds one event unless the ledger has been frozen or bounded.
func (ledger *TranscriptLedger) Append(event TranscriptEvent) {
	if ledger == nil || ledger.frozen || ledger.outcomeSeen {
		return
	}
	if ledger.truncated && event.Kind != TranscriptOutcome {
		return
	}
	event = ledger.normalize(event)
	if event.Kind == TranscriptOutcome {
		ledger.appendOutcome(event)
		return
	}
	if !ledger.canFit(event) {
		ledger.addTruncation()
		return
	}
	ledger.appendRetained(event)
}

// Freeze prevents further mutation and returns an immutable event snapshot.
func (ledger *TranscriptLedger) Freeze() []TranscriptEvent {
	if ledger == nil {
		return nil
	}
	ledger.frozen = true
	return ledger.Events()
}

// Events returns a copy of the current event sequence.
func (ledger *TranscriptLedger) Events() []TranscriptEvent {
	if ledger == nil {
		return nil
	}
	return append([]TranscriptEvent(nil), ledger.events...)
}

// Bytes reports the accounted UTF-8 bytes in the ledger.
func (ledger *TranscriptLedger) Bytes() int {
	if ledger == nil {
		return 0
	}
	return ledger.bytes
}

// Render returns a control-free line-oriented transcript projection.
func (ledger *TranscriptLedger) Render() string {
	if ledger == nil {
		return ""
	}
	answers := make([]string, 0)
	work := make([]string, 0)
	var outcome *TranscriptEvent
	for _, event := range ledger.events {
		switch event.Kind {
		case TranscriptAsk:
			if line := event.renderLine(); line != "" {
				answers = append(answers, line)
			}
		case TranscriptMilestone, TranscriptPhase, TranscriptTruncated:
			if line := event.renderLine(); line != "" {
				work = append(work, line)
			}
		case TranscriptOutcome:
			copy := event
			outcome = &copy
		}
	}

	sections := make([]string, 0, 4)
	if len(answers) > 0 {
		sections = append(sections, transcriptSection("ANSWERS", answers))
	}
	if len(work) > 0 {
		sections = append(sections, transcriptSection("WORK", work))
	}
	if outcome != nil {
		outcomeLines := make([]string, 0, 2)
		if outcome.Location != "" {
			outcomeLines = append(outcomeLines, "AT       "+outcome.Location)
		}
		outcomeLines = append(outcomeLines, "OUTCOME  "+outcome.renderOutcomeLine())
		sections = append(sections, strings.Join(outcomeLines, "\n"))
	}
	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n") + "\n"
}

func transcriptSection(name string, lines []string) string {
	section := make([]string, 0, len(lines)+1)
	section = append(section, name)
	section = append(section, lines...)
	return strings.Join(section, "\n")
}

func (ledger *TranscriptLedger) normalize(event TranscriptEvent) TranscriptEvent {
	event.Label = normalizeTranscriptField(event.Label, ledger.maxFieldSize)
	event.Text = normalizeTranscriptField(event.Text, ledger.maxFieldSize)
	event.PhaseID = normalizeTranscriptField(event.PhaseID, ledger.maxFieldSize)
	event.Location = normalizeTranscriptField(event.Location, ledger.maxFieldSize)
	event.Summary = normalizeTranscriptField(event.Summary, ledger.maxFieldSize)
	return event
}

func (ledger *TranscriptLedger) canFit(event TranscriptEvent) bool {
	// Keep event slots and byte budget available for both the truncation marker
	// and the eventual outcome, which is appended after all work is complete.
	if len(ledger.events) >= max(ledger.maxEvents-2, 0) {
		return false
	}
	markerBytes := transcriptEventBytes(TranscriptEvent{Kind: TranscriptTruncated, Text: transcriptTruncatedText})
	return ledger.bytes+transcriptEventBytes(event)+markerBytes+transcriptOutcomeReserveBytes() <= ledger.maxBytes
}

func (ledger *TranscriptLedger) addTruncation() {
	if ledger.truncated {
		return
	}
	ledger.truncated = true
	marker := ledger.normalize(TranscriptEvent{
		Kind: TranscriptTruncated,
		Text: transcriptTruncatedText,
	})
	if len(ledger.events) < ledger.maxEvents && ledger.bytes+transcriptEventBytes(marker) <= ledger.maxBytes {
		ledger.appendRetained(marker)
	}
}

func (ledger *TranscriptLedger) appendRetained(event TranscriptEvent) {
	event.Sequence = ledger.nextSequence + 1
	ledger.nextSequence = event.Sequence
	ledger.events = append(ledger.events, event)
	ledger.bytes += transcriptEventBytes(event)
}

func (ledger *TranscriptLedger) appendOutcome(event TranscriptEvent) {
	if ledger.frozen {
		return
	}
	if !ledger.truncated && len(ledger.events) < ledger.maxEvents && ledger.bytes+transcriptEventBytes(event) <= ledger.maxBytes {
		ledger.appendRetained(event)
		ledger.outcomeSeen = true
		return
	}
	if !ledger.truncated {
		ledger.addTruncation()
	}
	if len(ledger.events) < ledger.maxEvents && ledger.bytes+transcriptEventBytes(event) <= ledger.maxBytes {
		ledger.appendRetained(event)
		ledger.outcomeSeen = true
	}
}

func transcriptOutcomeReserveBytes() int {
	return transcriptEventBytes(TranscriptEvent{Kind: TranscriptOutcome, Outcome: Succeeded})
}

func mandatoryTranscriptBytes() int {
	return transcriptEventBytes(TranscriptEvent{Kind: TranscriptTruncated, Text: transcriptTruncatedText}) + transcriptOutcomeReserveBytes()
}

func (event TranscriptEvent) renderLine() string {
	switch event.Kind {
	case TranscriptAsk:
		if event.Label == "" {
			return event.Text
		}
		return event.Label + ": " + event.Text
	case TranscriptMilestone:
		return event.Text
	case TranscriptPhase:
		if event.Text == "" {
			return event.Label + " (" + event.State.String() + ")"
		}
		return event.Label + " (" + event.State.String() + "): " + event.Text
	case TranscriptOutcome:
		return event.renderOutcomeLine()
	case TranscriptTruncated:
		return event.Text
	default:
		return event.Text
	}
}

func (event TranscriptEvent) renderOutcomeLine() string {
	line := event.Outcome.String()
	if event.Summary != "" {
		line += ": " + event.Summary
	}
	return line
}

func interactionTranscriptText(request InteractionRequest, answer InteractionAnswer) string {
	if request.Sensitive || request.Kind == InteractionSecret {
		return "[redacted]"
	}
	if request.TranscriptProject != nil {
		return request.TranscriptProject(answer)
	}
	switch request.Kind {
	case InteractionSelect:
		for _, option := range request.Options {
			if option.Value == answer.Value {
				return option.Label
			}
		}
		return answer.Value
	case InteractionMultiSelect:
		labels := make([]string, 0, len(answer.Values))
		for _, value := range answer.Values {
			label := value
			for _, option := range request.Options {
				if option.Value == value {
					label = option.Label
					break
				}
			}
			labels = append(labels, label)
		}
		return strings.Join(labels, ", ")
	case InteractionConfirm:
		if answer.Confirmed {
			return "yes"
		}
		return "no"
	default:
		return answer.Value
	}
}

func (document PresentationDocument) transcriptText() string {
	parts := make([]string, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		text := block.Text
		if block.Sensitive {
			text = "[redacted]"
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func transcriptEventBytes(event TranscriptEvent) int {
	return len(event.Label) + len(event.Text) + len(event.PhaseID) + len(event.Location) + len(event.Summary) + 32
}

func normalizeTranscriptField(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "�")
	value = ansi.Strip(value)
	var output strings.Builder
	output.Grow(len(value))
	for _, character := range value {
		switch {
		case character == '\r' || character == '\n' || character == '\t':
			output.WriteByte(' ')
		case unicode.IsControl(character):
			if character <= 0xff {
				_, _ = fmt.Fprintf(&output, "\\x%02x", character)
			} else {
				output.WriteRune('�')
			}
		default:
			output.WriteRune(character)
		}
	}
	value = strings.Join(strings.Fields(output.String()), " ")
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	bytes := []byte(value)
	bytes = bytes[:maxBytes]
	for !utf8Boundary(bytes) {
		bytes = bytes[:len(bytes)-1]
	}
	return string(bytes)
}

func utf8Boundary(value []byte) bool {
	if len(value) == 0 {
		return true
	}
	for index := 0; index < len(value); index++ {
		if value[index]&0xc0 == 0x80 {
			continue
		}
		width := 1
		switch {
		case value[index] < 0x80:
			width = 1
		case value[index]&0xe0 == 0xc0:
			width = 2
		case value[index]&0xf0 == 0xe0:
			width = 3
		case value[index]&0xf8 == 0xf0:
			width = 4
		default:
			return false
		}
		if index+width > len(value) {
			return false
		}
		index += width - 1
	}
	return true
}
