package diff

import (
	"net/http"
	"os"
	"testing"
	"time"
)

func TestRunningServerStartsInitialRefreshAndCloses(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "same.txt", "same")
	writeComparisonFile(t, target, "same.txt", "same")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}

	server, err := StartServer(workspace, "127.0.0.1", 0)
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	if server.Port() == 0 || server.URL() == "" {
		t.Fatalf("server address = %q, port = %d", server.URL(), server.Port())
	}

	waitForWorkspacePhase(t, workspace, PhaseReady)
	response, err := http.Get(server.URL() + "/api/state")
	if err != nil {
		t.Fatalf("GET state: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("state status = %d", response.StatusCode)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if _, err := http.Get(server.URL() + "/api/state"); err == nil {
		t.Fatal("server still accepted a connection after Close")
	}
}

func TestRunningServerKeepsServingAfterInitialRefreshFailure(t *testing.T) {
	baseline, target := comparisonRoots(t)
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatalf("remove Target Directory: %v", err)
	}

	server, err := StartServer(workspace, "127.0.0.1", 0)
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	waitForWorkspacePhase(t, workspace, PhaseError)
	response, err := http.Get(server.URL() + "/api/state")
	if err != nil {
		t.Fatalf("GET state after refresh failure: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("state after refresh failure = %d", response.StatusCode)
	}
	if state := workspace.State(); state.Phase != PhaseError || state.Error == "" {
		t.Fatalf("workspace state = %#v", state)
	}
}

func TestRunningServerCloseCancelsAnActiveInitialRefresh(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "same.txt", "same")
	writeComparisonFile(t, target, "same.txt", "same")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}

	enteredComparing := make(chan struct{})
	releaseComparing := make(chan struct{})
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if state.Phase == PhaseComparing {
			select {
			case <-enteredComparing:
			default:
				close(enteredComparing)
				<-releaseComparing
			}
		}
	})
	defer unsubscribe()

	server, err := StartServer(workspace, "127.0.0.1", 0)
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	released := false
	release := func() {
		if !released {
			close(releaseComparing)
			released = true
		}
	}
	defer func() {
		release()
		_ = server.Close()
	}()

	select {
	case <-enteredComparing:
	case <-time.After(5 * time.Second):
		t.Fatal("initial refresh did not reach comparing")
	}
	closed := make(chan error, 1)
	go func() {
		closed <- server.Close()
	}()
	select {
	case err := <-closed:
		t.Fatalf("Close() returned before the active Refresh ended: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	if err := <-closed; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	waitForWorkspacePhase(t, workspace, PhaseCanceled)
}
