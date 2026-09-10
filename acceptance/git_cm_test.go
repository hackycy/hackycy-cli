//go:build acceptance

package acceptance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestGitCMStandaloneBinaryExposesThePublicLeaf(t *testing.T) {
	binary := standaloneBinaryOutputPath(filepath.Join(t.TempDir(), "ycy"))
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	build.Dir = repositoryRoot(t)
	build.Env = environmentWith(map[string]string{
		"CGO_ENABLED": "0",
		"GOTOOLCHAIN": "go1.26.7",
		"GOWORK":      "off",
	})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}
	command := exec.Command(resolveStandaloneBinary(binary), "git", "cm", "--help")
	command.Dir = t.TempDir()
	command.Env = environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git cm --help: %v\n%s", err, output)
	}
	for _, expected := range []string{"Generate an Angular-style commit message", "--stage-all", "--stage-push", "--dry-run"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("git cm --help omitted %q:\n%s", expected, output)
		}
	}
}

func TestGitCMStandaloneBinaryGeneratesDryRunFromAllUncommittedChanges(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "local change\n")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	providerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		providerCalls++
		if request.Method != http.MethodPost || request.URL.Path != "/chat/completions" {
			t.Fatalf("provider request = %s %s", request.Method, request.URL.Path)
		}
		if got, want := request.Header.Get("Authorization"), "Bearer fixture-api-key"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Messages  []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		if body.Model != "fixture-model" || body.MaxTokens != 4096 || len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "README.md") {
			t.Fatalf("provider body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"choices":[{"message":{"content":"feat(cm): generate local message"}}],"usage":{"prompt_tokens":21,"completion_tokens":7,"total_tokens":28}}`)
	}))
	defer server.Close()

	output, err := runGitCMStandalone(binary, repository, gitCMProviderEnvironment(t, server.URL), "", "git", "cm", "--dry-run")
	if err != nil {
		t.Fatalf("git cm --dry-run: %v\n%s", err, output)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
	}
	text := string(output)
	for _, expected := range []string{"feat(cm): generate local message", "Profile: env (fixture-model)", "Provider tokens: 21 prompt / 7 completion / 28 total"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "fixture-api-key") {
		t.Fatalf("output exposed API key: %q", text)
	}
	if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("dry run changed HEAD from %q to %q", beforeHead, afterHead)
	}
	if output := gitCMCommand(t, repository, "diff", "--cached", "--quiet"); output != "" {
		t.Fatalf("dry run changed index: %q", output)
	}
	if status := gitCMOutput(t, repository, "status", "--short"); status != "?? README.md\n" {
		t.Fatalf("dry run status = %q", status)
	}
}

func TestGitCMStandaloneBinaryRedactsProviderFailureAndPresentsSafeProfile(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "provider failure fixture\n")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	providerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		providerCalls++
		if request.Method != http.MethodPost || request.URL.Path != "/chat/completions" {
			t.Fatalf("provider request = %s %s", request.Method, request.URL.Path)
		}
		if got, want := request.Header.Get("Authorization"), "Bearer fixture-api-key"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(response, `{"error":"fixture-api-key rejected"}`)
	}))
	defer server.Close()

	output, err := runGitCMStandalone(binary, repository, gitCMProviderEnvironment(t, server.URL), "", "git", "cm", "--dry-run")
	if exitCode(err) != 1 {
		t.Fatalf("git cm --dry-run error = %v, want exit 1\n%s", err, output)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
	}
	text := string(output)
	for _, expected := range []string{"Provider: env", "Base URL: " + server.URL, "Model: fixture-model", "[REDACTED]", "error:"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("provider failure output omitted %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "fixture-api-key") {
		t.Fatalf("provider failure output exposed API key: %q", text)
	}
	if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("provider failure changed HEAD from %q to %q", beforeHead, afterHead)
	}
	if output := gitCMCommand(t, repository, "diff", "--cached", "--quiet"); output != "" {
		t.Fatalf("provider failure changed index: %q", output)
	}
	if status := gitCMOutput(t, repository, "status", "--short"); status != "?? README.md\n" {
		t.Fatalf("provider failure status = %q", status)
	}
}

