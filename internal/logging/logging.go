package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/charmbracelet/colorprofile"
)

// Level controls which structured messages a Runtime writes.
type Level uint8

const (
	Debug Level = iota
	Info
	Warn
	Error
)

// ParseLevel accepts the global CLI spelling.
func ParseLevel(value string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return Debug, nil
	case "info", "":
		return Info, nil
	case "warn":
		return Warn, nil
	case "error":
		return Error, nil
	default:
		return Info, fmt.Errorf("invalid log level %q (expected debug, info, warn, or error)", value)
	}
}

func (level Level) String() string {
	switch level {
	case Debug:
		return "DEBUG"
	case Info:
		return "INFO"
	case Warn:
		return "WARN"
	case Error:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Options makes logging dependencies explicit at the composition root.
type Options struct {
	Writer io.Writer
	Now    func() time.Time
	Color  bool
	Format RecordFormat
}

// Runtime owns filtering, formatting, redaction, and output for scoped loggers.
type Runtime struct {
	mu     sync.RWMutex
	level  Level
	writer io.Writer
	now    func() time.Time
	color  bool
	format RecordFormat
}

// NewRuntime creates a runtime at the conventional info level.
func NewRuntime(options Options) *Runtime {
	if options.Writer == nil {
		options.Writer = os.Stderr
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Runtime{
		level:  Info,
		writer: options.Writer,
		now:    options.Now,
		color:  options.Color,
		format: normalizeRecordFormat(options.Format),
	}
}

func (runtime *Runtime) SetLevel(level Level) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.level = level
}

func (runtime *Runtime) Level() Level {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.level
}

// SetFormat changes the schema used for subsequent Diagnostic Records.
func (runtime *Runtime) SetFormat(format RecordFormat) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.format = normalizeRecordFormat(format)
}

// Format returns the current Diagnostic Record schema.
func (runtime *Runtime) Format() RecordFormat {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	return runtime.format
}

// Logger creates a logger whose scope is part of every message.
func (runtime *Runtime) Logger(scope string) Logger {
	return Logger{runtime: runtime, scope: scope}
}

// Logger carries only a scope and redacted structured context.
type Logger struct {
	runtime *Runtime
	scope   string
	context map[string]any
}

// With returns a child logger without mutating the parent context.
func (logger Logger) With(fields map[string]any) Logger {
	context := make(map[string]any, len(logger.context)+len(fields))
	for key, value := range logger.context {
		context[key] = value
	}
	for key, value := range fields {
		context[key] = value
	}
	logger.context = context
	return logger
}

func (logger Logger) Debug(message string, fields map[string]any) { logger.Log(Debug, message, fields) }
func (logger Logger) Info(message string, fields map[string]any)  { logger.Log(Info, message, fields) }
func (logger Logger) Warn(message string, fields map[string]any)  { logger.Log(Warn, message, fields) }
func (logger Logger) Error(message string, fields map[string]any) { logger.Log(Error, message, fields) }

// Event writes a lifecycle record with a stable semantic identifier. The
// identifier is kept inside the existing context object so text and NDJSON
// records retain the established outer schema.
func (logger Logger) Event(level Level, event, message string, fields map[string]any) {
	context := make(map[string]any, len(logger.context)+len(fields)+1)
	for key, value := range logger.context {
		context[key] = value
	}
	for key, value := range fields {
		context[key] = value
	}
	if strings.TrimSpace(event) != "" {
		context["event"] = strings.TrimSpace(event)
	}
	logger.Log(level, message, context)
}

// Log formats one redacted structured line on stderr-selected output.
func (logger Logger) Log(level Level, message string, fields map[string]any) {
	if logger.runtime == nil {
		return
	}

	logger.runtime.mu.Lock()
	defer logger.runtime.mu.Unlock()
	if level < logger.runtime.level {
		return
	}

	context := make(map[string]any, len(logger.context)+len(fields))
	for key, value := range logger.context {
		context[key] = value
	}
	for key, value := range fields {
		context[key] = value
	}

	loggedAt := logger.runtime.now().UTC()
	record := diagnosticRecord{
		at:        loggedAt,
		timestamp: diagnosticTimestamp(loggedAt),
		level:     level,
		scope:     logger.scope,
		message:   Redact(message),
		context:   redactContext(context),
	}
	_, _ = io.WriteString(logger.runtime.writer, renderRecord(record, logger.runtime.format, logger.runtime.color))
}

type diagnosticRecord struct {
	at        time.Time
	timestamp string
	level     Level
	scope     string
	message   string
	context   map[string]any
}

func diagnosticTimestamp(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func renderRecord(record diagnosticRecord, format RecordFormat, color bool) string {
	if format == JSONFormat {
		return renderJSONRecord(record)
	}
	return textAdapter{color: color}.render(record)
}

// textAdapter is the private Log v2 boundary. Runtime passes it only complete,
// normalized records, and performs the single write after this buffer is ready.
type textAdapter struct {
	color bool
}

func (adapter textAdapter) render(record diagnosticRecord) string {
	var output bytes.Buffer
	logger := charmlog.NewWithOptions(&output, charmlog.Options{
		Level:           charmlog.DebugLevel,
		Prefix:          record.scope,
		ReportTimestamp: true,
		TimeFormat:      "2006-01-02T15:04:05.000Z",
		Formatter:       charmlog.TextFormatter,
	})
	styles := charmlog.DefaultStyles()
	for level, label := range map[charmlog.Level]string{
		charmlog.DebugLevel: "DEBUG",
		charmlog.InfoLevel:  "INFO",
		charmlog.WarnLevel:  "WARN",
		charmlog.ErrorLevel: "ERROR",
	} {
		styles.Levels[level] = styles.Levels[level].SetString(label).MaxWidth(len(label))
	}
	logger.SetStyles(styles)
	if adapter.color {
		logger.SetColorProfile(colorprofile.TrueColor)
	} else {
		logger.SetColorProfile(colorprofile.NoTTY)
	}
	entry := slog.NewRecord(record.at, charmLogLevel(record.level), textMessage(record), 0)
	_ = logger.Handle(context.Background(), entry)
	return output.String()
}

func charmLogLevel(level Level) slog.Level {
	switch level {
	case Debug:
		return slog.Level(charmlog.DebugLevel)
	case Info:
		return slog.Level(charmlog.InfoLevel)
	case Warn:
		return slog.Level(charmlog.WarnLevel)
	case Error:
		return slog.Level(charmlog.ErrorLevel)
	default:
		return slog.Level(charmlog.ErrorLevel)
	}
}

func textMessage(record diagnosticRecord) string {
	message := record.message
	if symbol := lifecycleSymbol(record.scope, record.message, record.level, record.context["event"] != nil); symbol != "" {
		message = symbol + "  " + message
	}
	if len(record.context) == 0 {
		return message
	}
	return message + " " + marshalContext(record.context)
}

func lifecycleSymbol(scope, message string, level Level, lifecycleEvent bool) string {
	if scope == "tunnel.client" || scope == "tunnel.server" || scope == "upgrade" {
		if !lifecycleEvent {
			return ""
		}
		if level == Warn {
			return "!"
		}
		if level == Error {
			return "×"
		}
		return lifecycleEventSymbol(message)
	}
	if scope != "diff" && scope != "fs" {
		return ""
	}
	if level == Warn {
		return "!"
	}
	if level == Error {
		return "✕"
	}
	switch message {
	case "Directory diff started", "Diff endpoints available", "Comparison workspace configured", "Initial comparison refresh started", "Comparison refresh started",
		"File Browser started", "Browse root configured", "File Browser capabilities configured", "File Browser authentication configured",
		"File Browser stopping", "Download task accepted", "Download task started", "Extraction task accepted", "Extraction task started", "Chunked upload started":
		return "●"
	case "Comparison refresh phase":
		return "·"
	case "Comparison snapshot ready", "Directory diff stopped", "File Browser stopped", "Download task completed", "Extraction task completed", "Chunked upload completed":
		return "✓"
	case "Comparison refresh cancelled", "Download task cancelled", "Extraction task cancelled", "Chunked upload cancelled", "Chunked upload expired":
		return "⊘"
	default:
		return ""
	}
}

func lifecycleEventSymbol(message string) string {
	if strings.Contains(message, "starting") || strings.Contains(message, "preparing") || strings.Contains(message, "scheduled") {
		return "▶"
	}
	if strings.Contains(message, "recovering") {
		return "↻"
	}
	if strings.Contains(message, "stopped") || strings.Contains(message, "complete") || strings.Contains(message, "authenticated") || strings.Contains(message, "restored") || strings.Contains(message, "running") || strings.Contains(message, "applied") || strings.Contains(message, "recovered") || strings.Contains(message, "restarted") || strings.Contains(message, "started") {
		return "✓"
	}
	if strings.Contains(message, "requested") {
		return "■"
	}
	return ""
}

func renderJSONRecord(record diagnosticRecord) string {
	value := struct {
		Timestamp string          `json:"timestamp"`
		Level     string          `json:"level"`
		Scope     string          `json:"scope,omitempty"`
		Message   string          `json:"message"`
		Context   json.RawMessage `json:"context,omitempty"`
	}{
		Timestamp: record.timestamp,
		Level:     strings.ToLower(record.level.String()),
		Scope:     record.scope,
		Message:   record.message,
	}
	if len(record.context) > 0 {
		value.Context = json.RawMessage(marshalContext(record.context))
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return `{"timestamp":"` + record.timestamp + `","level":"error","message":"diagnostic encoding failed"}` + "\n"
	}
	return string(encoded) + "\n"
}

func marshalContext(context map[string]any) string {
	keys := make([]string, 0, len(context))
	for key := range context {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(context))
	for _, key := range keys {
		ordered[key] = context[key]
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return `{"logging":"context encoding failed"}`
	}
	return string(encoded)
}

func normalizeRecordFormat(format RecordFormat) RecordFormat {
	if format == JSONFormat {
		return JSONFormat
	}
	return TextFormat
}
