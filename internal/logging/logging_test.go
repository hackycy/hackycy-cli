package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestParseLevelNormalizesInput(t *testing.T) {
	level, err := ParseLevel(" WARN ")
	if err != nil {
		t.Fatalf("ParseLevel returned an error: %v", err)
	}
	if level != Warn {
		t.Fatalf("level = %v, want %v", level, Warn)
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("ParseLevel accepted an invalid level")
	}
}

func TestRuntimeFiltersAndRedacts(t *testing.T) {
	var output bytes.Buffer
	runtime := NewRuntime(Options{
		Writer: &output,
		Now:    func() time.Time { return time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC) },
	})
	runtime.SetLevel(Warn)
	logger := runtime.Logger("cli").With(map[string]any{"request": "one", "token": "secret"})
	logger.Info("Bearer top-secret", nil)
	logger.Warn("password=not-for-logs\ncontinuing", map[string]any{"authorization": "hidden", "count": 2})

	line := output.String()
	if strings.Contains(line, "top-secret") || strings.Contains(line, "not-for-logs") || strings.Contains(line, "hidden") || strings.Contains(line, `"token":"secret"`) {
		t.Fatalf("secret leaked in %q", line)
	}
	for _, expected := range []string{"2026-08-23T00:00:00.000Z WARN cli:", `password=[REDACTED]\ncontinuing`, `"authorization":"[REDACTED]"`, `"count":2`} {
		if !strings.Contains(line, expected) {
			t.Fatalf("output %q does not contain %q", line, expected)
		}
	}
}

func TestStripANSIRemovesStylingWithoutChangingText(t *testing.T) {
	if got, want := StripANSI("one\x1b[31mtwo\x1b[0m\nthree\tend"), "onetwo\nthree\tend"; got != want {
		t.Fatalf("StripANSI() = %q, want %q", got, want)
	}
}

func TestRuntimeRoutesRedactedDiagnosticsOnlyToInjectedStderr(t *testing.T) {
	streams := terminaltest.NewRedirectedStreams("")
	runtime := NewRuntime(Options{
		Writer: streams.Stderr,
		Now:    func() time.Time { return time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC) },
	})
	runtime.Logger("tunnel.server").Info("Bearer service-token", map[string]any{
		"authorization": "hidden-value",
		"port":          7000,
	})

	if streams.Stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", streams.Stdout.String())
	}
	if output := streams.Stderr.String(); strings.Contains(output, "service-token") || strings.Contains(output, "hidden-value") || !strings.Contains(output, "Bearer [REDACTED]") || !strings.Contains(output, `"authorization":"[REDACTED]"`) {
		t.Fatalf("stderr = %q", output)
	}
	if terminaltest.ContainsTerminalControl(streams.Stdout.Bytes()) || terminaltest.ContainsTerminalControl(streams.Stderr.Bytes()) {
		t.Fatalf("diagnostic streams contain terminal control: stdout = %q stderr = %q", streams.Stdout.String(), streams.Stderr.String())
	}
}

func TestRuntimeBuffersRedactedRecordsForLeaseAwareFIFO(t *testing.T) {
	destination := &recordingWriter{}
	diagnostics := terminal.NewLeaseAwareDiagnosticWriter(destination)
	runtime := NewRuntime(Options{
		Writer: diagnostics,
		Now:    func() time.Time { return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) },
	})
	lease := diagnostics.AcquireRendererLease()
	runtime.Logger("sync").Info("first event token=first-secret", map[string]any{"authorization": "first-header"})
	runtime.Logger("sync").Warn("second event token=second-secret", map[string]any{"authorization": "second-header"})

	if len(destination.writes) != 0 {
		t.Fatalf("diagnostics escaped active renderer lease: %#v", destination.writes)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("release renderer lease: %v", err)
	}
	if got := len(destination.writes); got != 2 {
		t.Fatalf("diagnostic writes = %#v, want two complete records", destination.writes)
	}
	for _, secret := range []string{"first-secret", "first-header", "second-secret", "second-header"} {
		if strings.Contains(strings.Join(destination.writes, ""), secret) {
			t.Fatalf("deferred diagnostics leaked %q: %#v", secret, destination.writes)
		}
	}
	for _, record := range destination.writes {
		if strings.Count(record, "\n") != 1 || !strings.HasSuffix(record, "\n") {
			t.Fatalf("diagnostic write is not one complete record: %q", record)
		}
	}
	if !strings.Contains(destination.writes[0], "first event") || !strings.Contains(destination.writes[1], "second event") {
		t.Fatalf("deferred diagnostics lost FIFO order: %#v", destination.writes)
	}
}

