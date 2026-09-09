package fork

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestGitForkRichPTYFourWayArchiveJourney(t *testing.T) {
	const helperEnvironment = "YCY_GIT_FORK_ARCHIVE_PTY_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runGitForkArchivePTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name          string
		width, height uint16
		color         bool
	}{
		{name: "wide color", width: 120, height: 40, color: true},
		{name: "wide no color", width: 120, height: 40, color: false},
		{name: "compact color", width: 40, height: 15, color: true},
		{name: "compact no color", width: 40, height: 15, color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestGitForkRichPTYFourWayArchiveJourney$")
			command.Env = gitForkPTYEnvironment(map[string]string{
				"NO_COLOR":                       map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                           "xterm-256color",
				helperEnvironment:                "1",
				"YCY_GIT_FORK_ARCHIVE_PTY_START": "1",
			})
			output := runGitForkPTYProcess(t, command, testCase.width, testCase.height, "", "GIT_FORK_ARCHIVE_OK")
			assertGitForkArchivePTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func TestGitForkRichPTYFourWayFallbackJourney(t *testing.T) {
	const helperEnvironment = "YCY_GIT_FORK_FALLBACK_PTY_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runGitForkFallbackPTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name          string
		width, height uint16
		color         bool
	}{
		{name: "wide color", width: 120, height: 40, color: true},
		{name: "wide no color", width: 120, height: 40, color: false},
		{name: "compact color", width: 40, height: 15, color: true},
		{name: "compact no color", width: 40, height: 15, color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestGitForkRichPTYFourWayFallbackJourney$")
			command.Env = gitForkPTYEnvironment(map[string]string{
				"NO_COLOR":                        map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                            "xterm-256color",
				helperEnvironment:                 "1",
				"YCY_GIT_FORK_FALLBACK_PTY_START": "1",
			})
			output := runGitForkPTYProcess(t, command, testCase.width, testCase.height, "", "GIT_FORK_FALLBACK_OK")
			assertGitForkFallbackPTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func TestGitForkRichPTYFourWayOverwriteDeclineAndCancel(t *testing.T) {
	const helperEnvironment = "YCY_GIT_FORK_OVERWRITE_PTY_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runGitForkOverwritePTYHelper(t, os.Getenv("YCY_GIT_FORK_OVERWRITE_PTY_MODE"))
		return
	}
	for _, mode := range []struct {
		name  string
		input string
	}{
		{name: "decline", input: "n\r"},
		{name: "cancel", input: "\x1b"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, testCase := range []struct {
				name          string
				width, height uint16
				color         bool
			}{
				{name: "wide color", width: 120, height: 40, color: true},
				{name: "wide no color", width: 120, height: 40, color: false},
				{name: "compact color", width: 40, height: 15, color: true},
				{name: "compact no color", width: 40, height: 15, color: false},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					command := exec.Command(os.Args[0], "-test.run=^TestGitForkRichPTYFourWayOverwriteDeclineAndCancel$")
					command.Env = gitForkPTYEnvironment(map[string]string{
						"NO_COLOR":                         map[bool]string{true: "", false: "1"}[testCase.color],
						"TERM":                             "xterm-256color",
						helperEnvironment:                  "1",
						"YCY_GIT_FORK_OVERWRITE_PTY_START": "1",
						"YCY_GIT_FORK_OVERWRITE_PTY_MODE":  mode.name,
					})
					marker := "GIT_FORK_OVERWRITE_" + strings.ToUpper(mode.name) + "_OK"
					output := runGitForkPTYProcess(t, command, testCase.width, testCase.height, mode.input, marker)
					assertGitForkOverwritePTYOutput(t, output, testCase.color, testCase.width >= 70, mode.name)
				})
			}
		})
	}
}

