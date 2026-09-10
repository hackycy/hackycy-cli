//go:build acceptance

package acceptance

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestZIPStandaloneBinaryRejectsRedirectedPlanningAndPreservesHelp(t *testing.T) {
	repository := repositoryRoot(t)
	binary := standaloneBinaryOutputPath(filepath.Join(t.TempDir(), "ycy"))
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	build.Dir = repository
	build.Env = environmentWith(map[string]string{
		"CGO_ENABLED": "0",
		"GOTOOLCHAIN": "go1.26.7",
		"GOWORK":      "off",
	})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}

	project := filepath.Join(t.TempDir(), "project")
	writeStandaloneZIPFile(t, project, "package.json", `{"name":"project","devDependencies":{"vite":"1"}}`)
	writeStandaloneZIPFile(t, project, "dist/index.html", "<main />")
	environment := environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""})
	command := exec.Command(resolveStandaloneBinary(binary), "zip", ".", "--without-open", "--with-dir", "bundle")
	command.Dir = project
	command.Env = environment
	command.Stdin = strings.NewReader("\n\n\n")
	output, err := command.CombinedOutput()
	if err == nil || string(output) != "error: zip requires an interactive terminal\n" {
		t.Fatalf("redirected zip = (%v, %q)", err, output)
	}
	archivePath := filepath.Join(project, "dist", "project.zip")
	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("redirected zip created archive: %v", err)
	}

	command = exec.Command(resolveStandaloneBinary(binary), "zip", "--help")
	command.Dir = project
	command.Env = environment
	output, err = command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Zip a directory into a zip file") || !strings.Contains(string(output), "--without-open") {
		t.Fatalf("zip help = (%v, %q)", err, output)
	}
}

func writeStandaloneZIPFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
