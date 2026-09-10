//go:build acceptance

package acceptance

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFSStandaloneBinaryPreservesCLIHTTPAndSignalLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal fixture uses Unix process delivery")
	}
	binary := buildDiffStandaloneBinary(t)
	state := t.TempDir()
	environment := environmentWith(map[string]string{"HOME": state, "XDG_STATE_HOME": state, "USERPROFILE": ""})
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	help, err := runDiffStandalone(binary, root, environment, "fs", "--help")
	if err != nil {
		t.Fatalf("fs --help: %v\n%s", err, help)
	}
	for _, expected := range []string{"Browse a directory in a browser", "-p, --port", "-a, --address", "-m, --manage", "--account", "--session-dir", "--chunked-upload", "--upload-chunk-size"} {
		if !strings.Contains(string(help), expected) {
			t.Fatalf("fs help omitted %q:\n%s", expected, help)
		}
	}
	for _, testCase := range []struct {
		arguments []string
		message   string
	}{
		{arguments: []string{"fs", "--port", "1.0", root}, message: "'1.0' is not a valid port"},
		{arguments: []string{"fs", "--port", "65536", root}, message: "Port must be between 0 and 65535"},
		{arguments: []string{"fs", "--account", "invalid", root}, message: "account must use <username>:<password>"},
		{arguments: []string{"fs", filepath.Join(root, "missing")}, message: "workspace root not found"},
	} {
		output, runErr := runDiffStandalone(binary, root, environment, testCase.arguments...)
		if exitCode(runErr) != 1 || !strings.Contains(string(output), testCase.message) {
			t.Fatalf("arguments %q = (%v, %q), want %q", testCase.arguments, runErr, output, testCase.message)
		}
	}

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy local port: %v", err)
	}
	_, occupiedPort, err := net.SplitHostPort(occupied.Addr().String())
	if err != nil {
		_ = occupied.Close()
		t.Fatalf("split occupied port: %v", err)
	}
	output, err := runDiffStandalone(binary, root, environment, "fs", "--address", "127.0.0.1", "--port", occupiedPort, root)
	_ = occupied.Close()
	if exitCode(err) != 1 || !strings.Contains(string(output), "address already in use") {
		t.Fatalf("occupied port = (%v, %q)", err, output)
	}

	process := startDiffStandalone(t, binary, root, environment, "fs", "--address", "127.0.0.1", "--port", "0", "--manage", "--chunked-upload", "--upload-chunk-size", "4", root)
	startup, localURL := waitForFSStartup(t, process)
	if !strings.Contains(startup, "Directory: ") || !strings.Contains(startup, "Management: true") || !strings.Contains(startup, "Chunked uploads: true") {
		t.Fatalf("startup output = %q", startup)
	}
	parsed, err := url.Parse(localURL)
	if err != nil || parsed.Port() == "" || parsed.Port() == "0" || parsed.Hostname() != "127.0.0.1" {
		t.Fatalf("selected URL = %q, parse error = %v", localURL, err)
	}
	assertFSHTTP(t, localURL, "/", http.StatusOK, "text/html; charset=utf-8")
	assertFSHTTP(t, localURL, "/api/session", http.StatusOK, "application/json; charset=utf-8")
	assertFSHTTP(t, localURL, "/api/directory?path=", http.StatusOK, "application/json; charset=utf-8")
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatalf("fs exit after SIGINT: %v\nstderr:\n%s", err, process.stderr.String())
	}
	if process.command.ProcessState == nil || !process.command.ProcessState.Success() || !strings.Contains(readFSProcessOutput(process), "File Browser stopped.") {
		t.Fatalf("SIGINT state = %#v, stdout/stderr = %q / %q", process.command.ProcessState, readFSProcessOutput(process), process.stderr.String())
	}
	lifecycle := process.stderr.String()
	for _, message := range []string{
		"INFO fs: ●  File Browser started",
		"INFO fs: ●  Browse root configured",
		"INFO fs: ●  File Browser capabilities configured",
		"INFO fs: ●  File Browser authentication configured",
		"INFO fs: ●  File Browser stopping",
		"INFO fs: ✓  File Browser stopped",
	} {
		if !strings.Contains(lifecycle, message) {
			t.Fatalf("FS lifecycle omitted %q: %q", message, lifecycle)
		}
	}
	if !strings.Contains(lifecycle, `"bindingAddress":"127.0.0.1"`) || !strings.Contains(lifecycle, `"chunkedUploadsEnabled":true`) || !strings.Contains(lifecycle, `"uploadChunkSizeBytes":4194304`) {
		t.Fatalf("FS startup lifecycle omitted safe capability fields: %q", lifecycle)
	}
	if strings.Index(lifecycle, "File Browser stopping") > strings.Index(lifecycle, "File Browser stopped") || !strings.Contains(lifecycle, `"reason":"context-cancelled"`) {
		t.Fatalf("FS shutdown lifecycle ordering = %q", lifecycle)
	}
	if strings.ContainsAny(lifecycle, "\x1b\r") {
		t.Fatalf("FS lifecycle contains terminal control: %q", lifecycle)
	}
}

