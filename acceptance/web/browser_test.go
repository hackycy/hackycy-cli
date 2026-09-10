//go:build acceptance

package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	urlpkg "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const (
	browserReadyTimeout = 15 * time.Second
	shutdownTimeout     = 10 * time.Second
)

// TestBrowserAcceptanceLoadsRealServices starts the assembled ycy binary for
// each browser command. The static readiness harness intentionally cannot
// stand in for these command-owned HTTP surfaces.
func TestBrowserAcceptanceLoadsRealServices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("acceptance-web records native Linux browser lifecycle evidence")
	}
	binary := buildStandaloneBinary(t)

	for _, testCase := range []struct {
		name              string
		readyText         string
		apiPaths          []string
		start             func(*testing.T, string) (*runningService, string)
		browserSessionFor func(*testing.T, string) *http.Cookie
	}{
		{name: "diff", readyText: "HACKYCY CLI — DIFF SERVER", apiPaths: []string{"/api/state"}, start: startDiffService},
		{name: "fs", readyText: "HACKYCY CLI · FILE BROWSER", apiPaths: []string{"/api/session", "/api/directory"}, start: startFSService},
		{name: "tunnel", readyText: "Overview", apiPaths: []string{"/api/state"}, start: startTunnelService, browserSessionFor: signInTunnelBrowserSession},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, pageURL := testCase.start(t, binary)
			stopped := false
			t.Cleanup(func() {
				if !stopped {
					_ = service.stop()
				}
			})

			waitForHTTPService(t, service, pageURL)
			var browserSession *http.Cookie
			if testCase.browserSessionFor != nil {
				browserSession = testCase.browserSessionFor(t, pageURL)
			}
			assertBrowserJourney(t, pageURL, testCase.readyText, testCase.apiPaths, browserSession)
			if err := service.stop(); err != nil {
				t.Fatalf("clean shutdown: %v", err)
			}
			stopped = true
		})
	}
}

func buildStandaloneBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "ycy")
	command := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	command.Dir = repositoryRoot(t)
	command.Env = environmentWith(map[string]string{
		"CGO_ENABLED": "0",
		"GOTOOLCHAIN": "go1.26.7",
		"GOWORK":      "off",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}
	return binary
}

func startDiffService(t *testing.T, binary string) (*runningService, string) {
	t.Helper()
	root := t.TempDir()
	baseline := filepath.Join(root, "baseline")
	target := filepath.Join(root, "target")
	for _, directory := range []string{baseline, target} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create Diff fixture directory: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(baseline, "example.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatalf("write Diff baseline: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "example.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatalf("write Diff target: %v", err)
	}
	port := reserveTCPPort(t)
	service := startService(t, binary, root, environmentWith(map[string]string{
		"HOME":        t.TempDir(),
		"USERPROFILE": "",
	}), "diff", "--port", strconv.Itoa(port), baseline, target)
	return service, localURL(port)
}

func startFSService(t *testing.T, binary string) (*runningService, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("browser acceptance\n"), 0o600); err != nil {
		t.Fatalf("write FS fixture: %v", err)
	}
	port := reserveTCPPort(t)
	service := startService(t, binary, root, environmentWith(map[string]string{
		"HOME":                         t.TempDir(),
		"USERPROFILE":                  "",
		"XDG_STATE_HOME":               t.TempDir(),
		"YCY_FS_CHUNKED_UPLOAD":        "false",
		"YCY_FS_SESSION_DIR":           "",
		"YCY_FS_SESSION_IDLE_DAYS":     "7",
		"YCY_FS_UPLOAD_CHUNK_SIZE_MIB": "8",
	}), "fs", "--address", "127.0.0.1", "--port", strconv.Itoa(port), root)
	return service, localURL(port)
}

