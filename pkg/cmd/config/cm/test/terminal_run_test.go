package test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunTestPlainSuccessTracksBothPhasesAndWritesOneResult(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	resolver := &recordingCMTestResolver{profile: cmTestProfile()}
	requests := 0
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runTest(&Options{
		Context: context.Background(),
		Profile: "work",
		Store:   func() (TestProfileResolver, error) { return resolver, nil },
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}, nil
		}),
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("runTest() error = %v", err)
	}
	if got, want := stdout.String(), "Commit message provider test\nResponse:\nok\nDone\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	for _, expected := range []string{
		"Resolve CM test profile",
		"Resolving CM test profile...",
		"Test CM provider",
		"waiting for response",
		"Response received",
	} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("stderr = %q, missing %q", stderr.String(), expected)
		}
	}
	if requests != 1 || resolver.options.ProfileName != "work" {
		t.Fatalf("resolver/request counts = (%d, %#v)", requests, resolver.options)
	}
	if terminaltest.ContainsTerminalControl(append(stdout.Bytes(), stderr.Bytes()...)) {
		t.Fatalf("plain output contains terminal controls: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunTestAutomationIsSilentExceptForTheUnchangedResult(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runTest(&Options{
		Context: context.Background(),
		Store:   func() (TestProfileResolver, error) { return &recordingCMTestResolver{profile: cmTestProfile()}, nil },
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}, nil
		}),
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("runTest() error = %v", err)
	}
	if got, want := stdout.String(), "Commit message provider test\nResponse:\nok\nDone\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("Automation stderr = %q, want silence", stderr.String())
	}
}

func TestRunTestUsesTheUnchangedResultWhenRichStdoutIsRedirected(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdout:      terminalexperience.StreamCapability{Terminal: false},
		},
		Output:      stdout,
		Diagnostics: stderr,
	})
	err := runTest(&Options{
		Context: context.Background(),
		Store:   func() (TestProfileResolver, error) { return &recordingCMTestResolver{profile: cmTestProfile()}, nil },
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))}, nil
		}),
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("runTest() error = %v", err)
	}
	if got, want := stdout.String(), "Commit message provider test\nResponse:\nok\nDone\n"; got != want {
		t.Fatalf("redirected stdout = %q, want %q", got, want)
	}
	for _, forbidden := range []string{"YCY / config cm test", "Prompt tokens", "\x1b["} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("redirected stdout leaked Rich-only content %q: %q", forbidden, stdout.String())
		}
	}
}

func TestRunTestResolverFailureDoesNotCreateProviderPhaseOrRequest(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	failure := errors.New("No usable CM profile found")
	storeCalls, requests := 0, 0
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runTest(&Options{
		Context: context.Background(),
		Store: func() (TestProfileResolver, error) {
			storeCalls++
			return &recordingCMTestResolver{err: failure}, nil
		},
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, nil
		}),
		Terminal: experience,
	})
	if !errors.Is(err, failure) {
		t.Fatalf("runTest() error = %v, want resolver failure", err)
	}
	if stdout.Len() != 0 || requests != 0 || storeCalls != 1 {
		t.Fatalf("resolver failure side effects = stdout=%q requests=%d storeCalls=%d", stdout.String(), requests, storeCalls)
	}
	if !strings.Contains(stderr.String(), "Unable to resolve CM test profile (selection)") {
		t.Fatalf("stderr = %q, missing safe resolver phase", stderr.String())
	}
}

func TestRunTestProviderFailureUsesSafeProjectionAndCategory(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	profile := cmTestProfile()
	profile.Name = "work\nprofile"
	profile.BaseURL = "https://user:test-api-key@example.test/v1?token=hidden#fragment"
	profile.Model = "model\x1b[31m"
	const responseSecret = "response-secret"
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runTest(&Options{
		Context: context.Background(),
		Store:   func() (TestProfileResolver, error) { return &recordingCMTestResolver{profile: profile}, nil },
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Upstream", Body: io.NopCloser(strings.NewReader(`{"error":"` + responseSecret + `"}`))}, nil
		}),
		Terminal: experience,
	})
	if err == nil || !strings.Contains(err.Error(), "502 Upstream") || strings.Contains(err.Error(), responseSecret) || strings.Contains(err.Error(), "test-api-key") {
		t.Fatalf("runTest() error = %v, want safe HTTP failure", err)
	}
	for _, forbidden := range []string{"token=hidden", "fragment", responseSecret, "test-api-key"} {
		if strings.Contains(stdout.String()+stderr.String(), forbidden) {
			t.Fatalf("provider failure leaked %q: stdout=%q stderr=%q", forbidden, stdout.String(), stderr.String())
		}
	}
	for _, expected := range []string{"Provider request failed", "Provider: work profile", "Base URL: https://example.test/v1", "Model: model"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), expected)
		}
	}
	if !strings.Contains(stderr.String(), "http-status") {
		t.Fatalf("stderr = %q, missing stable category", stderr.String())
	}
}

