package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
)

func TestRunReleaseResumesInterruptedPublishing(t *testing.T) {
	for _, failure := range []string{
		"push origin HEAD:main",
		"tag -a v0.0.70 -m chore: release v0.0.70",
		"push origin refs/tags/v0.0.70",
	} {
		t.Run(failure, func(t *testing.T) {
			root, remote := newRecoveryRepository(t)
			git := &interruptedReleaseGit{failure: failure}
			commands := &fakeCommands{}
			options := releaseOptions{Root: root, Git: git, Commands: commands, Prompt: &fakePrompt{selection: "next", confirmed: true}}
			if err := runRelease(context.Background(), options); err == nil || !strings.Contains(err.Error(), "SSL_ERROR_SYSCALL") {
				t.Fatalf("first release error = %v", err)
			}
			head := recoveryGit(t, root, "rev-parse", "HEAD")
			prompt := &fakePrompt{confirmed: true}
			options.Prompt = prompt
			if err := runRelease(context.Background(), options); err != nil {
				t.Fatalf("resume release: %v", err)
			}
			if prompt.opened {
				t.Fatal("resume asked for a new version")
			}
			if got := recoveryGit(t, root, "rev-parse", "HEAD"); got != head {
				t.Fatalf("resume created another commit: %s, want %s", got, head)
			}
			if got := recoveryGit(t, root, "show", "HEAD:"+versionFile); got != "0.0.70" {
				t.Fatalf("resume VERSION = %q", got)
			}
			if got := recoveryGit(t, remote, "cat-file", "-t", "refs/tags/v0.0.70"); got != "tag" {
				t.Fatalf("remote tag type = %q", got)
			}
			for _, ref := range []string{"refs/heads/main", "refs/tags/v0.0.70^{commit}"} {
				if got := recoveryGit(t, remote, "rev-parse", ref); got != head {
					t.Fatalf("remote %s = %s, want %s", ref, got, head)
				}
			}
			if len(commands.calls) != 2 {
				t.Fatalf("check calls = %v, want checks on both attempts", commands.calls)
			}
			plan, err := prepareRelease(context.Background(), options, &fakeReleaseWork{})
			if err != nil || plan.resume || plan.current.String() != "0.0.70" {
				t.Fatalf("published release should become the next baseline: plan=%+v, error=%v", plan, err)
			}
		})
	}
}

func TestRunReleaseResumeDryRunAndCancellationDoNotPublish(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "dry-run"}[dryRun], func(t *testing.T) {
			root, remote := newRecoveryRepository(t)
			git := &interruptedReleaseGit{failure: "push origin HEAD:main"}
			options := releaseOptions{Root: root, Git: git, Commands: &fakeCommands{}, Prompt: &fakePrompt{selection: "next", confirmed: true}}
			if err := runRelease(context.Background(), options); err == nil {
				t.Fatal("first release unexpectedly succeeded")
			}
			head := recoveryGit(t, root, "rev-parse", "HEAD")
			remoteHead := recoveryGit(t, remote, "rev-parse", "refs/heads/main")
			options.Prompt = &fakePrompt{confirmed: dryRun}
			options.DryRun = dryRun
			err := runRelease(context.Background(), options)
			if dryRun && err != nil || !dryRun && (err == nil || !strings.Contains(err.Error(), "release cancelled")) {
				t.Fatalf("resume error = %v", err)
			}
			if recoveryGit(t, root, "rev-parse", "HEAD") != head || recoveryGit(t, remote, "rev-parse", "refs/heads/main") != remoteHead {
				t.Fatal("dry run or cancellation changed release commits")
			}
			if got := recoveryGit(t, root, "tag", "--list", "v0.0.70"); got != "" {
				t.Fatalf("dry run or cancellation created tag %s", got)
			}
		})
	}
}

