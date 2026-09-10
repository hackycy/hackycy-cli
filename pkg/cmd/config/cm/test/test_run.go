package test

import (
	"context"
	"errors"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

// TestRequest is the typed CLI request for config cm test.
type TestRequest struct {
	Profile string
}

// TestResult records the secret-safe provider response reported by config cm test.
type TestResult struct {
	Content    string
	Diagnostic *TestDiagnostic
	usage      *cmTestTokenUsage
}

// TestProfileResolver is the appconfig boundary used by config cm test.
type TestProfileResolver interface {
	ResolveCMProfile(appconfig.CMResolveOptions) (appconfig.ResolvedCMProfile, error)
}

// TestDiagnostic is the profile projection safe for a failed provider test.
type TestDiagnostic struct {
	Provider string
	BaseURL  string
	Model    string
}

// TestDependencies are the command-owned adapters for config cm test.
type TestDependencies struct {
	Resolver  TestProfileResolver
	Transport cmTestProviderTransport
}

// TestModule owns config cm test behavior behind its typed command interface.
type TestModule struct {
	resolver  TestProfileResolver
	transport cmTestProviderTransport
}

// NewTest constructs a config cm test command module.
func NewTest(dependencies TestDependencies) (*TestModule, error) {
	if dependencies.Resolver == nil {
		return nil, errors.New("config cm test resolver is required")
	}
	if dependencies.Transport == nil {
		return nil, errors.New("config cm test transport is required")
	}
	return &TestModule{
		resolver:  dependencies.Resolver,
		transport: dependencies.Transport,
	}, nil
}

// Run resolves one profile, tests it, and returns only secret-safe outcomes.
func (module *TestModule) Run(ctx context.Context, request TestRequest) (TestResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	profile, err := module.resolveProfile(request)
	if err != nil {
		return TestResult{}, err
	}
	return module.testProvider(ctx, profile)
}

func (module *TestModule) resolveProfile(request TestRequest) (appconfig.ResolvedCMProfile, error) {
	return module.resolver.ResolveCMProfile(appconfig.CMResolveOptions{ProfileName: request.Profile})
}

func (module *TestModule) testProvider(context context.Context, profile appconfig.ResolvedCMProfile) (TestResult, error) {
	providerResult, err := executeCMTestProvider(context, profile, module.transport)
	if err != nil {
		diagnostic := &TestDiagnostic{
			Provider: redactCMTestText(profile.Name, profile.APIKey),
			BaseURL:  redactCMTestText(safeCMTestURL(profile.BaseURL), profile.APIKey),
			Model:    redactCMTestText(profile.Model, profile.APIKey),
		}
		return TestResult{Diagnostic: diagnostic}, redactCMTestError(err, profile.APIKey)
	}
	content := redactCMTestText(providerResult.Content, profile.APIKey)
	return TestResult{Content: content, usage: providerResult.Usage}, nil
}

func redactCMTestError(err error, apiKey string) error {
	message := redactCMTestText(err.Error(), apiKey)
	if message == err.Error() {
		return err
	}
	return cmTestRedactedError{source: err, message: message}
}

func redactCMTestText(value, apiKey string) string {
	if apiKey == "" {
		return value
	}
	return strings.ReplaceAll(value, apiKey, "[REDACTED]")
}

type cmTestRedactedError struct {
	source  error
	message string
}

func (err cmTestRedactedError) Error() string {
	return err.message
}

func (err cmTestRedactedError) Unwrap() error {
	return err.source
}
