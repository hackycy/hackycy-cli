//go:build acceptance

package acceptance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDiffStandaloneBinaryPreservesCLIValidationAndLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal fixture uses Unix process delivery")
	}
	binary := buildDiffStandaloneBinary(t)
	environment := environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""})

	help, err := runDiffStandalone(binary, t.TempDir(), environment, "diff", "--help")
	if err != nil {
		t.Fatalf("diff --help: %v\n%s", err, help)
	}
	for _, expected := range []string{"Compare two directories in a browser", "-p, --port", "--public", "-x, --exclude", "--no-gitignore"} {
		if !strings.Contains(string(help), expected) {
			t.Fatalf("diff help omitted %q:\n%s", expected, help)
		}
	}
	if strings.Contains(string(help), "--address") {
		t.Fatalf("diff help exposed an address flag:\n%s", help)
	}

	root := t.TempDir()
	baseline := filepath.Join(root, "baseline")
	target := filepath.Join(root, "target")
	writeStandaloneDiffFile(t, baseline, "same.txt", "same")
	writeStandaloneDiffFile(t, target, "same.txt", "same")
	for _, testCase := range []struct {
		arguments []string
		message   string
	}{
		{arguments: []string{"diff", "--port", "1.0", baseline, target}, message: "'1.0' is not a valid port"},
		{arguments: []string{"diff", "--port", "65536", baseline, target}, message: "Port must be between 0 and 65535"},
		{arguments: []string{"diff", baseline}, message: "accepts 2 arg(s)"},
		{arguments: []string{"diff", baseline, target, "extra"}, message: "accepts 2 arg(s)"},
		{arguments: []string{"diff", baseline, baseline}, message: "Baseline Directory and Target Directory must be different"},
	} {
		output, runErr := runDiffStandalone(binary, root, environment, testCase.arguments...)
		if exitCode(runErr) != 1 || !strings.Contains(string(output), testCase.message) {
			t.Fatalf("arguments %q = (%v, %q), want %q", testCase.arguments, runErr, output, testCase.message)
		}
	}

	notDirectory := filepath.Join(root, "not-directory")
	writeStandaloneDiffFile(t, root, "not-directory", "file")
	output, err := runDiffStandalone(binary, root, environment, "diff", baseline, notDirectory)
	if exitCode(err) != 1 || !strings.Contains(string(output), "Target Directory must be a directory") {
		t.Fatalf("non-directory target = (%v, %q)", err, output)
	}

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy local port: %v", err)
	}
	_, occupiedPort, err := net.SplitHostPort(occupied.Addr().String())
	if err != nil {
		occupied.Close()
		t.Fatalf("split occupied port: %v", err)
	}
	output, err = runDiffStandalone(binary, root, environment, "diff", "--port", occupiedPort, baseline, target)
	occupied.Close()
	if exitCode(err) != 1 || !strings.Contains(string(output), "address already in use") {
		t.Fatalf("occupied port = (%v, %q)", err, output)
	}

	process := startDiffStandalone(t, binary, root, environment, "diff", "--port", "0", "-x", "ignored/**", "--no-gitignore", baseline, target)
	startup, localURL := waitForDiffStartup(t, process)
	resolvedBaseline, err := filepath.EvalSymlinks(baseline)
	if err != nil {
		t.Fatalf("resolve baseline: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	for _, expected := range []string{
		"Directory diff: " + localURL,
		"MCP endpoint:   " + localURL + "/mcp",
		"Baseline: " + resolvedBaseline,
		"Target:   " + resolvedTarget,
	} {
		if !strings.Contains(startup, expected) {
			t.Fatalf("startup output omitted %q:\n%s", expected, startup)
		}
	}
	parsedURL, err := url.Parse(localURL)
	if err != nil || parsedURL.Port() == "" || parsedURL.Port() == "0" {
		t.Fatalf("selected URL = %q, parse error = %v", localURL, err)
	}
	state := waitForDiffHTTPResponse(t, localURL+"/api/state")
	state.Body.Close()
	if state.StatusCode != http.StatusOK || state.Header.Get("Content-Type") != "application/json;charset=utf-8" {
		t.Fatalf("state response = %d, headers = %v", state.StatusCode, state.Header)
	}

	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatalf("diff exit after SIGINT: %v\nstderr:\n%s", err, process.stderr.String())
	}
	assertDiffTextLifecycle(t, process.stderr.String(), localURL, resolvedBaseline, resolvedTarget)
}

