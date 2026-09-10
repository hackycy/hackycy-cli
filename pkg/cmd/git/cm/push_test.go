package cm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPushCommitUsesTheCurrentBranchAndLegacyRemoteArguments(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		remote string
		want   []string
	}{
		{name: "default remote", want: []string{"-C", "/repo", "push", "-u", "origin", "main"}},
		{name: "custom remote", remote: "upstream", want: []string{"-C", "/repo", "push", "-u", "upstream", "main"}},
		{name: "option like remote remains a Git argument", remote: "-f", want: []string{"-C", "/repo", "push", "-u", "-f", "main"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &scriptedGitRunner{responses: map[string]GitOutput{
				"-C /repo branch --show-current": {Stdout: []byte("main\n")},
			}}
			if err := PushCommit(context.Background(), runner, "/repo", testCase.remote); err != nil {
				t.Fatalf("PushCommit() error = %v", err)
			}
			runner.requireCalls(t,
				[]string{"-C", "/repo", "branch", "--show-current"},
				testCase.want,
			)
		})
	}
}

func TestPushCommitRejectsDetachedHeadAndPropagatesGitFailures(t *testing.T) {
	t.Run("detached head", func(t *testing.T) {
		runner := &scriptedGitRunner{responses: map[string]GitOutput{
			"-C /repo branch --show-current": {},
		}}
		err := PushCommit(context.Background(), runner, "/repo", "origin")
		if err == nil || err.Error() != "Cannot push from detached HEAD. Check out a branch first." {
			t.Fatalf("PushCommit() error = %v", err)
		}
		runner.requireCalls(t, []string{"-C", "/repo", "branch", "--show-current"})
	})
	t.Run("branch query", func(t *testing.T) {
		runner := &scriptedGitRunner{responses: map[string]GitOutput{
			"-C /repo branch --show-current": {Stderr: []byte("branch lookup failed\n"), ExitCode: 1},
		}}
		err := PushCommit(context.Background(), runner, "/repo", "origin")
		if err == nil || err.Error() != "branch lookup failed" {
			t.Fatalf("PushCommit() error = %v", err)
		}
	})
	t.Run("push", func(t *testing.T) {
		failure := errors.New("Git transport unavailable")
		runner := &scriptedGitRunner{
			responses: map[string]GitOutput{"-C /repo branch --show-current": {Stdout: []byte("main\n")}},
			errors:    map[string]error{"-C /repo push -u origin main": failure},
		}
		err := PushCommit(context.Background(), runner, "/repo", "origin")
		if !errors.Is(err, failure) {
			t.Fatalf("PushCommit() error = %v", err)
		}
	})
}

func TestPushCommitPushesToADisposableLocalRemoteAndSetsUpstream(t *testing.T) {
	root := newCommitRepository(t)
	remote := t.TempDir()
	runCommitGit(t, remote, "init", "--bare", "-q")
	runCommitGit(t, root, "remote", "add", "origin", remote)
	runCommitGit(t, root, "add", "value.go")
	runner := rootGitRunner{root: root}
	snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeStaged)
	if err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}
	if err := CommitSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, CommitRequest{RepositoryRoot: root, Scope: ScopeStaged, SnapshotID: snapshot.ID, Message: "feat(cm): push locally"}); err != nil {
		t.Fatalf("CommitSnapshot() error = %v", err)
	}
	if err := PushCommit(context.Background(), runner, root, "origin"); err != nil {
		t.Fatalf("PushCommit() error = %v", err)
	}
	branch := strings.TrimSpace(runCommitGit(t, root, "branch", "--show-current"))
	if branch == "" {
		t.Fatal("test repository unexpectedly detached")
	}
	if got := strings.TrimSpace(runCommitGit(t, remote, "rev-parse", "refs/heads/"+branch)); got == "" {
		t.Fatal("local remote did not receive the branch")
	}
	if got := strings.TrimSpace(runCommitGit(t, root, "config", "--get", "branch."+branch+".remote")); got != "origin" {
		t.Fatalf("upstream remote = %q, want origin", got)
	}
}