func TestRunTestRedactsKnownAPIKeysFromPhaseProjections(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	profile := cmTestProfile()
	profile.Name = "profile-" + profile.APIKey
	profile.BaseURL = "https://provider.test/v1/" + profile.APIKey
	profile.Model = "model-" + profile.APIKey
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runTest(&Options{
		Context: context.Background(),
		Store:   func() (TestProfileResolver, error) { return &recordingCMTestResolver{profile: profile}, nil },
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}, nil
		}),
		Terminal: experience,
	})
	if err != nil {
		t.Fatalf("runTest() error = %v", err)
	}
	if stream := stdout.String() + stderr.String(); strings.Contains(stream, profile.APIKey) || !strings.Contains(stream, "[REDACTED]") {
		t.Fatalf("phase projection = %q, want API-key-safe values", stream)
	}
}

func TestRunTestKeepsTheProviderOutcomeWhenPhasePresentationFails(t *testing.T) {
	stdout := &bytes.Buffer{}
	presentationFailure := errors.New("phase presentation failed")
	requests := 0
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  &failingCMTestWriter{err: presentationFailure},
	})
	err := runTest(&Options{
		Context: context.Background(),
		Store:   func() (TestProfileResolver, error) { return &recordingCMTestResolver{profile: cmTestProfile()}, nil },
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}, nil
		}),
		Terminal: experience,
	})
	if !errors.Is(err, presentationFailure) || requests != 1 {
		t.Fatalf("runTest() = (%v, requests=%d), want preserved provider success", err, requests)
	}
	if got, want := stdout.String(), "Commit message provider test\nResponse:\nok\nDone\n"; got != want {
		t.Fatalf("stdout = %q, want successful provider result %q", got, want)
	}
}

func TestRunTestKeepsProviderFailureWhenPhasePresentationAlsoFails(t *testing.T) {
	stdout := &bytes.Buffer{}
	presentationFailure := errors.New("phase presentation failed")
	providerFailure := errors.New("provider exchange failed")
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  &failingCMTestWriter{err: presentationFailure},
	})
	err := runTest(&Options{
		Context: context.Background(),
		Store:   func() (TestProfileResolver, error) { return &recordingCMTestResolver{profile: cmTestProfile()}, nil },
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			return nil, providerFailure
		}),
		Terminal: experience,
	})
	if !errors.Is(err, presentationFailure) || !errors.Is(err, providerFailure) {
		t.Fatalf("runTest() error = %v, want both presentation and provider failures", err)
	}
	if !strings.Contains(stdout.String(), "Provider request failed") || strings.Contains(stdout.String(), "Done") {
		t.Fatalf("stdout = %q, want failed provider result only", stdout.String())
	}
}

func TestRunTestReturnsOneOutputWriteFailureWithoutRetryingTheProvider(t *testing.T) {
	outputFailure := errors.New("stdout failed")
	stdout := &failingCMTestWriter{err: outputFailure}
	requests := 0
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       stdout,
		Diagnostics:  &bytes.Buffer{},
	})
	err := runTest(&Options{
		Context: context.Background(),
		Store:   func() (TestProfileResolver, error) { return &recordingCMTestResolver{profile: cmTestProfile()}, nil },
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}, nil
		}),
		Terminal: experience,
	})
	if !errors.Is(err, outputFailure) || requests != 1 || stdout.writes != 1 {
		t.Fatalf("runTest() = (%v, requests=%d, writes=%d), want one output attempt after one provider request", err, requests, stdout.writes)
	}
}

func TestRunTestCancellationBeforeResolutionDoesNoStoreOrProviderWork(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	storeCalled := false
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runTest(&Options{
		Context: ctx,
		Store: func() (TestProfileResolver, error) {
			storeCalled = true
			return &recordingCMTestResolver{profile: cmTestProfile()}, nil
		},
		HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
			t.Fatal("provider was called after cancellation")
			return nil, nil
		}),
		Terminal: experience,
	})
	if !errors.Is(err, context.Canceled) || storeCalled || stdout.Len() != 0 {
		t.Fatalf("cancelled run = (%v, %t, %q)", err, storeCalled, stdout.String())
	}
	if !strings.Contains(stderr.String(), "Cancelled while resolving CM test profile") {
		t.Fatalf("stderr = %q, missing cancellation phase", stderr.String())
	}
}