func TestRuntimeReconfiguresExistingLoggerAndColor(t *testing.T) {
	var output bytes.Buffer
	runtime := NewRuntime(Options{Writer: &output, Color: true})
	logger := runtime.Logger("scope")
	runtime.SetLevel(Error)
	logger.Warn("not written", nil)
	logger.Error("written", nil)

	if strings.Contains(output.String(), "not written") || !strings.Contains(output.String(), "ERROR") || !strings.Contains(output.String(), "scope:") || !strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("unexpected output %q", output.String())
	}
}

func TestRuntimeUsesLogV2TextStyles(t *testing.T) {
	var output bytes.Buffer
	runtime := NewRuntime(Options{
		Writer: &output,
		Now:    func() time.Time { return time.Date(2026, 8, 26, 12, 34, 56, 789000000, time.UTC) },
		Color:  true,
	})
	runtime.SetLevel(Debug)
	logger := runtime.Logger("scope")
	logger.Debug("debug", nil)
	logger.Info("info", nil)
	logger.Warn("warn", nil)
	logger.Error("error", nil)

	for _, expected := range []string{
		"DEBUG",
		"INFO",
		"WARN",
		"ERROR",
		"scope:",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("rich text record %q does not contain %q", output.String(), expected)
		}
	}
	if !strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("rich text record has no terminal styles: %q", output.String())
	}
}

func TestRuntimeFormatsTextAndNDJSONRecords(t *testing.T) {
	timestamp := func() time.Time { return time.Date(2026, 8, 26, 12, 34, 56, 789000000, time.UTC) }

	var text bytes.Buffer
	textRuntime := NewRuntime(Options{Writer: &text, Now: timestamp})
	textRuntime.Logger("tunnel.server").Info("Tunnel started", map[string]any{"port": 7000})
	if got, want := text.String(), "2026-08-26T12:34:56.789Z INFO tunnel.server: Tunnel started {\"port\":7000}\n"; got != want {
		t.Fatalf("text record = %q, want %q", got, want)
	}

	var jsonOutput bytes.Buffer
	jsonRuntime := NewRuntime(Options{Writer: &jsonOutput, Now: timestamp, Color: true, Format: JSONFormat})
	jsonRuntime.Logger("tunnel.server").Info("Tunnel started", map[string]any{"port": 7000})
	if got, want := jsonOutput.String(), "{\"timestamp\":\"2026-08-26T12:34:56.789Z\",\"level\":\"info\",\"scope\":\"tunnel.server\",\"message\":\"Tunnel started\",\"context\":{\"port\":7000}}\n"; got != want {
		t.Fatalf("NDJSON record = %q, want %q", got, want)
	}
	if terminaltest.ContainsTerminalControl(jsonOutput.Bytes()) {
		t.Fatalf("NDJSON contains terminal control: %q", jsonOutput.String())
	}

	var bareJSON bytes.Buffer
	bareRuntime := NewRuntime(Options{Writer: &bareJSON, Now: timestamp, Format: JSONFormat})
	bareRuntime.Logger("").Info("Tunnel started", nil)
	if got, want := bareJSON.String(), "{\"timestamp\":\"2026-08-26T12:34:56.789Z\",\"level\":\"info\",\"message\":\"Tunnel started\"}\n"; got != want {
		t.Fatalf("NDJSON record without optional fields = %q, want %q", got, want)
	}
}

