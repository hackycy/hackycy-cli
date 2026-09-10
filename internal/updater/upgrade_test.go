package updater

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunUpgradeSchedulesOnlyVerifiedCandidate(t *testing.T) {
	content := []byte("new executable")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			_, _ = io.WriteString(writer, `{"tag_name":"v2.0.0","assets":[{"name":"ycy-linux-x64","digest":"sha256:`+sha256Bytes(content)+`"}]}`)
		case "/download/v2.0.0/ycy-linux-x64":
			_, _ = writer.Write(content)
		default:
			t.Errorf("unexpected request path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	directory := t.TempDir()
	target := nativeTestExecutablePath(filepath.Join(directory, "ycy"))
	if err := os.WriteFile(target, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	var spawnedPath string
	var spawnedArgs []string
	var phases []UpgradePhaseEvent
	result, err := RunUpgrade(context.Background(), UpgradeOptions{
		Resolver: ReleaseResolverOptions{LatestURL: server.URL + "/latest", DownloadBaseURL: server.URL + "/download", CurrentVersion: "1.0.0", GOOS: "linux", GOARCH: "amd64"},
		Candidate: CandidateOptions{
			TransactionID: func() (string, error) { return "tx", nil },
			Executor: func(context.Context, string, []string, []string) (ProcessResult, error) {
				return ProcessResult{Stdout: []byte("2.0.0\n")}, nil
			},
		},
		Executable: func() (string, error) { return target, nil },
		Copy: func(source, destination string) error {
			return os.WriteFile(destination, []byte("updater"), 0o700)
		},
		Spawn: func(_ context.Context, path string, args []string) error {
			spawnedPath, spawnedArgs = path, append([]string(nil), args...)
			return nil
		},
		TempDirectory: func() string { return directory },
		PID:           func() int { return 123 },
		Now:           func() time.Time { return time.Unix(1, 0) },
		Observer: UpgradeObserver{Phase: func(event UpgradePhaseEvent) {
			phases = append(phases, event)
		}},
	})
	if err != nil || !result.Scheduled || result.ScheduledVersion != "2.0.0" {
		t.Fatalf("run = %#v, %v", result, err)
	}
	if spawnedPath != expectedUpdaterPath(directory, "tx") || len(spawnedArgs) == 0 || FindInternalMarker(spawnedArgs) != 0 {
		t.Fatalf("spawn = %s %#v", spawnedPath, spawnedArgs)
	}
	state, err := ReadState(StatePath(target))
	if err != nil || state == nil || state.Status != StatusPending {
		t.Fatalf("pending state = %#v, %v", state, err)
	}
	assertPrivateUpgradePath(t, result.State.StagedPath, 0o755)
	assertPrivateUpgradePath(t, spawnedPath, 0o755)
	assertPrivateUpgradePath(t, state.StatePath, 0o600)
	wantPhases := []UpgradePhaseEvent{
		{Phase: UpgradePhaseConsumeStartupTransaction, State: UpgradePhaseActive},
		{Phase: UpgradePhaseConsumeStartupTransaction, State: UpgradePhaseCompleted},
		{Phase: UpgradePhaseResolveRelease, State: UpgradePhaseActive},
		{Phase: UpgradePhaseResolveRelease, State: UpgradePhaseCompleted},
		{Phase: UpgradePhaseResolveArtifact, State: UpgradePhaseActive},
		{Phase: UpgradePhaseResolveArtifact, State: UpgradePhaseCompleted},
		{Phase: UpgradePhaseDownloadCandidate, State: UpgradePhaseActive},
		{Phase: UpgradePhaseDownloadCandidate, State: UpgradePhaseCompleted},
		{Phase: UpgradePhaseVerifyCandidate, State: UpgradePhaseActive},
		{Phase: UpgradePhaseVerifyCandidate, State: UpgradePhaseCompleted},
		{Phase: UpgradePhaseStageUpdater, State: UpgradePhaseActive},
		{Phase: UpgradePhaseStageUpdater, State: UpgradePhaseCompleted},
		{Phase: UpgradePhasePublishPending, State: UpgradePhaseActive},
		{Phase: UpgradePhasePublishPending, State: UpgradePhaseCompleted},
		{Phase: UpgradePhaseScheduleUpdater, State: UpgradePhaseActive},
		{Phase: UpgradePhaseScheduleUpdater, State: UpgradePhaseCompleted},
		{Phase: UpgradePhaseComplete, State: UpgradePhaseActive},
		{Phase: UpgradePhaseComplete, State: UpgradePhaseCompleted},
	}
	if len(phases) != len(wantPhases) {
		t.Fatalf("phase count = %d, want %d: %#v", len(phases), len(wantPhases), phases)
	}
	for index, want := range wantPhases {
		if phases[index].Phase != want.Phase || phases[index].State != want.State {
			t.Fatalf("phase[%d] = %#v, want %#v", index, phases[index], want)
		}
	}
	if phases[3].CurrentVersion != "1.0.0" || phases[3].CandidateVersion != "2.0.0" || phases[5].ArtifactName != "ycy-linux-x64" || phases[5].ChecksumSource != ChecksumReleaseDigest {
		t.Fatalf("safe resolution facts = %#v", phases)
	}
}

func TestRunUpgradeAlreadyCurrentAndFailureCleanup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"tag_name":"v1.0.0"}`)
	}))
	defer server.Close()
	result, err := RunUpgrade(context.Background(), UpgradeOptions{
		Resolver:   ReleaseResolverOptions{LatestURL: server.URL, CurrentVersion: "1.0.0", GOOS: "linux", GOARCH: "amd64"},
		Executable: func() (string, error) { return filepath.Join(t.TempDir(), "ycy"), nil },
	})
	if err != nil || !result.AlreadyCurrent || result.CurrentVersion != "1.0.0" {
		t.Fatalf("already current = %#v, %v", result, err)
	}

	content := []byte("new")
	failureServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/latest" {
			_, _ = io.WriteString(writer, `{"tag_name":"v2.0.0","assets":[{"name":"ycy-linux-x64","digest":"sha256:`+sha256Bytes(content)+`"}]}`)
			return
		}
		_, _ = writer.Write(content)
	}))
	defer failureServer.Close()
	directory := t.TempDir()
	target := nativeTestExecutablePath(filepath.Join(directory, "ycy"))
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	updater := expectedUpdaterPath(directory, "fail")
	_, err = RunUpgrade(context.Background(), UpgradeOptions{
		Resolver: ReleaseResolverOptions{LatestURL: failureServer.URL + "/latest", DownloadBaseURL: failureServer.URL, CurrentVersion: "1.0.0", GOOS: "linux", GOARCH: "amd64"},
		Candidate: CandidateOptions{TransactionID: func() (string, error) { return "fail", nil }, Executor: func(context.Context, string, []string, []string) (ProcessResult, error) {
			return ProcessResult{Stdout: []byte("2.0.0")}, nil
		}},
		Executable:    func() (string, error) { return target, nil },
		Copy:          func(source, destination string) error { return os.WriteFile(destination, []byte("updater"), 0o700) },
		Spawn:         func(context.Context, string, []string) error { return errors.New("spawn refused") },
		Remove:        func(path string) error { return os.Remove(path) },
		TempDirectory: func() string { return directory },
		Now:           time.Now,
		PID:           func() int { return 123 },
	})
	if err == nil || !strings.Contains(err.Error(), "spawn refused") {
		t.Fatalf("spawn failure = %v", err)
	}
	if fileExists(StatePath(target)) || fileExists(expectedTransactionPath(target, ".new.", "fail")) || fileExists(updater) {
		t.Fatal("failed scheduling left transaction files")
	}
}

func TestClassifyUpgradeErrorPreservesTheDeliberateExitContract(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "ordinary failure",
			err:      errors.New("release metadata is invalid"),
			wantCode: 1,
		},
		{
			name:     "HTTP failure remains a successful abort",
			err:      &HTTPStatusError{URL: "https://releases.example/latest", Status: http.StatusBadGateway},
			wantCode: 0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := classifyUpgradeError(testCase.err)
			var exit *ExitCodeError
			if !errors.As(err, &exit) || exit.Code != testCase.wantCode {
				t.Fatalf("classified error = %#v, want exit code %d", err, testCase.wantCode)
			}
		})
	}
}

func TestRunUpgradeReturnsPriorStateAndAbortForClassifiedFailure(t *testing.T) {
	state := testState(t, StatusSucceeded)
	if err := WriteState(state); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	result, err := RunUpgrade(context.Background(), UpgradeOptions{
		Resolver:   ReleaseResolverOptions{LatestURL: server.URL, CurrentVersion: "1.0.0", GOOS: "linux", GOARCH: "amd64"},
		Executable: func() (string, error) { return state.TargetPath, nil },
	})
	var exit *ExitCodeError
	if !result.Aborted || result.PreviousState == nil || result.PreviousState.Status != StatusSucceeded || !errors.As(err, &exit) || exit.Code != 0 {
		t.Fatalf("classified result = %#v, %v", result, err)
	}
}

func TestRunUpgradeMarksCancelledCandidateWorkWithoutLeakingTransportFacts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var phases []UpgradePhaseEvent
	result, err := RunUpgrade(ctx, UpgradeOptions{
		Resolver: ReleaseResolverOptions{
			CurrentVersion: "1.0.0",
			GOOS:           "linux",
			GOARCH:         "amd64",
			LatestURL:      "https://releases.example/private?token=upgrade-secret",
			Client: upgradeHTTPDoer(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/private" {
					cancel()
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v2.0.0","assets":[{"name":"ycy-linux-x64","digest":"sha256:` + strings.Repeat("a", 64) + `"}]}`))}, nil
				}
				return nil, request.Context().Err()
			}),
		},
		Executable: func() (string, error) { return filepath.Join(t.TempDir(), "ycy"), nil },
		Observer: UpgradeObserver{Phase: func(event UpgradePhaseEvent) {
			phases = append(phases, event)
		}},
	})
	var exit *ExitCodeError
	if !result.Aborted || !errors.Is(err, context.Canceled) || !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("cancelled result = %#v, %v", result, err)
	}
	if !containsUpgradePhaseState(phases, UpgradePhaseDownloadCandidate, UpgradePhaseCancelled) || !containsUpgradePhaseState(phases, UpgradePhaseComplete, UpgradePhaseCancelled) {
		t.Fatalf("cancellation phases = %#v", phases)
	}
	for _, event := range phases {
		if strings.Contains(event.Detail, "upgrade-secret") || strings.Contains(event.Detail, "releases.example") {
			t.Fatalf("unsafe phase event = %#v", event)
		}
	}
}

func containsUpgradePhaseState(events []UpgradePhaseEvent, phase UpgradePhase, state UpgradePhaseState) bool {
	for _, event := range events {
		if event.Phase == phase && event.State == state {
			return true
		}
	}
	return false
}

type upgradeHTTPDoer func(*http.Request) (*http.Response, error)

func (do upgradeHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	return do(request)
}

func TestConsumeStateDoesNotReadAdjacentState(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "ycy")
	legacyPath := target + ".update-state.json"
	if err := os.WriteFile(legacyPath, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConsumeState(target); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(legacyPath)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("adjacent state = %q, %v", contents, err)
	}
}