func TestGitCMStandaloneBinaryCreatesAStagedCommitThroughTheNormalHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook fixture requires a Unix host")
	}
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "staged change\n")
	runGitCM(t, repository, "add", "README.md")
	writeGitCMHook(t, repository, "pre-commit", "#!/bin/sh\nprintf hook-ran > hook-ran\nexit 0\n")
	server, provider := newGitCMMessageProvider(t, "feat(cm): commit through hook")
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "\n", "git", "cm", "--staged")
	if err != nil {
		t.Fatalf("git cm --staged: %v\n%s", err, output)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	text := string(output)
	for _, expected := range []string{"feat(cm): commit through hook", "Create this commit? [Y/n]:", "Commit created"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, "hook-ran")); err != nil {
		t.Fatalf("successful pre-commit hook did not run: %v", err)
	}
	if got := strings.TrimSpace(gitCMOutput(t, repository, "log", "-1", "--format=%s")); got != "feat(cm): commit through hook" {
		t.Fatalf("HEAD subject = %q", got)
	}
	if output := gitCMCommand(t, repository, "diff", "--cached", "--quiet"); output != "" {
		t.Fatalf("commit left cached changes: %q", output)
	}
}

func TestGitCMStandaloneBinaryHandlesNoChangesAndStagePromptTermination(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	t.Run("no changes", func(t *testing.T) {
		repository := newGitCMRepository(t)
		output, err := runGitCMStandalone(binary, repository, gitCMNoProviderEnvironment(t), "", "git", "cm")
		if err != nil {
			t.Fatalf("git cm: %v\n%s", err, output)
		}
		if got, want := string(output), "No uncommitted changes.\n"; got != want {
			t.Fatalf("output = %q, want %q", got, want)
		}
	})
	for _, testCase := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "cancelled selection", input: "cancel\n", want: "Cancelled"},
		{name: "empty selection", input: "none\n", want: "Nothing selected."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := newGitCMRepository(t)
			writeGitCMFile(t, filepath.Join(repository, "README.md"), "untracked change\n")
			beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
			output, err := runGitCMPlainStandalone(t, binary, repository, gitCMNoProviderEnvironment(t), testCase.input, "git", "cm", "--stage")
			if err != nil {
				t.Fatalf("git cm --stage: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "Select files to stage") || !strings.Contains(string(output), testCase.want) {
				t.Fatalf("output = %q, want selection prompt and %q", output, testCase.want)
			}
			if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
				t.Fatalf("termination changed HEAD from %q to %q", beforeHead, afterHead)
			}
			if output := gitCMCommand(t, repository, "diff", "--cached", "--quiet"); output != "" {
				t.Fatalf("termination changed index: %q", output)
			}
			if status := gitCMOutput(t, repository, "status", "--short"); status != "?? README.md\n" {
				t.Fatalf("termination status = %q", status)
			}
		})
	}
}

func TestGitCMStandaloneBinaryStagesOnlyTheSelectedTrackedChange(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "a.txt"), "before a\n")
	writeGitCMFile(t, filepath.Join(repository, "b.txt"), "before b\n")
	runGitCM(t, repository, "add", "a.txt", "b.txt")
	runGitCM(t, repository, "commit", "-m", "chore: add selection fixtures")
	writeGitCMFile(t, filepath.Join(repository, "a.txt"), "after a\n")
	writeGitCMFile(t, filepath.Join(repository, "b.txt"), "after b\n")
	server, provider := newGitCMMessageProvider(t, "feat(cm): commit selected change")
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "2\n\n", "git", "cm", "--stage")
	if err != nil {
		t.Fatalf("git cm --stage: %v\n%s", err, output)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	text := string(output)
	for _, expected := range []string{"1) M a.txt", "2) M b.txt", "feat(cm): commit selected change", "Commit created"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	if got := strings.TrimSpace(gitCMOutput(t, repository, "show", "--format=", "--name-only", "HEAD")); got != "b.txt" {
		t.Fatalf("committed paths = %q, want b.txt", got)
	}
	if got := gitCMOutput(t, repository, "diff", "--name-only"); got != "a.txt\n" {
		t.Fatalf("unstaged paths = %q, want a.txt", got)
	}
	if output := gitCMCommand(t, repository, "diff", "--cached", "--quiet"); output != "" {
		t.Fatalf("selected commit left cached changes: %q", output)
	}
}

func TestGitCMStandaloneBinaryStagesAllChangesBeforeCommitting(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "package.json"), "{\"name\":\"after\"}\n")
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "untracked addition\n")
	server, provider := newGitCMMessageProvider(t, "feat(cm): commit every change")
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "\n", "git", "cm", "--stage-all")
	if err != nil {
		t.Fatalf("git cm --stage-all: %v\n%s", err, output)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	text := string(output)
	for _, expected := range []string{"feat(cm): commit every change", "Create this commit? [Y/n]:", "Commit created"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "Select files to stage") {
		t.Fatalf("stage-all unexpectedly prompted for selection:\n%s", text)
	}
	committed := strings.Fields(gitCMOutput(t, repository, "show", "--format=", "--name-only", "HEAD"))
	if len(committed) != 2 || !containsGitCMPath(committed, "README.md") || !containsGitCMPath(committed, "package.json") {
		t.Fatalf("committed paths = %#v, want README.md and package.json", committed)
	}
	if output := gitCMCommand(t, repository, "diff", "--cached", "--quiet"); output != "" {
		t.Fatalf("stage-all commit left cached changes: %q", output)
	}
	if status := gitCMOutput(t, repository, "status", "--short"); status != "" {
		t.Fatalf("stage-all commit status = %q", status)
	}
}