func TestGitForkRichPTYFailureDoesNotEmitASuccessResult(t *testing.T) {
	const helperEnvironment = "YCY_GIT_FORK_FAILURE_PTY_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		runGitForkFailurePTYHelper(t)
		return
	}

	for _, testCase := range []struct {
		name          string
		width, height uint16
		color         bool
	}{
		{name: "wide color", width: 120, height: 40, color: true},
		{name: "compact no color", width: 40, height: 15, color: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestGitForkRichPTYFailureDoesNotEmitASuccessResult$")
			command.Env = gitForkPTYEnvironment(map[string]string{
				"NO_COLOR":                       map[bool]string{true: "", false: "1"}[testCase.color],
				"TERM":                           "xterm-256color",
				helperEnvironment:                "1",
				"YCY_GIT_FORK_FAILURE_PTY_START": "1",
			})
			output := runGitForkPTYProcess(t, command, testCase.width, testCase.height, "", "GIT_FORK_FAILURE_OK")
			assertGitForkFailurePTYOutput(t, output, testCase.color, testCase.width >= 70)
		})
	}
}

func runGitForkOverwritePTYHelper(t *testing.T, mode string) {
	t.Helper()
	if os.Getenv("YCY_GIT_FORK_OVERWRITE_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}
	root := t.TempDir()
	destination := filepath.Join(root, "project")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatalf("create overwrite destination: %v", err)
	}
	kept := filepath.Join(destination, "keep.txt")
	if err := os.WriteFile(kept, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write overwrite fixture: %v", err)
	}
	called := filepath.Join(root, "network-called")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = os.WriteFile(called, []byte(request.URL.Path), 0o600)
		http.Error(response, "must not be called", http.StatusInternalServerError)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	credentials := appconfig.ForkCredentials{Name: "fixture", Host: host, Scheme: "http", Type: "github", Token: "overwrite-secret"}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	result, err := executeFork(&Options{
		Context: context.Background(), Repository: "fixture:group/project", Destination: destination,
		Config: func() (ConfigReader, error) {
			return richPTYForkConfig{credentials: credentials}, nil
		},
		WorkingDirectory: func() (string, error) { return root, nil },
		HTTP:             server.Client(), Terminal: experience, Git: &gitprocess.Runner{Executable: filepath.Join(root, "missing-git")},
	})
	if err != nil || !result.Cancelled {
		t.Fatalf("executeFork() = (%#v, %v)", result, err)
	}
	if contents, readErr := os.ReadFile(kept); readErr != nil || string(contents) != "keep" {
		t.Fatalf("kept destination = %q, %v", contents, readErr)
	}
	if _, statErr := os.Stat(called); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("overwrite %s reached network: %v", mode, statErr)
	}
	_, _ = fmt.Fprintln(os.Stderr, "GIT_FORK_OVERWRITE_"+strings.ToUpper(mode)+"_OK")
}

func assertGitForkOverwritePTYOutput(t *testing.T, output string, color, wide bool, mode string) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("git fork overwrite %s did not restore primary screen: %q", mode, output)
	}
	live := strings.Join(strings.Fields(terminaltest.StripANSI(visible[enter:leave])), " ")
	for _, expected := range []string{"YCY / git fork", "STATE", "PHASE", "DETAIL"} {
		if !strings.Contains(live, expected) {
			t.Fatalf("git fork overwrite %s live Console missing %q: %q", mode, expected, output)
		}
	}
	if strings.Contains(output, "overwrite-secret") || strings.Contains(output, "must not be called") {
		t.Fatalf("git fork overwrite %s entered mutation or leaked data: %q", mode, output)
	}
	marker := "GIT_FORK_OVERWRITE_" + strings.ToUpper(mode) + "_OK"
	if !strings.Contains(visible, marker) || !strings.Contains(visible, "Cancelled") {
		t.Fatalf("git fork overwrite %s missing cancellation result: %q", mode, output)
	}
	transcript := terminaltest.StripANSI(visible[leave:])
	if wide {
		if !strings.Contains(live, "Project acquisition cancelled") || strings.Contains(live, "Project acquired") {
			t.Fatalf("git fork overwrite %s wide live Outcome is not cancellation-only: %q", mode, output)
		}
	} else if !strings.Contains(live, "Replace destination") || !strings.Contains(live, "confirmation") {
		t.Fatalf("git fork overwrite %s compact live state omitted confirmation phase: %q", mode, output)
	}
	if !strings.Contains(transcript, "OUTCOME  cancelled:") || !strings.Contains(transcript, "Project acquisition cancelled") || strings.Contains(transcript, "Project acquired") {
		t.Fatalf("git fork overwrite %s Transcript Outcome is not cancellation-only: %q", mode, output)
	}
	needle := "Destination replacement declined"
	if mode == "cancel" {
		needle = "Destination replacement cancelled"
	}
	resultIndex := strings.Index(transcript, "Cancelled")
	outcomeIndex := strings.Index(transcript, "OUTCOME  cancelled:")
	if !strings.Contains(transcript, needle) || !strings.Contains(transcript, "Destination unchanged") || !strings.Contains(transcript, "cancelled") || outcomeIndex < 0 || resultIndex < 0 || outcomeIndex > resultIndex {
		t.Fatalf("git fork overwrite %s Transcript missing safe cancellation facts: %q", mode, output)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR git fork overwrite %s output contains %q: %q", mode, prefix, output)
			}
		}
	}
}

func runGitForkFallbackPTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_GIT_FORK_FALLBACK_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v3/repos/group/project/tarball/release" {
			t.Errorf("unexpected fallback request path: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer fork-fallback-secret" {
			t.Errorf("archive Authorization = %q", request.Header.Get("Authorization"))
		}
		response.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "project")
	argumentsPath := filepath.Join(root, "git-arguments")
	gitPath := filepath.Join(root, "git")
	gitScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FORK_FALLBACK_ARGUMENTS\"\nfor value do destination=\"$value\"; done\nmkdir -p \"$destination/.git\"\nprintf 'fallback child output\\n' >&2\nprintf 'fallback contents\\n' > \"$destination/README.md\"\n"
	if err := os.WriteFile(gitPath, []byte(gitScript), 0o700); err != nil {
		t.Fatalf("write Git fallback fixture: %v", err)
	}
	t.Setenv("FORK_FALLBACK_ARGUMENTS", argumentsPath)
	host := strings.TrimPrefix(server.URL, "http://")
	credentials := appconfig.ForkCredentials{Name: "fixture", Host: host, Scheme: "http", Type: "github", Token: "fork-fallback-secret"}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	result, err := executeFork(&Options{
		Context:     context.Background(),
		Repository:  "fixture:group/project#release",
		Destination: destination,
		Config: func() (ConfigReader, error) {
			return richPTYForkConfig{credentials: credentials}, nil
		},
		WorkingDirectory: func() (string, error) { return root, nil },
		HTTP:             server.Client(),
		Terminal:         experience,
		Git:              &gitprocess.Runner{Executable: gitPath},
	})
	if err != nil || result.Cancelled || result.Acquisition != acquisitionClone || result.Ref != "release" {
		t.Fatalf("executeFork() = (%#v, %v)", result, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "README.md")); err != nil {
		t.Fatalf("clone content missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(destination, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clone metadata remained: %v", err)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil || !strings.Contains(string(arguments), "clone\n") || !strings.Contains(string(arguments), "--branch\nrelease\n") {
		t.Fatalf("fallback Git arguments = %q, %v", arguments, err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "GIT_FORK_FALLBACK_OK")
}

func runGitForkFailurePTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_GIT_FORK_FAILURE_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v3/repos/group/project/tarball/release" {
			t.Errorf("unexpected failure request path: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer fork-failure-secret" {
			t.Errorf("archive Authorization = %q", request.Header.Get("Authorization"))
		}
		http.Error(response, "archive child detail should not be shown", http.StatusBadGateway)
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "project")
	gitPath := filepath.Join(root, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nprintf 'clone child secret\n' >&2\nexit 23\n"), 0o700); err != nil {
		t.Fatalf("write Git failure fixture: %v", err)
	}
	host := strings.TrimPrefix(server.URL, "http://")
	credentials := appconfig.ForkCredentials{Name: "fixture", Host: host, Scheme: "http", Type: "github", Token: "fork-failure-secret"}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	result, err := executeFork(&Options{
		Context:     context.Background(),
		Repository:  "fixture:group/project#release",
		Destination: destination,
		Config: func() (ConfigReader, error) {
			return richPTYForkConfig{credentials: credentials}, nil
		},
		WorkingDirectory: func() (string, error) { return root, nil },
		HTTP:             server.Client(),
		Terminal:         experience,
		Git:              &gitprocess.Runner{Executable: gitPath},
	})
	if err == nil || result.Acquisition != "" || result.DiskFact != "Destination may contain a partial clone" {
		t.Fatalf("executeFork() = (%#v, %v)", result, err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "GIT_FORK_FAILURE_OK")
}

func assertGitForkFailurePTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("git fork failure did not restore primary screen: %q", output)
	}
	live := strings.Join(strings.Fields(terminaltest.StripANSI(visible[enter:leave])), " ")
	for _, expected := range []string{"YCY / git fork", "STATE", "PHASE", "DETAIL"} {
		if !strings.Contains(live, expected) {
			t.Fatalf("git fork failure live Console missing %q: %q", expected, output)
		}
	}
	if wide {
		for _, expected := range []string{"FAILED", "Project acquisition failed", "Unable to clone project", "Destination may contain a partial clone"} {
			if !strings.Contains(live, expected) {
				t.Fatalf("wide git fork failure live Outcome missing %q: %q", expected, output)
			}
		}
	} else if !strings.Contains(live, "FAILED") || !strings.Contains(live, "Download archive") {
		t.Fatalf("compact git fork failure live phase missing: %q", output)
	}
	transcript := terminaltest.StripANSI(visible[leave:])
	if strings.Contains(live, "Project acquired") || strings.Contains(transcript, "Project acquired") || strings.Contains(transcript, "Done! Project created") {
		t.Fatalf("git fork failure emitted success-looking output: %q", output)
	}
	if strings.Contains(output, "fork-failure-secret") || strings.Contains(output, "clone child secret") || strings.Contains(output, "archive child detail") || strings.Contains(output, "Authorization") {
		t.Fatalf("git fork failure leaked unsafe detail: %q", output)
	}
	for _, expected := range []string{"Download archive (failed)", "Clone fallback (failed)", "partial clone", "failed:", "OUTCOME  failed:", "Project acquisition failed", "Unable to clone project", "GIT_FORK_FAILURE_OK"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("git fork failure Transcript missing %q: %q", expected, output)
		}
	}
	if strings.Index(transcript, "Download archive (failed)") > strings.Index(transcript, "Clone fallback (failed)") {
		t.Fatalf("git fork failure phase ordering = %q", transcript)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR git fork failure output contains %q: %q", prefix, output)
			}
		}
	}
}

func assertGitForkFallbackPTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("git fork fallback did not restore primary screen: %q", output)
	}
	live := strings.Join(strings.Fields(terminaltest.StripANSI(visible[enter:leave])), " ")
	rowExpected := []string{"YCY / git fork", "STATE", "PHASE", "DETAIL", "Resolve repository"}
	if wide {
		rowExpected = append(rowExpected, "Download archive")
	}
	for _, expected := range rowExpected {
		if !strings.Contains(live, expected) {
			t.Fatalf("git fork fallback live Console missing %q: %q", expected, output)
		}
	}
	if wide {
		for _, expected := range []string{"Clone fallback", "Remove Git metadata", "archive first; git clone fallback"} {
			if !strings.Contains(live, expected) {
				t.Fatalf("wide git fork fallback omitted %q: %q", expected, output)
			}
		}
	}
	for _, expected := range []string{"GIT_FORK_FALLBACK_OK", "Archive download failed", "Cloned and cleaned up", "Done! Project created at project"} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("git fork fallback output missing %q: %q", expected, output)
		}
	}
	if strings.Contains(output, "fork-fallback-secret") || strings.Contains(output, "Authorization") || strings.Contains(output, "fallback child output") {
		t.Fatalf("git fork fallback leaked unsafe or child detail: %q", output)
	}
	transcript := terminaltest.StripANSI(visible[leave:])
	for _, expected := range []string{"Download archive (failed)", "Clone fallback (completed)", "Remove Git metadata (completed)", "succeeded", "Done! Project created at project"} {
		if !strings.Contains(transcript, expected) {
			t.Fatalf("git fork fallback Transcript missing %q: %q", expected, output)
		}
	}
	if strings.Index(transcript, "Download archive (failed)") > strings.Index(transcript, "Clone fallback (completed)") || strings.Index(transcript, "Clone fallback (completed)") > strings.Index(transcript, "Remove Git metadata (completed)") {
		t.Fatalf("git fork fallback phase ordering = %q", transcript)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR git fork fallback output contains %q: %q", prefix, output)
			}
		}
	}
}

func runGitForkArchivePTYHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("YCY_GIT_FORK_ARCHIVE_PTY_START") == "1" {
		var start [2]byte
		if _, err := io.ReadFull(os.Stdin, start[:]); err != nil {
			t.Fatalf("wait for PTY sizing: %v", err)
		}
	}

	archive := gitForkPTYArchive(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v3/repos/group/project":
			if request.Header.Get("Authorization") != "Bearer fork-pty-secret" {
				t.Errorf("default branch Authorization = %q", request.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(response, `{"default_branch":"main"}`)
		case "/api/v3/repos/group/project/tarball/main":
			if request.Header.Get("Authorization") != "Bearer fork-pty-secret" {
				t.Errorf("archive Authorization = %q", request.Header.Get("Authorization"))
			}
			_, _ = response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	destination := filepath.Join(root, "project")
	host := strings.TrimPrefix(server.URL, "http://")
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdin:       terminalexperience.StreamCapability{Terminal: true},
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: os.Getenv("NO_COLOR") == ""},
		},
		Input:       os.Stdin,
		Output:      os.Stdout,
		Diagnostics: os.Stderr,
	})
	credentials := appconfig.ForkCredentials{Name: "fixture", Host: host, Scheme: "http", Type: "github", Token: "fork-pty-secret"}
	result, err := executeFork(&Options{
		Context:     context.Background(),
		Repository:  "fixture:group/project",
		Destination: destination,
		Config: func() (ConfigReader, error) {
			return richPTYForkConfig{credentials: credentials}, nil
		},
		WorkingDirectory: func() (string, error) { return root, nil },
		HTTP:             server.Client(),
		Terminal:         experience,
		Git:              &gitprocess.Runner{},
	})
	if err != nil || result.Cancelled || result.Acquisition != acquisitionArchive || result.Ref != "main" {
		t.Fatalf("executeFork() = (%#v, %v)", result, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "README.md")); err != nil {
		t.Fatalf("archive content missing: %v", err)
	}
	_, _ = fmt.Fprintln(os.Stderr, "GIT_FORK_ARCHIVE_OK")
}

func assertGitForkArchivePTYOutput(t *testing.T, output string, color, wide bool) {
	t.Helper()
	visible := strings.ReplaceAll(output, "\r\n", "\n")
	enter := strings.Index(visible, "\x1b[?1049h")
	leave := strings.LastIndex(visible, "\x1b[?1049l")
	if strings.Count(visible, "\x1b[?1049h") != 1 || strings.Count(visible, "\x1b[?1049l") != 1 || enter < 0 || leave < enter || !strings.Contains(visible, "\x1b[?25h") {
		t.Fatalf("git fork Rich PTY did not restore primary screen: %q", output)
	}
	live := strings.Join(strings.Fields(terminaltest.StripANSI(visible[enter:leave])), " ")
	rowExpected := []string{"YCY / git fork", "STATE", "PHASE", "DETAIL", "Resolve repository", "Inspect destination"}
	if wide {
		rowExpected = append(rowExpected, "Download archive")
	}
	for _, expected := range rowExpected {
		if !strings.Contains(live, expected) {
			t.Fatalf("git fork Rich PTY live Console missing %q: %q", expected, output)
		}
	}
	if wide {
		for _, expected := range []string{"Acquire project files", "archive first; git clone fallback", "repository", "destination"} {
			if !strings.Contains(live, expected) {
				t.Fatalf("wide git fork Rich PTY omitted descriptor/form context %q: %q", expected, output)
			}
		}
	} else if !strings.Contains(live, "main") && !strings.Contains(live, "Project ready") {
		t.Fatalf("compact git fork Rich PTY omitted active-region context: %q", output)
	}
	if strings.Contains(output, "fork-pty-secret") || strings.Contains(output, "http://") || strings.Contains(output, "/private/") {
		t.Fatalf("git fork Rich PTY leaked unsafe provider context: %q", output)
	}
	for _, expected := range []string{"GIT_FORK_ARCHIVE_OK", "Archive downloaded and extracted", "Done! Project created at project"} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("git fork Rich PTY output missing %q: %q", expected, output)
		}
	}
	transcript := terminaltest.StripANSI(visible[leave:])
	ordered := []string{
		"Resolve repository (completed)",
		"Inspect destination (completed)",
		"Resolve default branch (completed)",
		"Download archive (completed)",
		"Extract archive (completed)",
		"succeeded",
		"Done! Project created at project",
	}
	last := 0
	for _, expected := range ordered {
		next := strings.Index(transcript[last:], expected)
		if next < 0 {
			t.Fatalf("git fork Rich PTY Transcript missing ordered event %q: %q", expected, output)
		}
		last += next + len(expected)
	}
	if !color {
		for _, prefix := range []string{"\x1b[38;", "\x1b[3m", "\x1b[9m"} {
			if strings.Contains(output, prefix) {
				t.Fatalf("NO_COLOR git fork Rich PTY output contains %q: %q", prefix, output)
			}
		}
	}
}

