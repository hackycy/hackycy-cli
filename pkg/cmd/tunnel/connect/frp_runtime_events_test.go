package connect

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

func TestClientFRPRuntimeObserverMapsEventsToDiagnostics(t *testing.T) {
	var stdout, diagnostics bytes.Buffer
	logRuntime := logging.NewRuntime(logging.Options{Writer: &diagnostics, Format: logging.JSONFormat, Color: false})
	logRuntime.SetLevel(logging.Debug)
	observer := clientFRPRuntimeObserver(logRuntime.Logger("tunnel.client"))
	totalBytes, percent := int64(100), 25
	events := []tunnelruntime.FRPRuntimeEvent{
		{Type: tunnelruntime.FRPRuntimeEventReuse, Version: tunnelruntime.FRPVersion, Directory: "/runtime"},
		{Type: tunnelruntime.FRPRuntimeEventDownloadStart, Version: tunnelruntime.FRPVersion, Archive: "frp.tar.gz", URL: "https://example.test/frp.tar.gz", Directory: "/runtime"},
		{Type: tunnelruntime.FRPRuntimeEventDownloadProgress, ReceivedBytes: 25, TotalBytes: &totalBytes, Percent: &percent},
		{Type: tunnelruntime.FRPRuntimeEventDownloadDone, ReceivedBytes: 100, Elapsed: 1250 * time.Millisecond},
		{Type: tunnelruntime.FRPRuntimeEventVerifyArchive, SHA256: "archive-sha"},
		{Type: tunnelruntime.FRPRuntimeEventExtract, Archive: "frp.tar.gz", FRPCSHA256: "frpc-sha", FRPSSHA256: "frps-sha"},
		{Type: tunnelruntime.FRPRuntimeEventPublish, Directory: "/runtime"},
		{Type: tunnelruntime.FRPRuntimeEventProbe, Binary: "/runtime/frpc", Version: tunnelruntime.FRPVersion},
		{Type: tunnelruntime.FRPRuntimeEventReady, Directory: "/runtime", FRPC: "/runtime/frpc", FRPS: "/runtime/frps", Downloaded: true},
		{Type: tunnelruntime.FRPRuntimeEventFailed, FailureStage: tunnelruntime.FRPRuntimeFailureProbe},
	}
	for _, event := range events {
		observer(event)
	}

	records := decodeClientFRPRuntimeRecords(t, diagnostics.Bytes())
	wantLevels := []string{"info", "info", "debug", "info", "debug", "debug", "debug", "debug", "info", "error"}
	if len(records) != len(events) {
		t.Fatalf("FRP runtime records = %#v", records)
	}
	for index, record := range records {
		wantEvent := "frp.runtime." + string(events[index].Type)
		if record.Level != wantLevels[index] || record.Context["event"] != wantEvent {
			t.Fatalf("FRP runtime record %d = %#v, want level %q event %q", index, record, wantLevels[index], wantEvent)
		}
	}
	if records[2].Context["receivedBytes"] != float64(25) || records[2].Context["totalBytes"] != float64(100) || records[2].Context["percent"] != float64(25) {
		t.Fatalf("progress context = %#v", records[2].Context)
	}
	if records[3].Context["elapsedMs"] != float64(1250) || records[8].Context["downloaded"] != true || records[9].Context["stage"] != "probe" {
		t.Fatalf("FRP runtime contexts = done %#v ready %#v failed %#v", records[3].Context, records[8].Context, records[9].Context)
	}
	if stdout.Len() != 0 || terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
		t.Fatalf("FRP runtime streams = stdout %q diagnostics %q", stdout.String(), diagnostics.String())
	}
}

func TestClientFRPRuntimeObserverHonorsInfoFiltering(t *testing.T) {
	var diagnostics bytes.Buffer
	logRuntime := logging.NewRuntime(logging.Options{Writer: &diagnostics, Format: logging.JSONFormat})
	observer := clientFRPRuntimeObserver(logRuntime.Logger("tunnel.client"))
	observer(tunnelruntime.FRPRuntimeEvent{Type: tunnelruntime.FRPRuntimeEventDownloadProgress, ReceivedBytes: 1})
	observer(tunnelruntime.FRPRuntimeEvent{Type: tunnelruntime.FRPRuntimeEventReady, Directory: "/runtime"})
	records := decodeClientFRPRuntimeRecords(t, diagnostics.Bytes())
	if len(records) != 1 || records[0].Context["event"] != "frp.runtime.ready" {
		t.Fatalf("info-filtered records = %#v", records)
	}
}

type clientFRPRuntimeRecord struct {
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Context map[string]any `json:"context"`
}

func decodeClientFRPRuntimeRecords(t *testing.T, contents []byte) []clientFRPRuntimeRecord {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var records []clientFRPRuntimeRecord
	for {
		var record clientFRPRuntimeRecord
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				return records
			}
			t.Fatalf("decode FRP runtime record: %v", err)
		}
		records = append(records, record)
	}
}
