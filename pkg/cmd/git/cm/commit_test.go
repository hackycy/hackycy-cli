package cm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCommitArgumentsPreserveTheLegacySubjectAndBodySplitting(t *testing.T) {
	for _, testCase := range []struct {
		message string
		want    []string
	}{
		{
			message: "feat(cm): subject  ",
			want:    []string{"-C", "/repo", "commit", "-m", "feat(cm): subject"},
		},
		{
			message: "fix(cm): subject  \n\nKeep the body.  \n",
			want:    []string{"-C", "/repo", "commit", "-m", "fix(cm): subject", "-m", "Keep the body."},
		},
	} {
		if got := commitArguments("/repo", testCase.message); !reflect.DeepEqual(got, testCase.want) {
			t.Fatalf("commitArguments(%q) = %#v, want %#v", testCase.message, got, testCase.want)
		}
	}
}

func TestCommitSnapshotRechecksBeforeInvokingGitCommit(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	runner := newModuleRunner(root, "M  value.go\x00")
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--no-ext-diff", "--find-renames", "--unified=0"})] = GitOutput{Stdout: []byte("diff --git a/value.go b/value.go\n--- a/value.go\n+++ b/value.go\n@@ -1 +1 @@\n-old\n+new\n")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--numstat", "-z", "--find-renames"})] = GitOutput{Stdout: []byte("1\t1\tvalue.go\x00")}
	snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeStaged)
	if err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}

	err = CommitSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, CommitRequest{
		RepositoryRoot: root,
		Scope:          ScopeStaged,
		SnapshotID:     snapshot.ID,
		Message:        "feat(cm): commit the change\n\nKeep details.",
	})
	if err != nil {
		t.Fatalf("CommitSnapshot() error = %v", err)
	}
	requireSnapshotCalls(t, runner, []string{"-C", root, "commit", "-m", "feat(cm): commit the change", "-m", "Keep details."})
}

func TestCommitSnapshotRefusesAChangedScopeBeforeGitCommit(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	runner := newModuleRunner(root, "M  value.go\x00")
	cachedPatch := []string{"-C", root, "diff", "--cached", "--no-ext-diff", "--find-renames", "--unified=0"}
	runner.responses[snapshotRunnerKey(cachedPatch)] = GitOutput{Stdout: []byte("diff --git a/value.go b/value.go\n--- a/value.go\n+++ b/value.go\n@@ -1 +1 @@\n-old\n+first\n")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--numstat", "-z", "--find-renames"})] = GitOutput{Stdout: []byte("1\t1\tvalue.go\x00")}
	snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeStaged)
	if err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}
	runner.responses[snapshotRunnerKey(cachedPatch)] = GitOutput{Stdout: []byte("diff --git a/value.go b/value.go\n--- a/value.go\n+++ b/value.go\n@@ -1 +1 @@\n-old\n+second\n")}

	err = CommitSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, CommitRequest{RepositoryRoot: root, Scope: ScopeStaged, SnapshotID: snapshot.ID, Message: "feat(cm): stale"})
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Code != ErrorStaleScope {
		t.Fatalf("CommitSnapshot() error = %#v", err)
	}
	runner.requireNoCall(t, []string{"-C", root, "commit", "-m", "feat(cm): stale"})
}

func TestCommitSnapshotPreservesGitHookFailures(t *testing.T) {
	root := t.TempDir()
	writeStageFile(t, root, "value.go")
	runner := newModuleRunner(root, "M  value.go\x00")
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--no-ext-diff", "--find-renames", "--unified=0"})] = GitOutput{Stdout: []byte("diff --git a/value.go b/value.go\n--- a/value.go\n+++ b/value.go\n@@ -1 +1 @@\n-old\n+new\n")}
	runner.responses[snapshotRunnerKey([]string{"-C", root, "diff", "--cached", "--numstat", "-z", "--find-renames"})] = GitOutput{Stdout: []byte("1\t1\tvalue.go\x00")}
	snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeStaged)
	if err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}
	commit := []string{"-C", root, "commit", "-m", "feat(cm): hook failure"}
	runner.responses[snapshotRunnerKey(commit)] = GitOutput{Stderr: []byte("pre-commit rejected this change\n"), ExitCode: 1}

	err = CommitSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, CommitRequest{RepositoryRoot: root, Scope: ScopeStaged, SnapshotID: snapshot.ID, Message: "feat(cm): hook failure"})
	if err == nil || err.Error() != "pre-commit rejected this change" {
		t.Fatalf("CommitSnapshot() error = %v", err)
	}
}