func TestRunReleaseBlocksUnsafeRecovery(t *testing.T) {
	for _, scenario := range []struct{ name, want string }{
		{"lightweight", "lightweight"},
		{"remote-conflict", "local and remote tag v0.0.70 differ"},
		{"wrong-tag-target", "local tag points to a different commit"},
		{"unrelated-head", "automatic recovery requires HEAD to be its release commit"},
		{"extra-release-files", "release commit must change only"},
		{"version-downgrade", "behind the highest stable tag"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root, remote := newRecoveryRepository(t)
			git := &interruptedReleaseGit{failure: "push origin HEAD:main"}
			commands := &fakeCommands{}
			options := releaseOptions{Root: root, Git: git, Commands: commands, Prompt: &fakePrompt{selection: "next", confirmed: true}}
			if err := runRelease(context.Background(), options); err == nil {
				t.Fatal("first release unexpectedly succeeded")
			}
			switch scenario.name {
			case "lightweight":
				recoveryGit(t, root, "tag", "v0.0.70")
			case "remote-conflict":
				recoveryGit(t, root, "tag", "-a", "v0.0.70", "-m", "release")
				recoveryGit(t, root, "push", "origin", "HEAD:refs/tags/v0.0.70")
			case "wrong-tag-target":
				recoveryGit(t, root, "tag", "-a", "v0.0.70", "HEAD^", "-m", "release")
			case "unrelated-head":
				recoveryGit(t, root, "commit", "--allow-empty", "-m", "docs: unrelated")
			case "extra-release-files":
				if err := os.WriteFile(filepath.Join(root, "extra"), []byte("extra\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				recoveryGit(t, root, "add", "extra")
				recoveryGit(t, root, "commit", "--amend", "--no-edit")
			case "version-downgrade":
				if err := writeVersionAtomic(root, "0.0.68"); err != nil {
					t.Fatal(err)
				}
				recoveryGit(t, root, "add", versionFile)
				recoveryGit(t, root, "commit", "--amend", "-m", "chore(release): v0.0.68")
			}
			head := recoveryGit(t, root, "rev-parse", "HEAD")
			remoteRefs := recoveryGit(t, remote, "show-ref")
			options.Prompt = &fakePrompt{confirmed: true}
			err := runRelease(context.Background(), options)
			if err == nil || !strings.Contains(err.Error(), scenario.want) {
				t.Fatalf("unsafe recovery error = %v, want %q", err, scenario.want)
			}
			if recoveryGit(t, root, "rev-parse", "HEAD") != head || recoveryGit(t, remote, "show-ref") != remoteRefs {
				t.Fatal("blocked recovery changed release refs")
			}
			if len(commands.calls) != 1 {
				t.Fatalf("blocked recovery ran checks: %v", commands.calls)
			}
		})
	}
}

type interruptedReleaseGit struct {
	failure string
	failed  bool
}

func (git *interruptedReleaseGit) Run(ctx context.Context, arguments []string) (gitprocess.Output, error) {
	if !git.failed && strings.Join(arguments[2:], " ") == git.failure {
		git.failed = true
		return gitprocess.Output{ExitCode: 128, Stderr: []byte("fatal: LibreSSL SSL_connect: SSL_ERROR_SYSCALL in connection to github.com:443")}, nil
	}
	return (&gitprocess.Runner{}).Run(ctx, arguments)
}

func newRecoveryRepository(t *testing.T) (root, remote string) {
	t.Helper()
	root = writeReleaseVersion(t)
	remote = filepath.Join(t.TempDir(), "origin.git")
	recoveryGit(t, root, "init", "--initial-branch=main")
	recoveryGit(t, root, "config", "user.name", "Release test")
	recoveryGit(t, root, "config", "user.email", "release-test@example.com")
	recoveryGit(t, root, "config", "core.hooksPath", filepath.Join(t.TempDir(), "hooks"))
	recoveryGit(t, root, "config", "commit.gpgSign", "false")
	recoveryGit(t, root, "config", "tag.gpgSign", "false")
	recoveryGit(t, root, "init", "--bare", "--initial-branch=main", remote)
	recoveryGit(t, root, "add", versionFile)
	recoveryGit(t, root, "commit", "-m", "chore(release): v0.0.69")
	recoveryGit(t, root, "tag", "-a", "v0.0.69", "-m", "release baseline")
	recoveryGit(t, root, "remote", "add", "origin", remote)
	recoveryGit(t, root, "push", "origin", "main", "refs/tags/v0.0.69")
	recoveryGit(t, root, "commit", "--allow-empty", "-m", "fix: repair")
	return root, remote
}

func recoveryGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	output, err := (&gitprocess.Runner{}).Run(context.Background(), append([]string{"-C", root}, arguments...))
	if err != nil || output.ExitCode != 0 {
		t.Fatalf("git %v: %v (exit %d)\n%s", arguments, err, output.ExitCode, output.Stderr)
	}
	return strings.TrimSpace(string(output.Stdout))
}
