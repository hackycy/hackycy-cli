//go:build acceptance

package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportEnvStandaloneBinary(t *testing.T) {
	root := repositoryRoot(t)
	binary := standaloneBinaryOutputPath(filepath.Join(t.TempDir(), "ycy"))
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ycy")
	build.Dir = root
	build.Env = environmentWith(map[string]string{
		"CGO_ENABLED": "0",
		"GOTOOLCHAIN": "go1.26.7",
		"GOWORK":      "off",
	})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone binary: %v\n%s", err, output)
	}

	workingDirectory := t.TempDir()
	writeExportEnvFile(t, filepath.Join(workingDirectory, "named", ".env.production"), "VALUE=production\n")
	writeExportEnvFile(t, filepath.Join(workingDirectory, "ambiguous", ".env"), "BASE=base\n")
	writeExportEnvFile(t, filepath.Join(workingDirectory, "ambiguous", ".env.production"), "VALUE=production\n")
	protectedPath := filepath.Join(workingDirectory, "protected.json")
	if err := os.WriteFile(protectedPath, []byte("protected"), 0o600); err != nil {
		t.Fatalf("write protected output: %v", err)
	}
	environment := environmentWith(map[string]string{})

	output, err := runExportEnvStandalone(binary, workingDirectory, environment, "ignored\n", "export", "env", "named", "--env", "production")
	if err != nil || string(output) != "Exported variables:\n{\n  \"VALUE\": \"production\"\n}\n" {
		t.Fatalf("resolved redirected export = (%v, %q)", err, output)
	}

	output, err = runExportEnvStandalone(binary, workingDirectory, environment, "1\n", "export", "env", "ambiguous", "--out", "protected.json")
	if err == nil || string(output) != "error: export env requires an interactive terminal\n" {
		t.Fatalf("ambiguous redirected export = (%v, %q)", err, output)
	}
	contents, err := os.ReadFile(protectedPath)
	if err != nil || string(contents) != "protected" {
		t.Fatalf("ambiguous redirected export changed output target = (%v, %q)", err, contents)
	}
}

func runExportEnvStandalone(binary, directory string, environment []string, input string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = environment
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}

func writeExportEnvFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
