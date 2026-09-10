package factory

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewUsesExplicitProcessFacts(t *testing.T) {
	input := bytes.NewBufferString("input")
	output := &bytes.Buffer{}
	diagnostics := &bytes.Buffer{}
	client := &http.Client{}
	now := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	lookup := func(key string) (string, bool) { return "lookup-" + key, true }
	environment := func(key string) string { return "value-" + key }
	workingDirectory := func() (string, error) { return "/workspace", nil }

	result := New(Options{
		Version: "1.2.3",
		IOStreams: cmdutil.IOStreams{
			In:     input,
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities:      terminal.Capabilities{Interaction: terminal.RichInteractive},
		Environment:       environment,
		EnvironmentLookup: lookup,
		WorkingDirectory:  workingDirectory,
		HTTPClient:        client,
		Now:               func() time.Time { return now },
	})

	if result.Version != "1.2.3" || result.IOStreams.In != input || result.IOStreams.Out != output || result.IOStreams.ErrOut != diagnostics {
		t.Fatalf("Factory process facts = %#v", result)
	}
	if got := result.Terminal.Capabilities(); got != (terminal.Capabilities{Interaction: terminal.RichInteractive}) {
		t.Fatalf("Terminal session = %#v", got)
	}
	if result.Logging == nil || result.HTTPClient != client {
		t.Fatalf("Factory shared capabilities = %#v", result)
	}
	if got := result.Environment("key"); got != "value-key" {
		t.Fatalf("Environment = %q", got)
	}
	if got, ok := result.EnvironmentLookup("key"); got != "lookup-key" || !ok {
		t.Fatalf("EnvironmentLookup = %q, %t", got, ok)
	}
	if got, err := result.WorkingDirectory(); got != "/workspace" || err != nil {
		t.Fatalf("WorkingDirectory = %q, %v", got, err)
	}
	if got := result.Now(); !got.Equal(now) {
		t.Fatalf("Now = %s, want %s", got, now)
	}
}

func TestNewDefersAndMemoizesConfigStoreAndGitRunner(t *testing.T) {
	wantStore := &appconfig.Store{}
	wantConfigErr := errors.New("config unavailable")
	wantRunner := &gitprocess.Runner{}
	configCalls := 0
	runnerCalls := 0
	result := New(Options{
		IOStreams: cmdutil.IOStreams{In: bytes.NewBuffer(nil), Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		newConfigStore: func() (*appconfig.Store, error) {
			configCalls++
			return wantStore, wantConfigErr
		},
		newGitRunner: func() *gitprocess.Runner {
			runnerCalls++
			return wantRunner
		},
	})

	if configCalls != 0 || runnerCalls != 0 {
		t.Fatalf("New performed eager construction: config=%d git=%d", configCalls, runnerCalls)
	}
	for call := 0; call < 2; call++ {
		store, err := result.ConfigStore()
		if store != wantStore || !errors.Is(err, wantConfigErr) {
			t.Fatalf("ConfigStore call %d = %p, %v", call, store, err)
		}
		if runner := result.GitRunner(); runner != wantRunner {
			t.Fatalf("GitRunner call %d = %p, want %p", call, runner, wantRunner)
		}
	}
	if configCalls != 1 || runnerCalls != 1 {
		t.Fatalf("Factory construction calls = config=%d git=%d, want one each", configCalls, runnerCalls)
	}
}

func TestNewDerivesLookupFromAnExplicitEnvironment(t *testing.T) {
	result := New(Options{
		IOStreams:   cmdutil.IOStreams{In: bytes.NewBuffer(nil), Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		Environment: func(key string) string { return map[string]string{"set": "value"}[key] },
	})
	if got, ok := result.EnvironmentLookup("set"); got != "value" || !ok {
		t.Fatalf("set EnvironmentLookup = %q, %t", got, ok)
	}
	if got, ok := result.EnvironmentLookup("missing"); got != "" || ok {
		t.Fatalf("missing EnvironmentLookup = %q, %t", got, ok)
	}
}
