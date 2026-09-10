package fork_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	rootcommand "github.com/hackycy/hackycy-cli/pkg/cmd/root"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestRootConfiguresDiagnosticsBeforeGitFork(t *testing.T) {
	archive := rootOutcomeForkArchive(t, map[string]string{
		"project-main/README.md": "fixture\n",
	})
	runtime := logging.NewRuntime(logging.Options{Writer: &bytes.Buffer{}})
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if runtime.Level() != logging.Warn {
			t.Errorf("logging level during %s = %v, want %v", request.URL.Path, runtime.Level(), logging.Warn)
		}
		if request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("Authorization during %s = %q", request.URL.Path, request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/api/v3/repos/group/project":
			_, _ = io.WriteString(response, `{"default_branch":"main"}`)
		case "/api/v3/repos/group/project/tarball/main":
			_, _ = response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configureRootOutcomeForkFixture(t, home, server.URL)
	destination := filepath.Join(t.TempDir(), "destination")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	factory := commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    stdout,
			ErrOut: stderr,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
		Environment: func(key string) string {
			switch key {
			case "HOME":
				return home
			case "USERPROFILE":
				return ""
			default:
				return os.Getenv(key)
			}
		},
	})
	runtime = logging.NewRuntime(logging.Options{Writer: stderr})
	factory.Logging = runtime
	app, err := rootcommand.New(factory)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := app.Execute(context.Background(), []string{"--log-level", "warn", "git", "fork", "fixture:group/project", destination})
	if outcome.Code != 0 || outcome.Err != nil || runtime.Level() != logging.Warn || requests != 2 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Done! Project created at") {
		t.Fatalf("outcome = %#v, level = %v, requests = %d, streams = (%q, %q)", outcome, runtime.Level(), requests, stdout.String(), stderr.String())
	}
	if contents, err := os.ReadFile(filepath.Join(destination, "README.md")); err != nil || string(contents) != "fixture\n" {
		t.Fatalf("archive contents = %q, %v", contents, err)
	}
}

func configureRootOutcomeForkFixture(t *testing.T, home, serverURL string) {
	t.Helper()
	store, err := appconfig.New(appconfig.Dependencies{
		Environment: func(key string) string {
			switch key {
			case "HOME":
				return home
			case "USERPROFILE":
				return ""
			default:
				return os.Getenv(key)
			}
		},
	})
	if err != nil {
		t.Fatalf("new appconfig store: %v", err)
	}
	if err := store.SaveForkInstance("fixture", appconfig.ForkInput{
		Host: strings.TrimPrefix(serverURL, "http://"), Scheme: "http", Type: "github", Token: "fixture-token",
	}); err != nil {
		t.Fatalf("save Fork fixture: %v", err)
	}
}

func rootOutcomeForkArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, contents := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents))}); err != nil {
			t.Fatalf("write TAR header: %v", err)
		}
		if _, err := io.WriteString(tarWriter, contents); err != nil {
			t.Fatalf("write TAR contents: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close TAR: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return compressed.Bytes()
}
