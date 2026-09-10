package diff

import (
	"context"
	"path/filepath"
	"testing"
)

func TestComparisonSessionMapsInputToFixedWorkspaceAndPublicBinding(t *testing.T) {
	baseline, target := comparisonRoots(t)
	resolvedBaseline, err := filepath.EvalSymlinks(baseline)
	if err != nil {
		t.Fatalf("resolve baseline: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}

	session, err := startComparison(Input{
		BaselineDirectory: filepath.Join(baseline, "."),
		TargetDirectory:   target,
		Port:              0,
		Public:            true,
		Exclusions:        []string{"ignored/**"},
		NoGitIgnore:       true,
	})
	if err != nil {
		t.Fatalf("startComparison() error = %v", err)
	}
	defer func() {
		if err := session.server.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	if session.bindingAddress != "0.0.0.0" || session.workspace.baseline.path != resolvedBaseline || session.workspace.target.path != resolvedTarget || session.workspace.gitIgnore {
		t.Fatalf("session = %#v", session)
	}
	if session.server.Port() == 0 {
		t.Fatal("public session did not receive a listener port")
	}
}

func TestComparisonSessionTreatsContextCancellationAsNormalLifecycleExit(t *testing.T) {
	baseline, target := comparisonRoots(t)
	session, err := startComparison(Input{BaselineDirectory: baseline, TargetDirectory: target, Port: 0})
	if err != nil {
		t.Fatalf("startComparison() error = %v", err)
	}
	context, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.wait(context); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	if err := session.server.Wait(); err != nil {
		t.Fatalf("server Wait() error = %v", err)
	}
}

func TestComparisonSessionRejectsInvalidRootsBeforeServing(t *testing.T) {
	baseline, target := comparisonRoots(t)
	file := filepath.Join(target, "not-a-directory")
	writeComparisonFile(t, target, "not-a-directory", "file")

	if _, err := startComparison(Input{BaselineDirectory: baseline, TargetDirectory: file, Port: 0}); err == nil || err.Error() != "Target Directory must be a directory" {
		t.Fatalf("startComparison() error = %v", err)
	}
}
