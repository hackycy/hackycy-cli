package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallReplacesOnlyApprovedLegacyHook(t *testing.T) {
	root := testRepository(t)
	controller := testController(t, root)
	state, err := controller.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned an error: %v", err)
	}
	legacy := []byte("#!/bin/sh\n\nif [ \"$SKIP_SIMPLE_GIT_HOOKS\" = \"1\" ]; then\n    echo \"[INFO] SKIP_SIMPLE_GIT_HOOKS is set to 1, skipping hook.\"\n    exit 0\nfi\n\nif [ -f \"$SIMPLE_GIT_HOOKS_RC\" ]; then\n    . \"$SIMPLE_GIT_HOOKS_RC\"\nfi\n\nb" + "un run lint")
	if err := os.WriteFile(filepath.Join(state.HooksPath, "pre-commit"), legacy, 0o755); err != nil {
		t.Fatalf("write legacy hook: %v", err)
	}
	controller.invoke = installFakeHook(controller)

	if err := controller.Install(context.Background()); err != nil {
		t.Fatalf("Install returned an error: %v", err)
	}
	installed, err := controller.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover installed hook: %v", err)
	}
	if installed.PreCommitKind != hookLefthook {
		t.Fatalf("pre-commit manager = %s, want %s", installed.PreCommitKind, hookLefthook)
	}
	if err := controller.Install(context.Background()); err != nil {
		t.Fatalf("repeated Install returned an error: %v", err)
	}
}

func TestInstallPreservesUnknownHookAndConfiguredPath(t *testing.T) {
	root := testRepository(t)
	controller := testController(t, root)
	state, err := controller.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned an error: %v", err)
	}
	path := filepath.Join(state.HooksPath, "pre-commit")
	custom := []byte("#!/bin/sh\necho custom\n")
	if err := os.WriteFile(path, custom, 0o755); err != nil {
		t.Fatalf("write custom hook: %v", err)
	}
	if err := controller.Install(context.Background()); err == nil {
		t.Fatal("Install accepted a custom hook")
	}
	contents, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(contents, custom) {
		t.Fatalf("custom hook changed: %q, %v", contents, err)
	}

	runGit(t, root, "config", "--local", "core.hooksPath", "custom-hooks")
	if err := controller.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "core.hooksPath") {
		t.Fatalf("Install did not refuse configured hooks path: %v", err)
	}
	if value := strings.TrimSpace(runGit(t, root, "config", "--local", "--get", "core.hooksPath")); value != "custom-hooks" {
		t.Fatalf("core.hooksPath = %q, want custom-hooks", value)
	}
}

func TestUninstallRemovesOnlyLefthookState(t *testing.T) {
	root := testRepository(t)
	controller := testController(t, root)
	state, err := controller.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned an error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(state.HooksPath, "pre-commit"), []byte("#!/bin/sh\n# LEFTHOOK\n"), 0o755); err != nil {
		t.Fatalf("write Lefthook hook: %v", err)
	}
	checksum := filepath.Join(state.CommonGitDir, "info", "lefthook.checksum")
	if err := os.MkdirAll(filepath.Dir(checksum), 0o755); err != nil {
		t.Fatalf("create checksum directory: %v", err)
	}
	if err := os.WriteFile(checksum, []byte("managed"), 0o644); err != nil {
		t.Fatalf("write checksum: %v", err)
	}

	if err := controller.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall returned an error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(state.HooksPath, "pre-commit")); !os.IsNotExist(err) {
		t.Fatalf("pre-commit remains after uninstall: %v", err)
	}
	if _, err := os.Lstat(checksum); !os.IsNotExist(err) {
		t.Fatalf("checksum remains after uninstall: %v", err)
	}
}

func TestInstallPreservesGlobalHooksPath(t *testing.T) {
	root := testRepository(t)
	globalConfig := filepath.Join(t.TempDir(), "global.gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	runGit(t, root, "config", "--global", "core.hooksPath", "global-hooks")
	controller := testController(t, root)

	if err := controller.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "core.hooksPath") {
		t.Fatalf("Install did not refuse a global hooks path: %v", err)
	}
	if value := strings.TrimSpace(runGit(t, root, "config", "--global", "--get", "core.hooksPath")); value != "global-hooks" {
		t.Fatalf("global core.hooksPath = %q, want global-hooks", value)
	}
}

func TestDiscoverUsesCommonHooksForLinkedWorktree(t *testing.T) {
	root := testRepository(t)
	runGit(t, root, "config", "user.email", "test@example.invalid")
	runGit(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, root, "add", "README")
	runGit(t, root, "commit", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, root, "worktree", "add", "-b", "linked", linked)

	controller := testController(t, linked)
	state, err := controller.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover linked worktree: %v", err)
	}
	resolvedLinked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatalf("resolve linked worktree: %v", err)
	}
	if state.Root != resolvedLinked || !strings.HasSuffix(state.CommonGitDir, ".git") || !strings.HasSuffix(state.HooksPath, filepath.Join(".git", "hooks")) {
		t.Fatalf("unexpected linked-worktree state: %#v", state)
	}
}

func testRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init")
	return root
}

func testController(t *testing.T, root string) *Controller {
	t.Helper()
	for _, path := range []string{"lefthook.yml", "lefthook.rc", filepath.Join("web", "node_modules"), filepath.Join("tools", "lefthook", "bin")} {
		full := filepath.Join(root, path)
		if filepath.Ext(path) == "" {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
		} else if err := os.WriteFile(full, []byte("placeholder\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	binaryName := "lefthook"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	if err := os.WriteFile(filepath.Join(root, "tools", "lefthook", "bin", binaryName), []byte("placeholder\n"), 0o755); err != nil {
		t.Fatalf("write Lefthook placeholder: %v", err)
	}
	controller, err := New(root, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	return controller
}

func installFakeHook(controller *Controller) invoker {
	return func(context.Context, string, string, ...string) (string, error) {
		state, err := controller.Discover(context.Background())
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(state.HooksPath, "pre-commit"), []byte("#!/bin/sh\n# LEFTHOOK\n"), 0o755); err != nil {
			return "", err
		}
		return "", nil
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