func TestFSStandaloneBinaryEmitsNDJSONLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal fixture uses Unix process delivery")
	}
	binary := buildDiffStandaloneBinary(t)
	root := t.TempDir()
	process := startDiffStandalone(t, binary, root, environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""}), "--log-format=json", "fs", "--address", "127.0.0.1", "--port", "0", root)
	startup, localURL := waitForFSStartup(t, process)
	assertFSHTTP(t, localURL, "/api/session", http.StatusOK, "application/json; charset=utf-8")
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatalf("fs NDJSON exit after SIGINT: %v\nstderr:\n%s", err, process.stderr.String())
	}
	if !strings.Contains(startup, "File Browser") || !strings.Contains(readFSProcessOutput(process), "File Browser stopped.") {
		t.Fatalf("FS stdout checkpoints = %q / %q", startup, readFSProcessOutput(process))
	}

	scanner := bufio.NewScanner(strings.NewReader(process.stderr.String()))
	var records []struct {
		Timestamp string         `json:"timestamp"`
		Level     string         `json:"level"`
		Scope     string         `json:"scope"`
		Message   string         `json:"message"`
		Context   map[string]any `json:"context"`
	}
	for scanner.Scan() {
		var record struct {
			Timestamp string         `json:"timestamp"`
			Level     string         `json:"level"`
			Scope     string         `json:"scope"`
			Message   string         `json:"message"`
			Context   map[string]any `json:"context"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode FS NDJSON %q: %v", scanner.Text(), err)
		}
		if record.Timestamp == "" || record.Scope != "fs" || record.Message == "" || strings.ContainsRune(scanner.Text(), '\x1b') {
			t.Fatalf("FS NDJSON record = %#v", record)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan FS NDJSON: %v", err)
	}
	messages := make([]string, 0, len(records))
	for _, record := range records {
		messages = append(messages, record.Message)
	}
	if got, want := messages, []string{
		"File Browser started",
		"Browse root configured",
		"File Browser capabilities configured",
		"File Browser authentication configured",
		"File Browser stopping",
		"File Browser stopped",
	}; !slices.Equal(got, want) {
		t.Fatalf("FS NDJSON messages = %#v, want %#v", got, want)
	}
	if records[0].Context["localURL"] != localURL || records[4].Context["reason"] != "context-cancelled" || records[5].Context["cancelledDownloads"] != float64(0) || records[5].Context["cancelledExtractions"] != float64(0) || records[5].Context["removedChunkedUploads"] != float64(0) {
		t.Fatalf("FS NDJSON lifecycle contexts = %#v / %#v / %#v", records[0].Context, records[4].Context, records[5].Context)
	}
	if strings.ContainsRune(process.stderr.String(), '\x1b') {
		t.Fatalf("FS NDJSON contains ANSI control: %q", process.stderr.String())
	}
}

func TestFSStandaloneBinaryProtectsDataWithFreshGoAuthentication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal fixture uses Unix process delivery")
	}
	binary := buildDiffStandaloneBinary(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	process := startDiffStandalone(t, binary, root, environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""}), "fs", "--address", "127.0.0.1", "--port", "0", "--account", "Alice:password", "--session-dir", t.TempDir(), root)
	_, localURL := waitForFSStartup(t, process)
	unauthenticated, err := http.Get(localURL + "/api/directory?path=")
	if err != nil {
		t.Fatalf("GET protected directory: %v", err)
	}
	_ = unauthenticated.Body.Close()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated directory = %d", unauthenticated.StatusCode)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	login, err := http.NewRequest(http.MethodPost, localURL+"/api/session", strings.NewReader(`{"username":"alice","password":"password"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", localURL)
	response, err := client.Do(login)
	if err != nil {
		t.Fatalf("POST login: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login response = %d", response.StatusCode)
	}
	protected, err := client.Get(localURL + "/files/secret.txt")
	if err != nil {
		t.Fatalf("GET protected file: %v", err)
	}
	contents, readErr := io.ReadAll(protected.Body)
	_ = protected.Body.Close()
	if readErr != nil || protected.StatusCode != http.StatusOK || string(contents) != "secret" {
		t.Fatalf("protected file = (%d, %q, %v)", protected.StatusCode, contents, readErr)
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatalf("authenticated fs exit: %v\nstderr:\n%s", err, process.stderr.String())
	}
}

func TestFSStandaloneBinaryExercisesManagedHTTPArchiveThumbnailAndSSE(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal fixture uses Unix process delivery")
	}
	binary := buildDiffStandaloneBinary(t)
	root := t.TempDir()
	writeStandaloneFSZip(t, filepath.Join(root, "archive.zip"), "inside.txt", "from archive")
	writeStandaloneFSPNG(t, filepath.Join(root, "pixel.png"))
	process := startDiffStandalone(t, binary, root, environmentWith(map[string]string{"HOME": t.TempDir(), "XDG_STATE_HOME": t.TempDir(), "USERPROFILE": ""}), "fs", "--address", "127.0.0.1", "--port", "0", "--manage", root)
	_, base := waitForFSStartup(t, process)

	for _, endpoint := range []string{"/api/downloads/events", "/api/extractions/events"} {
		response := standaloneFSRequest(t, http.MethodGet, base+endpoint, nil, nil)
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || response.Header.Get("Cache-Control") != "no-cache" || response.Header.Get("X-Accel-Buffering") != "no" {
			_ = response.Body.Close()
			t.Fatalf("%s response = %d headers=%v", endpoint, response.StatusCode, response.Header)
		}
		reader := bufio.NewReader(response.Body)
		line, err := reader.ReadString('\n')
		if err != nil || line != "data: {\"version\":1,\"tasks\":[]}\n" {
			_ = response.Body.Close()
			t.Fatalf("%s first event = %q, %v", endpoint, line, err)
		}
		separator, err := reader.ReadString('\n')
		_ = response.Body.Close()
		if err != nil || separator != "\n" {
			t.Fatalf("%s first event separator = %q, %v", endpoint, separator, err)
		}
	}

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, "uploaded from standalone"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadResponse := standaloneFSRequest(t, http.MethodPost, base+"/api/upload?path=", &upload, map[string]string{"Content-Type": writer.FormDataContentType(), "Origin": base})
	uploadBody, err := io.ReadAll(uploadResponse.Body)
	_ = uploadResponse.Body.Close()
	if err != nil || uploadResponse.StatusCode != http.StatusOK || !strings.Contains(string(uploadBody), `"filename":"notes.txt"`) {
		t.Fatalf("standalone upload = (%d, %q, %v)", uploadResponse.StatusCode, uploadBody, err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "notes.txt")); err != nil || string(contents) != "uploaded from standalone" {
		t.Fatalf("standalone upload contents = %q, %v", contents, err)
	}

	thumbnail := standaloneFSRequest(t, http.MethodGet, base+"/thumbnails/pixel.png", nil, nil)
	thumbnailBytes, err := io.ReadAll(thumbnail.Body)
	thumbnailETag := thumbnail.Header.Get("ETag")
	_ = thumbnail.Body.Close()
	if err != nil || thumbnail.StatusCode != http.StatusOK || thumbnail.Header.Get("Content-Type") != "image/webp" || len(thumbnailBytes) == 0 || !strings.HasPrefix(thumbnailETag, "W/\"thumb-") {
		t.Fatalf("standalone thumbnail = (%d, %v, %d bytes, %q)", thumbnail.StatusCode, err, len(thumbnailBytes), thumbnailETag)
	}
	thumbnailCached := standaloneFSRequest(t, http.MethodGet, base+"/thumbnails/pixel.png", nil, map[string]string{"If-None-Match": thumbnailETag})
	_ = thumbnailCached.Body.Close()
	if thumbnailCached.StatusCode != http.StatusNotModified {
		t.Fatalf("standalone conditional thumbnail = %d", thumbnailCached.StatusCode)
	}

	extraction := standaloneFSRequest(t, http.MethodPost, base+"/api/extractions", strings.NewReader(`{"paths":["archive.zip"]}`), map[string]string{"Content-Type": "application/json", "Origin": base})
	if extraction.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(extraction.Body)
		_ = extraction.Body.Close()
		t.Fatalf("standalone extraction enqueue = %d %s", extraction.StatusCode, body)
	}
	_ = extraction.Body.Close()
	completed := waitForStandaloneExtraction(t, base)
	if completed.Status != "done" || completed.DestinationPath == "" {
		t.Fatalf("standalone extraction task = %#v", completed)
	}
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(completed.DestinationPath), "inside.txt"))
	if err != nil || string(contents) != "from archive" {
		t.Fatalf("standalone extraction output = %q, %v", contents, err)
	}

	download := standaloneFSRequest(t, http.MethodPost, base+"/api/downloads", strings.NewReader(`{"url":"http://127.0.0.1:1/","directoryPath":""}`), map[string]string{"Content-Type": "application/json", "Origin": base})
	downloadBody, err := io.ReadAll(download.Body)
	_ = download.Body.Close()
	if err != nil || download.StatusCode != http.StatusForbidden || !strings.Contains(string(downloadBody), "URL_FORBIDDEN") {
		t.Fatalf("standalone private download refusal = (%d, %q, %v)", download.StatusCode, downloadBody, err)
	}

	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatalf("managed standalone FS exit: %v\nstderr:\n%s", err, process.stderr.String())
	}
}