func TestRunTestCancellationDuringResolutionReturnsBeforeProviderStarts(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver := &blockingCMTestResolver{
		profile:  cmTestProfile(),
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	providerStarted := make(chan struct{}, 1)
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	completed := make(chan error, 1)
	go func() {
		completed <- runTest(&Options{
			Context: ctx,
			Store:   func() (TestProfileResolver, error) { return resolver, nil },
			HTTP: cmTestHTTPClient(func(*http.Request) (*http.Response, error) {
				providerStarted <- struct{}{}
				return nil, errors.New("provider must not start after resolver cancellation")
			}),
			Terminal: experience,
		})
	}()

	select {
	case <-resolver.started:
	case <-time.After(time.Second):
		t.Fatal("resolver did not start")
	}
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runTest() error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runTest() did not return after context cancellation")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Cancelled while resolving CM test profile") {
		t.Fatalf("cancelled streams = stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	close(resolver.release)
	select {
	case <-resolver.finished:
	case <-time.After(time.Second):
		t.Fatal("resolver did not finish after release")
	}
	select {
	case <-providerStarted:
		t.Fatal("provider started after resolution cancellation")
	default:
	}
}

func TestCMTestSafeProjectionsRemoveSecretsAndBoundResponse(t *testing.T) {
	if got := safeCMTestURL("https://user:secret@example.test/v1?token=hidden#fragment"); got != "https://example.test/v1" {
		t.Fatalf("safeCMTestURL() = %q", got)
	}
	if got := safeCMTestURL("file:///tmp/provider"); got != "Configured provider" {
		t.Fatalf("unsafe safeCMTestURL() = %q", got)
	}
	if got := safeCMTestProfile("bad\nprofile"); got != "bad profile" {
		t.Fatalf("safeCMTestProfile() = %q", got)
	}
	response := "line one\nline two\x1b[2K"
	if got := safeCMTestResponse(response); got != "line one\nline two" {
		t.Fatalf("safeCMTestResponse() = %q", got)
	}
	if got := safeCMTestResponse(strings.Repeat("x", cmTestResponseLimit+1)); !strings.HasSuffix(got, "... [truncated]") {
		t.Fatalf("bounded response = %q", got)
	}
}

func TestCMTestPhaseSinkUsesOneCompleteDeclaredWorkCatalog(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	sink := newCMTestPhaseSink(experience.Open(context.Background()), func() {})
	sink.begin(cmTestResolvePhaseID, cmTestResolvePhaseName, "Resolving")
	if err := sink.end(terminalexperience.PhaseCompleted, "Profile: work"); err != nil {
		t.Fatalf("resolve phase = %v", err)
	}
	sink.begin(cmTestProviderPhaseID, cmTestProviderPhaseName, "waiting")
	if err := sink.end(terminalexperience.PhaseFailed, "Provider request failed (decode)"); err != nil {
		t.Fatalf("provider phase = %v", err)
	}
	if err := sink.close(); err != nil {
		t.Fatalf("close catalog = %v", err)
	}
	operations := experience.Run.Operations()
	if len(operations) != 1 {
		t.Fatalf("operations = %#v", operations)
	}
	operation := operations[0].Value.(terminalexperience.TrackedOperation)
	if got, want := operation.ID, "config-cm-test"; got != want {
		t.Fatalf("operation ID = %q, want %q", got, want)
	}
	if got, want := operation.Phases, []terminalexperience.PhaseDefinition{
		{ID: cmTestResolvePhaseID, Name: cmTestResolvePhaseName},
		{ID: cmTestProviderPhaseID, Name: cmTestProviderPhaseName},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("phase catalog = %#v, want %#v", got, want)
	}
	var updates []terminalexperience.OperationPhase
	for update := range operation.Updates {
		updates = append(updates, update)
	}
	if got, want := updates, []terminalexperience.OperationPhase{
		{ID: cmTestResolvePhaseID, State: terminalexperience.PhaseActive, Detail: "Resolving"},
		{ID: cmTestResolvePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Profile: work"},
		{ID: cmTestProviderPhaseID, State: terminalexperience.PhaseActive, Detail: "waiting"},
		{ID: cmTestProviderPhaseID, State: terminalexperience.PhaseFailed, Detail: "Provider request failed (decode)"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("phase updates = %#v, want %#v", got, want)
	}
}

func TestCMTestPhaseSinkPassesTheCommandCancellationCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	experience := terminaltest.NewRecordingExperience()
	sink := newCMTestPhaseSink(experience.Open(ctx), cancel)
	sink.begin(cmTestResolvePhaseID, cmTestResolvePhaseName, "Resolving")
	if err := sink.end(terminalexperience.PhaseCompleted, "Profile: work"); err != nil {
		t.Fatalf("resolve phase = %v", err)
	}
	if err := sink.close(); err != nil {
		t.Fatalf("close catalog = %v", err)
	}
	operations := experience.Run.Operations()
	if len(operations) != 1 {
		t.Fatalf("operations = %#v", operations)
	}
	operation := operations[0].Value.(terminalexperience.TrackedOperation)
	if operation.RequestCancel == nil {
		t.Fatal("tracked operation omitted its command cancellation callback")
	}
	operation.RequestCancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("tracked cancellation callback did not cancel the command context")
	}
}

func cmTestHTTPClient(fn func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: cmTestRoundTripperFunc(fn)}
}

type cmTestRoundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTripper cmTestRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

type blockingCMTestResolver struct {
	profile  appconfig.ResolvedCMProfile
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

type failingCMTestWriter struct {
	err    error
	writes int
}

func (writer *failingCMTestWriter) Write([]byte) (int, error) {
	writer.writes++
	return 0, writer.err
}

func (resolver *blockingCMTestResolver) ResolveCMProfile(appconfig.CMResolveOptions) (appconfig.ResolvedCMProfile, error) {
	close(resolver.started)
	<-resolver.release
	close(resolver.finished)
	return resolver.profile, nil
}
