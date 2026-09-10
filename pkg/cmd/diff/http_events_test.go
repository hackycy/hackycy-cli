package diff

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPHandlerStreamsImmediateAndPublishedWorkspaceStates(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "before")
	writeComparisonFile(t, target, "changed.txt", "after")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	server := httptest.NewServer(NewHTTPHandler(workspace))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "https://attacker.example")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertHTTPEventHeaders(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("event status = %d", response.StatusCode)
	}
	if workspaceListenerCount(workspace) != 1 {
		t.Fatalf("workspace listeners = %d, want 1", workspaceListenerCount(workspace))
	}

	reader := bufio.NewReader(response.Body)
	initialRaw, initial := readHTTPEventState(t, reader)
	if initialRaw != "data: {\"version\":1,\"workspace\":{\"phase\":\"idle\"}}\n\n" || initial.Version != 1 || initial.Workspace.Phase != string(PhaseIdle) || initial.Snapshot != nil {
		t.Fatalf("initial event = %q, %#v", initialRaw, initial)
	}

	run, err := workspace.StartRefresh(context.Background())
	if err != nil {
		t.Fatalf("StartRefresh() error = %v", err)
	}
	refreshContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := run.Wait(refreshContext)
	if err != nil {
		t.Fatalf("refresh Wait() error = %v", err)
	}

	seen := make(map[WorkspacePhase]bool)
	for index := 0; index < 16; index++ {
		raw, event := readHTTPEventState(t, reader)
		if !strings.HasPrefix(raw, "data: ") || strings.Contains(raw, "event:") || strings.Contains(raw, "id:") || strings.Contains(raw, "retry:") {
			t.Fatalf("event framing = %q", raw)
		}
		phase := WorkspacePhase(event.Workspace.Phase)
		seen[phase] = true
		if phase == PhaseReady {
			if event.Snapshot == nil || event.Snapshot.ID != snapshot.Summary().ID || event.Workspace.SnapshotID != snapshot.Summary().ID {
				t.Fatalf("ready event = %#v, want snapshot %q", event, snapshot.Summary().ID)
			}
			break
		}
	}
	for _, phase := range []WorkspacePhase{PhaseDiscovering, PhaseComparing, PhasePublishing, PhaseReady} {
		if !seen[phase] {
			t.Fatalf("stream did not emit %q; seen %#v", phase, seen)
		}
	}

	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	waitForWorkspaceListenerCount(t, workspace, 0)
}

func TestHTTPHandlerRejectsInvalidEventMethods(t *testing.T) {
	baseline, target := comparisonRoots(t)
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	response := httpResponse(NewHTTPHandler(workspace), http.MethodPost, "/api/events")
	assertHTTPAPIHeaders(t, response)
	assertHTTPAPIError(t, response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
}

func assertHTTPEventHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Content-Security-Policy") != diffAPICSP || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || response.Header.Get("Referrer-Policy") != "no-referrer" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("event headers = %v", response.Header)
	}
}

func readHTTPEventState(t *testing.T, reader *bufio.Reader) (string, httpStateResponse) {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event line: %v", err)
	}
	separator, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event separator: %v", err)
	}
	if !strings.HasPrefix(line, "data: ") || separator != "\n" {
		t.Fatalf("event framing = %q%q", line, separator)
	}
	var payload httpStateResponse
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n")), &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	return line + separator, payload
}

func workspaceListenerCount(workspace *Workspace) int {
	workspace.mu.RLock()
	defer workspace.mu.RUnlock()
	return len(workspace.listeners)
}

func waitForWorkspaceListenerCount(t *testing.T, workspace *Workspace, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if workspaceListenerCount(workspace) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("workspace listeners = %d, want %d", workspaceListenerCount(workspace), want)
}
