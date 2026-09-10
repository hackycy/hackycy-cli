package test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestCMTestModuleResolvesAndRunsTheProvider(t *testing.T) {
	resolver := &recordingCMTestResolver{profile: cmTestProfile()}
	transport := cmTestTransportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}, nil
	})
	module := newCMTestModule(t, resolver, transport)

	result, err := module.Run(context.Background(), TestRequest{Profile: "work"})
	if err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	if resolver.options != (appconfig.CMResolveOptions{ProfileName: "work"}) {
		t.Fatalf("resolver options = %#v", resolver.options)
	}
	if result != (TestResult{Content: "ok"}) {
		t.Fatalf("Run() = %#v", result)
	}
}

func TestCMTestModuleReturnsOnlySafeFailureDiagnostics(t *testing.T) {
	profile := cmTestProfile()
	failure := errors.New("provider rejected test-api-key")
	transport := cmTestTransportFunc(func(*http.Request) (*http.Response, error) {
		return nil, failure
	})
	module := newCMTestModule(t, &recordingCMTestResolver{profile: profile}, transport)

	result, err := module.Run(context.Background(), TestRequest{})
	if !errors.Is(err, failure) || strings.Contains(err.Error(), profile.APIKey) || result.Diagnostic == nil {
		t.Fatalf("Run() = (%#v, %v), want redacted provider failure", result, err)
	}
	if got, want := *result.Diagnostic, (TestDiagnostic{Provider: profile.Name, BaseURL: profile.BaseURL, Model: profile.Model}); got != want || result.Content != "" {
		t.Fatalf("Run() = %#v, want safe diagnostic %#v", result, want)
	}
}

func TestCMTestModuleDoesNotReturnAResultForResolverFailure(t *testing.T) {
	failure := errors.New("No usable CM profile found")
	module := newCMTestModule(t, &recordingCMTestResolver{err: failure}, cmTestTransportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport was called after a resolution failure")
		return nil, nil
	}))

	result, err := module.Run(context.Background(), TestRequest{})
	if !errors.Is(err, failure) || result != (TestResult{}) {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
}

func TestCMTestModuleRedactsTheProfileAPIKeyFromSuccessfulProviderContent(t *testing.T) {
	profile := cmTestProfile()
	transport := cmTestTransportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"received test-api-key"}}]}`))}, nil
	})
	module := newCMTestModule(t, &recordingCMTestResolver{profile: profile}, transport)

	result, err := module.Run(context.Background(), TestRequest{})
	if err != nil || strings.Contains(result.Content, profile.APIKey) || result.Content != "received [REDACTED]" {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
}

func TestNewCMTestModuleRequiresEachCommandOwnedAdapter(t *testing.T) {
	resolver := &recordingCMTestResolver{profile: cmTestProfile()}
	transport := cmTestTransportFunc(func(*http.Request) (*http.Response, error) { return nil, nil })
	for _, dependencies := range []TestDependencies{
		{Transport: transport},
		{Resolver: resolver},
	} {
		if _, err := NewTest(dependencies); err == nil {
			t.Fatalf("NewTest(%#v) returned nil error", dependencies)
		}
	}
}

func newCMTestModule(t *testing.T, resolver TestProfileResolver, transport cmTestProviderTransport) *TestModule {
	t.Helper()
	module, err := NewTest(TestDependencies{Resolver: resolver, Transport: transport})
	if err != nil {
		t.Fatalf("NewTest() returned an error: %v", err)
	}
	return module
}

type recordingCMTestResolver struct {
	profile appconfig.ResolvedCMProfile
	options appconfig.CMResolveOptions
	err     error
}

func (resolver *recordingCMTestResolver) ResolveCMProfile(options appconfig.CMResolveOptions) (appconfig.ResolvedCMProfile, error) {
	resolver.options = options
	return resolver.profile, resolver.err
}