func startTunnelService(t *testing.T, binary string) (*runningService, string) {
	t.Helper()
	controlPort := reserveTCPPort(t)
	frpPort := reserveTCPPort(t)
	httpPort := reserveTCPPort(t)
	stateDirectory := t.TempDir()
	service := startService(t, binary, t.TempDir(), environmentWith(map[string]string{
		"HOME":                         t.TempDir(),
		"USERPROFILE":                  "",
		"XDG_STATE_HOME":               t.TempDir(),
		"YCY_TUNNEL_ADMIN_USER":        "browser-admin",
		"YCY_TUNNEL_ADMIN_PASSWORD":    "browser-acceptance-password",
		"YCY_TUNNEL_DOCKER":            "",
		"YCY_TUNNEL_FRP_TOKEN":         "browser-acceptance-token",
		"YCY_TUNNEL_SESSION_IDLE_DAYS": "7",
	}), "tunnel", "server",
		"--address", "127.0.0.1",
		"--control-port", strconv.Itoa(controlPort),
		"--frp-port", strconv.Itoa(frpPort),
		"--http-port", strconv.Itoa(httpPort),
		"--port-range", "20000-20100",
		"--data-dir", stateDirectory,
	)
	return service, localURL(controlPort)
}

func signInTunnelBrowserSession(t *testing.T, pageURL string) *http.Cookie {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, pageURL+"/api/session", strings.NewReader(`{"username":"browser-admin","password":"browser-acceptance-password"}`))
	if err != nil {
		t.Fatalf("create Tunnel browser session request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", pageURL)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("create Tunnel browser session: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read Tunnel browser session response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create Tunnel browser session: got %s\n%s", response.Status, body)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "ycy_tunnel_session" {
			return cookie
		}
	}
	t.Fatalf("create Tunnel browser session: response omitted ycy_tunnel_session\n%s", body)
	return nil
}

func startService(t *testing.T, binary, directory string, environment []string, arguments ...string) *runningService {
	t.Helper()
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	command.Env = environment
	service := &runningService{command: command, done: make(chan struct{})}
	command.Stdout = &service.stdout
	command.Stderr = &service.stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start %q: %v", arguments, err)
	}
	go func() {
		err := command.Wait()
		service.mu.Lock()
		service.waitErr = err
		service.mu.Unlock()
		close(service.done)
	}()
	return service
}

type runningService struct {
	command *exec.Cmd
	stdout  synchronizedBuffer
	stderr  synchronizedBuffer
	done    chan struct{}

	mu       sync.Mutex
	waitErr  error
	stopErr  error
	stopOnce sync.Once
}

func (service *runningService) stop() error {
	service.stopOnce.Do(func() {
		select {
		case <-service.done:
			service.stopErr = fmt.Errorf("service exited before shutdown: %w\n%s", service.exitError(), service.output())
			return
		default:
		}
		if err := service.command.Process.Signal(os.Interrupt); err != nil {
			select {
			case <-service.done:
				service.stopErr = fmt.Errorf("signal service: %w\n%s", err, service.output())
				return
			default:
			}
			service.stopErr = fmt.Errorf("signal service: %w", err)
			return
		}
		select {
		case <-service.done:
			if err := service.exitError(); err != nil {
				service.stopErr = fmt.Errorf("service exited after SIGINT: %w\n%s", err, service.output())
			}
		case <-time.After(shutdownTimeout):
			_ = service.command.Process.Kill()
			<-service.done
			service.stopErr = fmt.Errorf("service did not stop within %s\n%s", shutdownTimeout, service.output())
		}
	})
	return service.stopErr
}

func (service *runningService) exitError() error {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.waitErr
}

func (service *runningService) output() string {
	return fmt.Sprintf("stdout:\n%s\nstderr:\n%s", service.stdout.String(), service.stderr.String())
}

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(source []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.Write(source)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}

func waitForHTTPService(t *testing.T, service *runningService, pageURL string) {
	t.Helper()
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(browserReadyTimeout)
	for time.Now().Before(deadline) {
		response, err := client.Get(pageURL)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-service.done:
			t.Fatalf("service exited before accepting browser traffic: %v\n%s", service.exitError(), service.output())
		case <-time.After(25 * time.Millisecond):
		}
	}
	t.Fatalf("service did not become ready at %s\n%s", pageURL, service.output())
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	defer listener.Close()
	_, rawPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split reserved listener address: %v", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatalf("parse reserved listener port: %v", err)
	}
	return port
}