func TestDiffStandaloneBinaryEmitsNDJSONLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal fixture uses Unix process delivery")
	}
	binary := buildDiffStandaloneBinary(t)
	environment := environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""})
	root := t.TempDir()
	baseline := filepath.Join(root, "baseline")
	target := filepath.Join(root, "target")
	writeStandaloneDiffFile(t, baseline, "same.txt", "same")
	writeStandaloneDiffFile(t, target, "same.txt", "same")
	process := startDiffStandalone(t, binary, root, environment, "--log-format=json", "diff", "--port", "0", baseline, target)
	_, localURL := waitForDiffStartup(t, process)
	waitForDiffReady(t, localURL)
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatalf("diff exit after SIGINT: %v\nstderr:\n%s", err, process.stderr.String())
	}
	scanner := bufio.NewScanner(strings.NewReader(process.stderr.String()))
	var messages []string
	for scanner.Scan() {
		var record struct {
			Level   string         `json:"level"`
			Scope   string         `json:"scope"`
			Message string         `json:"message"`
			Context map[string]any `json:"context"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode diff NDJSON %q: %v", scanner.Text(), err)
		}
		if record.Scope != "diff" || record.Message == "" {
			t.Fatalf("NDJSON lifecycle record = %#v", record)
		}
		messages = append(messages, record.Message)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan diff NDJSON: %v", err)
	}
	if len(messages) < 6 || messages[0] != "Directory diff started" || messages[1] != "Diff endpoints available" || messages[2] != "Comparison workspace configured" || messages[3] != "Initial comparison refresh started" || messages[len(messages)-2] != "Directory diff stopping" || messages[len(messages)-1] != "Directory diff stopped" {
		t.Fatalf("NDJSON lifecycle messages = %#v (URL %s)", messages, localURL)
	}
	if !strings.Contains(strings.Join(messages, "\n"), "Comparison snapshot ready") && !strings.Contains(strings.Join(messages, "\n"), "Comparison refresh cancelled") {
		t.Fatalf("NDJSON lifecycle omitted refresh terminal event: %#v", messages)
	}
	if strings.ContainsRune(process.stderr.String(), '\x1b') {
		t.Fatalf("NDJSON lifecycle contains ANSI control: %q", process.stderr.String())
	}
}

func waitForDiffReady(t *testing.T, localURL string) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(localURL + "/api/state")
		if err == nil {
			var state struct {
				Workspace struct {
					Phase string `json:"phase"`
				} `json:"workspace"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&state)
			response.Body.Close()
			if decodeErr == nil && state.Workspace.Phase == "ready" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for diff ready state")
}

func assertDiffTextLifecycle(t *testing.T, contents, localURL, baseline, target string) {
	t.Helper()
	if strings.Contains(contents, "error:") || strings.Contains(contents, "\x1b[") || strings.ContainsAny(contents, "\r") {
		t.Fatalf("diff lifecycle contains root duplicate or terminal control: %q", contents)
	}
	lines := strings.Split(strings.TrimSuffix(contents, "\n"), "\n")
	if len(lines) < 7 {
		t.Fatalf("diff lifecycle emitted too few records: %q", contents)
	}
	for _, expected := range []string{
		"Directory diff started",
		"Diff endpoints available",
		"Comparison workspace configured",
		"Initial comparison refresh started",
		"Comparison snapshot ready",
		"Directory diff stopping",
		"Directory diff stopped",
	} {
		found := false
		for _, line := range lines {
			if strings.Contains(line, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("diff lifecycle omitted %q: %q", expected, contents)
		}
	}
	order := []string{
		"Directory diff started",
		"Diff endpoints available",
		"Comparison workspace configured",
		"Initial comparison refresh started",
		"Comparison snapshot ready",
		"Directory diff stopping",
		"Directory diff stopped",
	}
	last := -1
	for _, expected := range order {
		index := -1
		for candidate, line := range lines {
			if candidate > last && strings.Contains(line, expected) {
				index = candidate
				break
			}
		}
		if index < 0 {
			t.Fatalf("diff lifecycle order missing %q after line %d: %q", expected, last, contents)
		}
		last = index
	}
	if !strings.Contains(contents, `"localURL":"`+localURL+`"`) || !strings.Contains(contents, `"networkURLs":[]`) || !strings.Contains(contents, `"baselineDirectory":"`+baseline+`"`) || !strings.Contains(contents, `"targetDirectory":"`+target+`"`) {
		t.Fatalf("diff startup lifecycle fields missing: %q", contents)
	}
}

func buildDiffStandaloneBinary(t *testing.T) string {
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

func runDiffStandalone(binary, directory string, environment []string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = environment
	return command.CombinedOutput()
}

type runningDiffStandalone struct {
	command *exec.Cmd
	lines   <-chan string
	stderr  *bytes.Buffer
}

func startDiffStandalone(t *testing.T, binary, directory string, environment []string, arguments ...string) runningDiffStandalone {
	t.Helper()
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = environment
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("open standalone stdout: %v", err)
	}
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start standalone diff: %v", err)
	}
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	return runningDiffStandalone{command: command, lines: lines, stderr: stderr}
}

func waitForDiffStartup(t *testing.T, process runningDiffStandalone) (string, string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	lines := make([]string, 0, 8)
	localURL := ""
	for {
		select {
		case line, ok := <-process.lines:
			if !ok {
				err := process.command.Wait()
				t.Fatalf("diff exited before startup completed: %v\nstdout:\n%s\nstderr:\n%s", err, strings.Join(lines, "\n"), process.stderr.String())
			}
			lines = append(lines, line)
			if strings.HasPrefix(line, "Directory diff: ") {
				localURL = strings.TrimPrefix(line, "Directory diff: ")
			}
			if strings.HasPrefix(line, "Target:   ") {
				if localURL == "" {
					t.Fatalf("Diff startup omitted local URL:\n%s", strings.Join(lines, "\n"))
				}
				return strings.Join(lines, "\n"), localURL
			}
		case <-deadline.C:
			_ = process.command.Process.Kill()
			_ = process.command.Wait()
			t.Fatalf("timed out waiting for Diff startup\nstderr:\n%s", process.stderr.String())
		}
	}
}

func waitForDiffHTTPResponse(t *testing.T, endpoint string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			return response
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out requesting %s", endpoint)
	return nil
}

func waitForDiffProcess(t *testing.T, process runningDiffStandalone) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- process.command.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		_ = process.command.Process.Kill()
		return <-done
	}
}

func writeStandaloneDiffFile(t *testing.T, root, name, contents string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return path
}