func TestCommitSnapshotRequiresItsRepositoryAndSnapshotIdentity(t *testing.T) {
	runner := newModuleRunner(t.TempDir(), "")
	for _, request := range []CommitRequest{
		{SnapshotID: "snapshot"},
		{RepositoryRoot: "/repo"},
	} {
		if err := CommitSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, request); err == nil {
			t.Fatalf("CommitSnapshot(%#v) error = nil", request)
		}
	}
}

func TestCommitSnapshotRunsTheRepositoryHookAndPreservesGitCommitSemantics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook fixture requires a Unix host")
	}
	t.Run("failing hook blocks the commit", func(t *testing.T) {
		root := newCommitRepository(t)
		runCommitGit(t, root, "add", "value.go")
		if err := os.WriteFile(filepath.Join(root, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\nprintf hook-ran > hook-ran\necho hook rejected >&2\nexit 1\n"), 0o700); err != nil {
			t.Fatalf("write hook: %v", err)
		}
		runner := rootGitRunner{root: root}
		snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeStaged)
		if err != nil {
			t.Fatalf("CaptureSnapshot() error = %v", err)
		}

		err = CommitSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, CommitRequest{RepositoryRoot: root, Scope: ScopeStaged, SnapshotID: snapshot.ID, Message: "feat(cm): reject with hook"})
		if err == nil || !strings.Contains(err.Error(), "hook rejected") {
			t.Fatalf("CommitSnapshot() error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "hook-ran")); err != nil {
			t.Fatalf("hook did not run: %v", err)
		}
		if got := strings.TrimSpace(runCommitGit(t, root, "log", "-1", "--format=%s")); got != "baseline" {
			t.Fatalf("HEAD subject = %q, want baseline", got)
		}
	})
	t.Run("successful hook retains the subject and body", func(t *testing.T) {
		root := newCommitRepository(t)
		runCommitGit(t, root, "add", "value.go")
		if err := os.WriteFile(filepath.Join(root, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatalf("write hook: %v", err)
		}
		runner := rootGitRunner{root: root}
		snapshot, err := CaptureSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, ScopeStaged)
		if err != nil {
			t.Fatalf("CaptureSnapshot() error = %v", err)
		}

		if err := CommitSnapshot(context.Background(), runner, diskSnapshotFileSystem{}, CommitRequest{RepositoryRoot: root, Scope: ScopeStaged, SnapshotID: snapshot.ID, Message: "feat(cm): commit with hook\n\nKeep the details."}); err != nil {
			t.Fatalf("CommitSnapshot() error = %v", err)
		}
		if got := strings.TrimSpace(runCommitGit(t, root, "log", "-1", "--format=%s")); got != "feat(cm): commit with hook" {
			t.Fatalf("HEAD subject = %q", got)
		}
		if got := strings.TrimSpace(runCommitGit(t, root, "log", "-1", "--format=%b")); got != "Keep the details." {
			t.Fatalf("HEAD body = %q", got)
		}
	})
}

func newCommitRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runCommitGit(t, root, "init", "-q")
	runCommitGit(t, root, "config", "user.name", "Git CM Test")
	runCommitGit(t, root, "config", "user.email", "git-cm@example.test")
	if err := os.WriteFile(filepath.Join(root, "value.go"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	runCommitGit(t, root, "add", "value.go")
	runCommitGit(t, root, "commit", "-qm", "baseline")
	if err := os.WriteFile(filepath.Join(root, "value.go"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("write changed value: %v", err)
	}
	return root
}

func runCommitGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %q failed: %v\n%s", arguments, err, output)
	}
	return string(output)
}

type rootGitRunner struct {
	root string
}

func (runner rootGitRunner) Run(ctx context.Context, arguments []string) (GitOutput, error) {
	resolved := append([]string(nil), arguments...)
	if len(resolved) < 2 || resolved[0] != "-C" {
		resolved = append([]string{"-C", runner.root}, resolved...)
	}
	command := exec.CommandContext(ctx, "git", resolved...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	output := GitOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return output, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		output.ExitCode = exitError.ExitCode()
		return output, nil
	}
	return output, err
}
