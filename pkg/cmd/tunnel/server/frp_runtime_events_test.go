package server

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestServerFRPRuntimeObserverMapsEventsToDiagnostics(t *testing.T) {
	var stdout, diagnostics bytes.Buffer
	logRuntime := logging.NewRuntime(logging.Options{Writer: &diagnostics, Format: logging.JSONFormat, Color: false})
	logRuntime.SetLevel(logging.Debug)
	observer := serverFRPRuntimeObserver(logRuntime.Logger("tunnel.server"))
	totalBytes, percent := int64(200), 50
	events := []tunnelruntime.FRPRuntimeEvent{
		{Type: tunnelruntime.FRPRuntimeEventReuse, Version: tunnelruntime.FRPVersion, Directory: "/runtime"},
		{Type: tunnelruntime.FRPRuntimeEventDownloadStart, Version: tunnelruntime.FRPVersion, Archive: "frp.tar.gz", URL: "https://example.test/frp.tar.gz", Directory: "/runtime"},
		{Type: tunnelruntime.FRPRuntimeEventDownloadProgress, ReceivedBytes: 100, TotalBytes: &totalBytes, Percent: &percent},
		{Type: tunnelruntime.FRPRuntimeEventDownloadDone, ReceivedBytes: 200, Elapsed: 2 * time.Second},
		{Type: tunnelruntime.FRPRuntimeEventVerifyArchive, SHA256: "archive-sha"},
		{Type: tunnelruntime.FRPRuntimeEventExtract, Archive: "frp.tar.gz", FRPCSHA256: "frpc-sha", FRPSSHA256: "frps-sha"},
		{Type: tunnelruntime.FRPRuntimeEventPublish, Directory: "/runtime"},
		{Type: tunnelruntime.FRPRuntimeEventProbe, Binary: "/runtime/frps", Version: tunnelruntime.FRPVersion},
		{Type: tunnelruntime.FRPRuntimeEventReady, Directory: "/runtime", FRPC: "/runtime/frpc", FRPS: "/runtime/frps", Downloaded: false},
		{Type: tunnelruntime.FRPRuntimeEventFailed, FailureStage: tunnelruntime.FRPRuntimeFailurePublish},
	}
	for _, event := range events {
		observer(event)
	}

	records := decodeServerFRPRuntimeRecords(t, diagnostics.Bytes())
	wantLevels := []string{"info", "info", "debug", "info", "debug", "debug", "debug", "debug", "info", "error"}
	if len(records) != len(events) {
		t.Fatalf("FRPS runtime records = %#v", records)
	}
	for index, record := range records {
		wantEvent := "frps.runtime." + string(events[index].Type)
		if record.Level != wantLevels[index] || record.Context["event"] != wantEvent {
			t.Fatalf("FRPS runtime record %d = %#v, want level %q event %q", index, record, wantLevels[index], wantEvent)
		}
	}
	if records[0].Message != "Managed FRPS runtime reused" || records[3].Context["elapsedMs"] != float64(2000) || records[8].Context["downloaded"] != false || records[9].Context["stage"] != "publish" {
		t.Fatalf("FRPS runtime records = reuse %#v done %#v ready %#v failed %#v", records[0], records[3], records[8], records[9])
	}
	if stdout.Len() != 0 || terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
		t.Fatalf("FRPS runtime streams = stdout %q diagnostics %q", stdout.String(), diagnostics.String())
	}
}

type serverFRPRuntimeRecord struct {
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Context map[string]any `json:"context"`
}

func decodeServerFRPRuntimeRecords(t *testing.T, contents []byte) []serverFRPRuntimeRecord {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var records []serverFRPRuntimeRecord
	for {
		var record serverFRPRuntimeRecord
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				return records
			}
			t.Fatalf("decode FRPS runtime record: %v", err)
		}
		records = append(records, record)
	}
}