func localURL(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func environmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
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

func assertBrowserJourney(t *testing.T, pageURL, readyText string, apiPaths []string, browserSession *http.Cookie) {
	t.Helper()
	allocatorOptions := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocatorOptions = append(allocatorOptions,
		chromedp.ExecPath(chromeExecutable(t)),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("headless", true),
	)
	allocatorContext, closeAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	defer closeAllocator()
	browserContext, closeBrowser := chromedp.NewContext(allocatorContext)
	defer closeBrowser()
	ctx, cancel := context.WithTimeout(browserContext, browserReadyTimeout)
	defer cancel()

	observer := newBrowserObserver()
	chromedp.ListenTarget(ctx, observer.observe)
	if err := chromedp.Run(ctx, network.Enable(), cdpruntime.Enable(), cdplog.Enable()); err != nil {
		t.Fatalf("enable browser observers: %v", err)
	}
	if browserSession != nil {
		if err := chromedp.Run(ctx, network.SetCookie(browserSession.Name, browserSession.Value).WithURL(pageURL).WithHTTPOnly(browserSession.HttpOnly).WithSameSite(network.CookieSameSiteStrict)); err != nil {
			t.Fatalf("set browser session cookie: %v", err)
		}
	}
	if err := chromedp.Run(ctx, chromedp.Navigate(pageURL), chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		t.Fatalf("open %s in headless Chrome: %v", pageURL, err)
	}
	if body, err := waitForBrowserText(ctx, readyText); err != nil {
		t.Fatalf("wait for browser UI %q: %v\nbody:\n%s\n%s", readyText, err, body, observer.describe())
	}

	var criticalResources []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`Array.from(document.querySelectorAll('script[src], link[rel="stylesheet"], link[rel="modulepreload"]')).map((node) => node.src || node.href).filter(Boolean)`, &criticalResources)); err != nil {
		t.Fatalf("read critical browser resources: %v", err)
	}
	if len(criticalResources) == 0 {
		t.Fatal("browser page did not declare any critical resources")
	}
	if err := observer.waitFor(pageURL, criticalResources, apiPaths, browserReadyTimeout); err != nil {
		t.Fatal(err)
	}
	if err := observer.assertClean(pageURL, criticalResources, apiPaths); err != nil {
		t.Fatal(err)
	}
}

func chromeExecutable(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("YCY_CHROME_BINARY"),
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
	}
	t.Fatal("acceptance-web requires Chrome or Chromium; set YCY_CHROME_BINARY to its executable")
	return ""
}

func waitForBrowserText(ctx context.Context, readyText string) (string, error) {
	deadline := time.Now().Add(browserReadyTimeout)
	expression := fmt.Sprintf("document.body.innerText.includes(%q)", readyText)
	var body string
	for time.Now().Before(deadline) {
		var ready bool
		err := chromedp.Run(ctx,
			chromedp.Evaluate(expression, &ready),
			chromedp.Evaluate("document.body.innerText", &body),
		)
		if err == nil && ready {
			return body, nil
		}
		if ctx.Err() != nil {
			if err != nil {
				return body, err
			}
			return body, ctx.Err()
		}
		time.Sleep(25 * time.Millisecond)
	}
	return body, fmt.Errorf("did not render within %s", browserReadyTimeout)
}

type browserResponse struct {
	url          string
	status       int
	resourceType network.ResourceType
}

type browserObserver struct {
	mu          sync.Mutex
	requests    map[network.RequestID]string
	responses   []browserResponse
	failures    []string
	consoleLogs []string
}

func newBrowserObserver() *browserObserver {
	return &browserObserver{requests: make(map[network.RequestID]string)}
}

func (observer *browserObserver) observe(event any) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	switch value := event.(type) {
	case *network.EventRequestWillBeSent:
		if value.Request != nil {
			observer.requests[value.RequestID] = value.Request.URL
		}
	case *network.EventResponseReceived:
		if value.Response != nil {
			observer.responses = append(observer.responses, browserResponse{url: value.Response.URL, status: int(value.Response.Status), resourceType: value.Type})
		}
	case *network.EventLoadingFailed:
		if !value.Canceled {
			observer.failures = append(observer.failures, fmt.Sprintf("%s %s: %s", value.Type, observer.requests[value.RequestID], value.ErrorText))
		}
	case *cdpruntime.EventConsoleAPICalled:
		if value.Type == cdpruntime.APITypeError {
			observer.consoleLogs = append(observer.consoleLogs, "console.error: "+consoleArguments(value.Args))
		}
	case *cdpruntime.EventExceptionThrown:
		if value.ExceptionDetails != nil {
			message := value.ExceptionDetails.Text
			if value.ExceptionDetails.Exception != nil && value.ExceptionDetails.Exception.Description != "" {
				message = value.ExceptionDetails.Exception.Description
			}
			observer.consoleLogs = append(observer.consoleLogs, "unhandled exception: "+message)
		}
	case *cdplog.EventEntryAdded:
		if value.Entry != nil && value.Entry.Level == cdplog.LevelError {
			observer.consoleLogs = append(observer.consoleLogs, "browser log: "+value.Entry.Text+" "+value.Entry.URL)
		}
	}
}

