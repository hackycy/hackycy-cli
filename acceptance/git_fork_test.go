//go:build acceptance

package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestGitForkStandaloneBinaryDownloadsALocalProviderArchive(t *testing.T) {
	archive := acceptanceGitForkFixtureArchive(t, map[string]string{"project-main/README.md": "standalone archive\n"})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
	configureAcceptanceGitForkFixture(t, home, server.URL)
	binary := standaloneBinaryOutputPath(filepath.Join(t.TempDir(), "ycy"))
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	build.Dir = repositoryRoot(t)
	build.Env = environmentWith(map[string]string{"CGO_ENABLED": "0", "GOTOOLCHAIN": "go1.26.7", "GOWORK": "off"})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}
	destination := filepath.Join(t.TempDir(), "destination")
	command := exec.Command(resolveStandaloneBinary(binary), "git", "fork", "fixture:group/project", destination)
	command.Dir = t.TempDir()
	command.Env = environmentWith(map[string]string{"HOME": home, "USERPROFILE": ""})
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Done! Project created at") || strings.Contains(string(output), "fixture-token") {
		t.Fatalf("standalone git fork = (%v, %q)", err, output)
	}
	if contents, err := os.ReadFile(filepath.Join(destination, "README.md")); err != nil || string(contents) != "standalone archive\n" {
		t.Fatalf("standalone archive contents = %q, %v", contents, err)
	}
}

func configureAcceptanceGitForkFixture(t *testing.T, home, serverURL string) {
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

func acceptanceGitForkFixtureArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, contents := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents))}); err != nil {
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
