//go:build acceptance

package acceptance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStandaloneBinaryRejectsRedirectedSelectionAndPreservesParserBehavior(t *testing.T) {
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

	project := t.TempDir()
	writeStandaloneRunFile(t, project, "package.json", `{"scripts":{"check":"echo check"}}`)
	environment := environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""})

	output, err := runRunStandalone(binary, project, environment, "1\n1\n", "run")
	if err == nil || string(output) != "error: run requires an interactive terminal\n" {
		t.Fatalf("redirected run = (%v, %q)", err, output)
	}

	for _, arguments := range [][]string{{"run", ".", "--flag"}, {"run", "--flag", "value"}, {"run", "arg1", "arg2"}, {"run", "--", "arg1", "arg2"}} {
		output, err = runRunStandalone(binary, project, environment, "", arguments...)
		if exitCode(err) != 1 || !strings.Contains(string(output), "accepts at most 1 arg(s)") {
			t.Fatalf("arguments %q = (%v, %q)", arguments, err, output)
		}
	}

	output, err = runRunStandalone(binary, project, environment, "", "run", "--help")
	if err != nil || !strings.Contains(string(output), "Run package.json scripts") {
		t.Fatalf("run help = (%v, %q)", err, output)
	}
}

func writeStandaloneRunFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create package directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func runRunStandalone(binary, directory string, environment []string, input string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Dir = directory
	command.Env = environment
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}