func TestGitCMStandaloneBinaryStageAllDryRunUsesAllUncommittedWithoutStaging(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "baseline readme\n")
	runGitCM(t, repository, "add", "README.md")
	runGitCM(t, repository, "commit", "-m", "chore: add readme fixture")
	writeGitCMFile(t, filepath.Join(repository, "package.json"), "{\"name\":\"staged\"}\n")
	runGitCM(t, repository, "add", "package.json")
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "unstaged readme\n")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	server, provider := newGitCMMessageProvider(t, "feat(cm): preview all changes")
	defer server.Close()

	output, err := runGitCMStandalone(binary, repository, gitCMProviderEnvironment(t, server.URL), "", "git", "cm", "--stage-all", "--dry-run")
	if err != nil {
		t.Fatalf("git cm --stage-all --dry-run: %v\n%s", err, output)
	}
	if provider.calls != 1 || len(provider.bodies) != 1 {
		t.Fatalf("provider = %#v, want one request", provider)
	}
	if !strings.Contains(provider.bodies[0], "package.json") || !strings.Contains(provider.bodies[0], "README.md") {
		t.Fatalf("provider evidence omitted all-uncommitted paths: %s", provider.bodies[0])
	}
	text := string(output)
	if !strings.Contains(text, "feat(cm): preview all changes") || strings.Contains(text, "Create this commit?") {
		t.Fatalf("dry-run output = %q", text)
	}
	if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("dry run changed HEAD from %q to %q", beforeHead, afterHead)
	}
	if cached := gitCMOutput(t, repository, "diff", "--cached", "--name-only"); cached != "package.json\n" {
		t.Fatalf("cached paths = %q, want package.json unchanged", cached)
	}
	if worktree := gitCMOutput(t, repository, "diff", "--name-only"); worktree != "README.md\n" {
		t.Fatalf("worktree paths = %q, want README.md unchanged", worktree)
	}
}

func TestGitCMStandaloneBinaryKeepsTheIndexWhenCommitConfirmationIsDeclined(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "staged but not committed\n")
	runGitCM(t, repository, "add", "README.md")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	server, provider := newGitCMMessageProvider(t, "feat(cm): decline commit")
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "no\n", "git", "cm", "--staged")
	if err != nil {
		t.Fatalf("git cm --staged with declined confirmation: %v\n%s", err, output)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	text := string(output)
	for _, expected := range []string{"feat(cm): decline commit", "Create this commit? [Y/n]:", "Cancelled"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("declined confirmation changed HEAD from %q to %q", beforeHead, afterHead)
	}
	if cached := gitCMOutput(t, repository, "diff", "--cached", "--name-only"); cached != "README.md\n" {
		t.Fatalf("declined confirmation index = %q, want README.md", cached)
	}
}