func consoleArguments(arguments []*cdpruntime.RemoteObject) string {
	values := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		if argument != nil && argument.Description != "" {
			values = append(values, argument.Description)
		}
	}
	if len(values) == 0 {
		return "no details"
	}
	return strings.Join(values, " ")
}

func (observer *browserObserver) waitFor(pageURL string, resources, apiPaths []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if observer.hasSuccessfulResponses(pageURL, resources, apiPaths) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("browser did not load all critical resources and APIs within %s\n%s", timeout, observer.describe())
}

func (observer *browserObserver) hasSuccessfulResponses(pageURL string, resources, apiPaths []string) bool {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	for _, resource := range resources {
		if !hasSuccessfulURL(observer.responses, resource) {
			return false
		}
	}
	for _, path := range apiPaths {
		if !hasSuccessfulPath(observer.responses, pageURL, path) {
			return false
		}
	}
	return true
}

func (observer *browserObserver) assertClean(pageURL string, resources, apiPaths []string) error {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if !hasSuccessfulPath(observer.responses, pageURL, "/") {
		return fmt.Errorf("initial page did not return a successful response\n%s", observer.describeLocked())
	}
	for _, resource := range resources {
		if !hasSuccessfulURL(observer.responses, resource) {
			return fmt.Errorf("critical resource did not return successfully: %s\n%s", resource, observer.describeLocked())
		}
	}
	for _, path := range apiPaths {
		if !hasSuccessfulPath(observer.responses, pageURL, path) {
			return fmt.Errorf("initial API did not return successfully: %s\n%s", path, observer.describeLocked())
		}
	}
	pageOrigin, err := origin(pageURL)
	if err != nil {
		return err
	}
	for _, response := range observer.responses {
		responseOrigin, err := origin(response.url)
		if err == nil && responseOrigin == pageOrigin && response.status >= http.StatusBadRequest {
			return fmt.Errorf("browser resource failed: %s returned %d\n%s", response.url, response.status, observer.describeLocked())
		}
	}
	if len(observer.failures) > 0 || len(observer.consoleLogs) > 0 {
		return fmt.Errorf("browser reported console or resource failures\n%s", observer.describeLocked())
	}
	return nil
}

func hasSuccessfulURL(responses []browserResponse, target string) bool {
	for _, response := range responses {
		if response.url == target && response.status >= http.StatusOK && response.status < http.StatusBadRequest {
			return true
		}
	}
	return false
}

func hasSuccessfulPath(responses []browserResponse, pageURL, path string) bool {
	pageOrigin, err := origin(pageURL)
	if err != nil {
		return false
	}
	for _, response := range responses {
		responseURL, err := urlpkg.Parse(response.url)
		if err != nil || responseURL.Scheme+"://"+responseURL.Host != pageOrigin || responseURL.Path != path {
			continue
		}
		if response.status >= http.StatusOK && response.status < http.StatusBadRequest {
			return true
		}
	}
	return false
}

func origin(rawURL string) (string, error) {
	parsed, err := urlpkg.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func (observer *browserObserver) describe() string {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return observer.describeLocked()
}

func (observer *browserObserver) describeLocked() string {
	responses := make([]string, 0, len(observer.responses))
	for _, response := range observer.responses {
		responses = append(responses, fmt.Sprintf("%d %s %s", response.status, response.resourceType, response.url))
	}
	return strings.Join([]string{
		"responses: " + strings.Join(uniqueSorted(responses), " | "),
		"resource failures: " + strings.Join(uniqueSorted(observer.failures), " | "),
		"console errors: " + strings.Join(uniqueSorted(observer.consoleLogs), " | "),
	}, "\n")
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