type standaloneFSExtractionTask struct {
	Status          string `json:"status"`
	DestinationPath string `json:"destinationPath"`
}

func waitForStandaloneExtraction(t *testing.T, base string) standaloneFSExtractionTask {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response := standaloneFSRequest(t, http.MethodGet, base+"/api/extractions", nil, nil)
		var payload struct {
			Tasks []standaloneFSExtractionTask `json:"tasks"`
		}
		err := json.NewDecoder(response.Body).Decode(&payload)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("standalone extraction list = %d, %v", response.StatusCode, err)
		}
		if len(payload.Tasks) == 1 && (payload.Tasks[0].Status == "done" || payload.Tasks[0].Status == "error" || payload.Tasks[0].Status == "cancelled") {
			return payload.Tasks[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for standalone archive extraction")
	return standaloneFSExtractionTask{}
}

func standaloneFSRequest(t *testing.T, method, endpoint string, body io.Reader, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	return response
}

func writeStandaloneFSZip(t *testing.T, path, name, contents string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create(name)
	if err == nil {
		_, err = io.WriteString(entry, contents)
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func writeStandaloneFSPNG(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	image := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	image.Set(0, 0, color.NRGBA{R: 255, A: 255})
	image.Set(1, 0, color.NRGBA{B: 255, A: 255})
	err = png.Encode(file, image)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func waitForFSStartup(t *testing.T, process runningDiffStandalone) (string, string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	lines := make([]string, 0, 12)
	localURL := ""
	for {
		select {
		case line, ok := <-process.lines:
			if !ok {
				err := process.command.Wait()
				t.Fatalf("fs exited before startup completed: %v\nstdout:\n%s\nstderr:\n%s", err, strings.Join(lines, "\n"), process.stderr.String())
			}
			lines = append(lines, line)
			if strings.HasPrefix(line, "Local: ") {
				localURL = strings.TrimPrefix(line, "Local: ")
			}
			if strings.HasPrefix(line, "Authentication: ") {
				if localURL == "" {
					t.Fatalf("FS startup omitted local URL:\n%s", strings.Join(lines, "\n"))
				}
				return strings.Join(lines, "\n"), localURL
			}
		case <-deadline.C:
			_ = process.command.Process.Kill()
			_ = process.command.Wait()
			t.Fatalf("timed out waiting for FS startup\nstderr:\n%s", process.stderr.String())
		}
	}
}

func assertFSHTTP(t *testing.T, base, path string, status int, contentType string) {
	t.Helper()
	response := waitForDiffHTTPResponse(t, base+path)
	defer response.Body.Close()
	if response.StatusCode != status || response.Header.Get("Content-Type") != contentType {
		t.Fatalf("GET %s = %d headers=%v, want %d %q", path, response.StatusCode, response.Header, status, contentType)
	}
}

func readFSProcessOutput(process runningDiffStandalone) string {
	lines := make([]string, 0, 4)
	for line := range process.lines {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