func TestRuntimePlainAndNoColorRecordsStayUnstyled(t *testing.T) {
	for _, format := range []RecordFormat{TextFormat, JSONFormat} {
		t.Run(string(format), func(t *testing.T) {
			var output bytes.Buffer
			runtime := NewRuntime(Options{
				Writer: &output,
				Now:    func() time.Time { return time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC) },
				Color:  false,
				Format: format,
			})
			runtime.Logger("scope").Warn("Review this", nil)
			if terminaltest.ContainsTerminalControl(output.Bytes()) {
				t.Fatalf("unstyled %s record contains terminal control: %q", format, output.String())
			}
		})
	}
}

func TestRuntimeRecursivelyRedactsCredentialShapedDiagnostics(t *testing.T) {
	secrets := []string{
		"message-bearer-token",
		"message-api-key",
		"root-authorization",
		"nested-api-key",
		"request-password",
		"array-bearer-token",
		"error-cookie",
		"private-key-value",
		"field-credential",
	}

	for _, format := range []RecordFormat{TextFormat, JSONFormat} {
		t.Run(string(format), func(t *testing.T) {
			var output bytes.Buffer
			runtime := NewRuntime(Options{Writer: &output, Format: format})
			logger := runtime.Logger("tunnel.server").With(map[string]any{
				"Authorization": "root-authorization",
				"metadata": map[string]any{
					"API-Key":      "nested-api-key",
					"request_body": "request-password",
					"values": []any{
						"Bearer array-bearer-token",
						errors.New("cookie=error-cookie"),
						map[string]string{"private.key": "private-key-value"},
					},
				},
			})
			logger.Warn("Bearer message-bearer-token API_KEY=message-api-key", map[string]any{
				"credentials": "field-credential",
				"safe":        "visible",
			})

			got := output.String()
			for _, secret := range secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("%s record leaked %q: %q", format, secret, got)
				}
			}
			if !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "visible") {
				t.Fatalf("%s record lost redaction marker or safe value: %q", format, got)
			}
			if format == JSONFormat {
				var record map[string]any
				if err := json.Unmarshal(output.Bytes(), &record); err != nil {
					t.Fatalf("NDJSON record is invalid: %v; output = %q", err, got)
				}
			}
		})
	}
}

func TestRuntimeReplacesOnlyUnencodableNestedValues(t *testing.T) {
	for _, format := range []RecordFormat{TextFormat, JSONFormat} {
		t.Run(string(format), func(t *testing.T) {
			var output bytes.Buffer
			runtime := NewRuntime(Options{Writer: &output, Format: format})
			runtime.Logger("scope").Info("record", map[string]any{
				"safe":   "visible",
				"nested": []any{math.Inf(1)},
			})

			got := output.String()
			if !strings.Contains(got, "visible") || !strings.Contains(got, "[UNENCODABLE]") {
				t.Fatalf("%s record did not preserve safe context with a placeholder: %q", format, got)
			}
			if strings.Contains(got, "context encoding failed") {
				t.Fatalf("%s record discarded the entire context: %q", format, got)
			}
		})
	}
}

func TestRedactRecognizesCredentialTextShapes(t *testing.T) {
	tests := []struct {
		input  string
		secret string
	}{
		{input: "API_KEY=api-key-value", secret: "api-key-value"},
		{input: "private key: private-key-value", secret: "private-key-value"},
		{input: "request-body=request-body-value", secret: "request-body-value"},
		{input: "Authorization: Bearer authorization-value", secret: "authorization-value"},
	}
	for _, test := range tests {
		got := Redact(test.input)
		if strings.Contains(got, test.secret) || !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("Redact(%q) = %q", test.input, got)
		}
	}
	if got := Redact("project=demo"); got != "project=demo" {
		t.Fatalf("Redact redacted a non-credential value: %q", got)
	}
}

type recordingWriter struct {
	writes []string
}

func (writer *recordingWriter) Write(value []byte) (int, error) {
	writer.writes = append(writer.writes, string(value))
	return len(value), nil
}