func runGitForkPTYProcess(t *testing.T, command *exec.Cmd, width, height uint16, input, marker string) string {
	t.Helper()
	process, err := terminaltest.StartPTY(command)
	if errors.Is(err, terminaltest.ErrPTYUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("start git fork PTY helper: %v", err)
	}
	defer process.Close()
	if err := process.Resize(width, height); err != nil {
		t.Fatalf("resize PTY to %dx%d: %v", width, height, err)
	}
	var output gitForkPTYBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, process.Terminal())
		close(readDone)
	}()
	if _, err := process.Terminal().Write([]byte("go\n")); err != nil {
		t.Fatalf("release PTY helper after sizing: %v", err)
	}
	if input != "" {
		waitForGitForkPTYAny(t, &output, "confirmation", "Overwrite")
		if _, err := process.Terminal().Write([]byte(input)); err != nil {
			t.Fatalf("write git fork confirmation: %v", err)
		}
	}
	waitForGitForkPTYText(t, &output, marker)
	if err := process.Wait(); err != nil {
		t.Fatalf("wait git fork PTY helper: %v\n%s", err, output.String())
	}
	if err := process.Close(); err != nil {
		t.Fatalf("close git fork PTY helper: %v", err)
	}
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading git fork PTY output: %q", output.String())
	}
	return output.String()
}

func gitForkPTYArchive(t *testing.T) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	contents := "archive PTY contents\n"
	if err := tarWriter.WriteHeader(&tar.Header{Name: "project-main/README.md", Mode: 0o644, Size: int64(len(contents))}); err != nil {
		t.Fatalf("write archive header: %v", err)
	}
	if _, err := io.WriteString(tarWriter, contents); err != nil {
		t.Fatalf("write archive contents: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close TAR: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return compressed.Bytes()
}

type richPTYForkConfig struct {
	credentials appconfig.ForkCredentials
}

func (config richPTYForkConfig) ForkInstance(string) (appconfig.ForkCredentials, bool, error) {
	return config.credentials, true, nil
}

func (config richPTYForkConfig) ForkInstanceByHost(string) (appconfig.ForkCredentials, bool, error) {
	return appconfig.ForkCredentials{}, false, nil
}

func gitForkPTYEnvironment(overrides map[string]string) []string {
	ignored := map[string]struct{}{"CI": {}, "CLICOLOR": {}, "CLICOLOR_FORCE": {}, "COLORTERM": {}, "NO_COLOR": {}, "TERM": {}}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, skip := ignored[key]; skip {
			continue
		}
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

type gitForkPTYBuffer struct {
	mu    sync.Mutex
	value strings.Builder
}

func (buffer *gitForkPTYBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.value.Write(value)
}

func (buffer *gitForkPTYBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.value.String()
}

func waitForGitForkPTYText(t *testing.T, output *gitForkPTYBuffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if strings.Contains(output.String(), needle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for git fork PTY text %q: %q", needle, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForGitForkPTYAny(t *testing.T, output *gitForkPTYBuffer, needles ...string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		text := output.String()
		for _, needle := range needles {
			if strings.Contains(text, needle) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for git fork PTY text %q: %q", needles, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