func TestGitCMStandaloneBinaryPropagatesAFailingPreCommitHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell hook fixture requires a Unix host")
	}
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "staged hook failure\n")
	runGitCM(t, repository, "add", "README.md")
	writeGitCMHook(t, repository, "pre-commit", "#!/bin/sh\nprintf hook-ran > hook-ran\necho hook rejected >&2\nexit 1\n")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	server, provider := newGitCMMessageProvider(t, "feat(cm): reject hook")
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "\n", "git", "cm", "--staged")
	if exitCode(err) != 1 {
		t.Fatalf("git cm --staged error = %v, want exit 1\n%s", err, output)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	text := string(output)
	for _, expected := range []string{"feat(cm): reject hook", "Create this commit? [Y/n]:", "hook rejected"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, "hook-ran")); err != nil {
		t.Fatalf("failing pre-commit hook did not run: %v", err)
	}
	if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("failing hook changed HEAD from %q to %q", beforeHead, afterHead)
	}
	if cached := gitCMOutput(t, repository, "diff", "--cached", "--name-only"); cached != "README.md\n" {
		t.Fatalf("failing hook index = %q, want README.md", cached)
	}
}

func TestGitCMStandaloneBinaryRejectsAStaleSnapshotBeforeCommit(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "first snapshot\n")
	runGitCM(t, repository, "add", "README.md")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	mutated := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/chat/completions" {
			mutated <- fmt.Errorf("provider request = %s %s", request.Method, request.URL.Path)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := mutateGitCMStagedFile(repository, "README.md", "second snapshot\n"); err != nil {
			mutated <- err
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		mutated <- nil
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"choices":[{"message":{"content":"feat(cm): stale snapshot"}}]}`)
	}))
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "\n", "git", "cm", "--staged")
	if exitCode(err) != 1 {
		t.Fatalf("git cm --staged stale snapshot error = %v, want exit 1\n%s", err, output)
	}
	select {
	case err := <-mutated:
		if err != nil {
			t.Fatalf("mutate staged fixture: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("local provider did not mutate the staged fixture")
	}
	text := string(output)
	for _, expected := range []string{"feat(cm): stale snapshot", "Create this commit? [Y/n]:", "Git changes changed after the commit message was generated"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("stale snapshot changed HEAD from %q to %q", beforeHead, afterHead)
	}
	if cached := gitCMOutput(t, repository, "diff", "--cached", "--name-only"); cached != "README.md\n" {
		t.Fatalf("stale snapshot index = %q, want README.md", cached)
	}
	contents, readErr := os.ReadFile(filepath.Join(repository, "README.md"))
	if readErr != nil || string(contents) != "second snapshot\n" {
		t.Fatalf("stale snapshot contents = %q, %v", contents, readErr)
	}
}

func TestGitCMStandaloneBinaryPushesAStagedCommitToTheDefaultLocalRemote(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	remote := newGitCMBareRemote(t)
	runGitCM(t, repository, "remote", "add", "origin", remote)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "push this change\n")
	runGitCM(t, repository, "add", "README.md")
	server, provider := newGitCMMessageProvider(t, "feat(cm): push local commit")
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "\n", "git", "cm", "--staged", "--push")
	if err != nil {
		t.Fatalf("git cm --staged --push: %v\n%s", err, output)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	text := string(output)
	for _, expected := range []string{"feat(cm): push local commit", "Commit created and pushed"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	branch := strings.TrimSpace(gitCMOutput(t, repository, "branch", "--show-current"))
	if branch == "" {
		t.Fatal("push fixture has no current branch")
	}
	if upstream := strings.TrimSpace(gitCMOutput(t, repository, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")); upstream != "origin/"+branch {
		t.Fatalf("upstream = %q, want origin/%s", upstream, branch)
	}
	localHead := strings.TrimSpace(gitCMOutput(t, repository, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(gitCMOutput(t, remote, "rev-parse", "refs/heads/"+branch))
	if remoteHead != localHead {
		t.Fatalf("remote head = %q, local head = %q", remoteHead, localHead)
	}
}

func TestGitCMStandaloneBinaryReturnsAPartialResultWhenTheLocalPushFails(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	runGitCM(t, repository, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "commit before failed push\n")
	runGitCM(t, repository, "add", "README.md")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	server, provider := newGitCMMessageProvider(t, "feat(cm): retain commit on push failure")
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "\n", "git", "cm", "--staged", "--push")
	if exitCode(err) != 1 {
		t.Fatalf("git cm --staged --push error = %v, want exit 1\n%s", err, output)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	text := string(output)
	if !strings.Contains(text, "feat(cm): retain commit on push failure") || !strings.Contains(text, "error:") || strings.Contains(text, "Commit created and pushed") {
		t.Fatalf("push failure output = %q", text)
	}
	afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	if afterHead == beforeHead {
		t.Fatalf("push failure did not retain the local commit: HEAD = %q", afterHead)
	}
	if subject := strings.TrimSpace(gitCMOutput(t, repository, "log", "-1", "--format=%s")); subject != "feat(cm): retain commit on push failure" {
		t.Fatalf("partial-result subject = %q", subject)
	}
	if output := gitCMCommand(t, repository, "diff", "--cached", "--quiet"); output != "" {
		t.Fatalf("partial-result commit left cached changes: %q", output)
	}
}

func TestGitCMStandaloneBinaryGeneratesFromTheDefaultAllUncommittedScope(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	writeGitCMFile(t, filepath.Join(repository, "README.md"), "default generation change\n")
	beforeHead := gitCMOutput(t, repository, "rev-parse", "HEAD")
	server, provider := newGitCMMessageProvider(t, "feat(cm): use default scope")
	defer server.Close()

	output, err := runGitCMStandalone(binary, repository, gitCMProviderEnvironment(t, server.URL), "", "git", "cm")
	if err != nil {
		t.Fatalf("git cm: %v\n%s", err, output)
	}
	if provider.calls != 1 || len(provider.bodies) != 1 || !strings.Contains(provider.bodies[0], "README.md") {
		t.Fatalf("provider = %#v, want one all-uncommitted request", provider)
	}
	text := string(output)
	if !strings.Contains(text, "feat(cm): use default scope") || strings.Contains(text, "Create this commit?") {
		t.Fatalf("default generation output = %q", text)
	}
	if afterHead := gitCMOutput(t, repository, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("default generation changed HEAD from %q to %q", beforeHead, afterHead)
	}
	if output := gitCMCommand(t, repository, "diff", "--cached", "--quiet"); output != "" {
		t.Fatalf("default generation changed index: %q", output)
	}
	if status := gitCMOutput(t, repository, "status", "--short"); status != "?? README.md\n" {
		t.Fatalf("default generation status = %q", status)
	}
}

func TestGitCMStandaloneBinaryStagePushSelectsCommitsAndPushesToTheDefaultLocalRemote(t *testing.T) {
	binary := buildGitCMStandaloneBinary(t)
	repository := newGitCMRepository(t)
	remote := newGitCMBareRemote(t)
	runGitCM(t, repository, "remote", "add", "origin", remote)
	writeGitCMFile(t, filepath.Join(repository, "a.txt"), "before a\n")
	writeGitCMFile(t, filepath.Join(repository, "b.txt"), "before b\n")
	runGitCM(t, repository, "add", "a.txt", "b.txt")
	runGitCM(t, repository, "commit", "-m", "chore: add stage-push fixtures")
	writeGitCMFile(t, filepath.Join(repository, "a.txt"), "after a\n")
	writeGitCMFile(t, filepath.Join(repository, "b.txt"), "after b\n")
	server, provider := newGitCMMessageProvider(t, "feat(cm): stage and push selected change")
	defer server.Close()

	output, err := runGitCMPlainStandalone(t, binary, repository, gitCMProviderEnvironment(t, server.URL), "2\n\n", "git", "cm", "--stage-push")
	if err != nil {
		t.Fatalf("git cm --stage-push: %v\n%s", err, output)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	text := string(output)
	for _, expected := range []string{"1) M a.txt", "2) M b.txt", "feat(cm): stage and push selected change", "Commit created and pushed"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output omitted %q:\n%s", expected, text)
		}
	}
	if committed := strings.TrimSpace(gitCMOutput(t, repository, "show", "--format=", "--name-only", "HEAD")); committed != "b.txt" {
		t.Fatalf("stage-push committed paths = %q, want b.txt", committed)
	}
	if worktree := gitCMOutput(t, repository, "diff", "--name-only"); worktree != "a.txt\n" {
		t.Fatalf("stage-push worktree paths = %q, want a.txt", worktree)
	}
	branch := strings.TrimSpace(gitCMOutput(t, repository, "branch", "--show-current"))
	localHead := strings.TrimSpace(gitCMOutput(t, repository, "rev-parse", "HEAD"))
	if remoteHead := strings.TrimSpace(gitCMOutput(t, remote, "rev-parse", "refs/heads/"+branch)); remoteHead != localHead {
		t.Fatalf("stage-push remote head = %q, local head = %q", remoteHead, localHead)
	}
}

func buildGitCMStandaloneBinary(t *testing.T) string {
	t.Helper()
	binary := standaloneBinaryOutputPath(filepath.Join(t.TempDir(), "ycy"))
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	build.Dir = repositoryRoot(t)
	build.Env = environmentWith(map[string]string{
		"CGO_ENABLED": "0",
		"GOTOOLCHAIN": "go1.26.7",
		"GOWORK":      "off",
	})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}
	return binary
}

func gitCMProviderEnvironment(t *testing.T, baseURL string) []string {
	t.Helper()
	return environmentWith(map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"HOME":                t.TempDir(),
		"USERPROFILE":         "",
		"YCY_CM_BASE_URL":     baseURL,
		"YCY_CM_MODEL":        "fixture-model",
		"YCY_CM_API_KEY":      "fixture-api-key",
	})
}

func gitCMNoProviderEnvironment(t *testing.T) []string {
	t.Helper()
	return environmentWith(map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"HOME":                t.TempDir(),
		"USERPROFILE":         "",
		"YCY_CM_BASE_URL":     "",
		"YCY_CM_MODEL":        "",
		"YCY_CM_API_KEY":      "",
	})
}

type gitCMProviderFixture struct {
	calls  int
	bodies []string
}

func newGitCMMessageProvider(t *testing.T, message string) (*httptest.Server, *gitCMProviderFixture) {
	t.Helper()
	fixture := &gitCMProviderFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fixture.calls++
		if request.Method != http.MethodPost || request.URL.Path != "/chat/completions" {
			t.Errorf("provider request = %s %s", request.Method, request.URL.Path)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read provider request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.bodies = append(fixture.bodies, string(body))
		response.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(response, `{"choices":[{"message":{"content":%q}}]}`, message)
	}))
	return server, fixture
}

func runGitCMStandalone(binary, directory string, environment []string, input string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = environment
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}

func runGitCMPlainStandalone(t *testing.T, binary, directory string, environment []string, input string, arguments ...string) ([]byte, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("controlled PTY fixture is unavailable on Windows")
	}
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key != "TERM" {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "TERM=dumb")

	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start Plain PTY command: %v", err)
	}
	defer func() {
		_ = process.Close()
	}()

	var output bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, process.Terminal())
		readDone <- err
	}()
	if _, err := io.WriteString(process.Terminal(), input); err != nil {
		t.Fatalf("write Plain PTY input: %v", err)
	}
	waitErr := process.Wait()
	if err := process.Close(); err != nil {
		t.Fatalf("close Plain PTY: %v", err)
	}
	select {
	case readErr := <-readDone:
		if readErr != nil && !errors.Is(readErr, syscall.EIO) {
			t.Fatalf("read Plain PTY output: %v", readErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading Plain PTY output: %q", output.String())
	}
	return output.Bytes(), waitErr
}

func gitCMOutput(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	return gitCMCommand(t, directory, arguments...)
}

func gitCMCommand(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = environmentWith(map[string]string{"GIT_CONFIG_NOSYSTEM": "1"})
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func writeGitCMHook(t *testing.T, repository, name, contents string) {
	t.Helper()
	path := filepath.Join(repository, ".git", "hooks", name)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write hook: %v", err)
	}
}

func mutateGitCMStagedFile(repository, name, contents string) error {
	if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0o600); err != nil {
		return err
	}
	command := exec.Command("git", "add", name)
	command.Dir = repository
	command.Env = environmentWith(map[string]string{"GIT_CONFIG_NOSYSTEM": "1"})
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git add %s: %w: %s", name, err, output)
	}
	return nil
}

func newGitCMBareRemote(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	command := exec.Command("git", "init", "--bare", remote)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, output)
	}
	return remote
}

func containsGitCMPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}
