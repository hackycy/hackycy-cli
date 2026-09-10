//go:build acceptance

package acceptance

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigForkListStandaloneBinary(t *testing.T) {
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

	home := t.TempDir()
	configDirectory := filepath.Join(home, ".ycy-cli")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	const ciphertext = "MDEyMzQ1Njc4OWFiY2RlZg==:tag:ciphertext"
	config := `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "fork": {"instances": {
    "work": {"host": "gitlab.example", "type": "gitlab", "token": "MDEyMzQ1Njc4OWFiY2RlZg==:tag:ciphertext"},
    "personal": {"host": "github.example", "scheme": "http", "type": "github", "token": "QWVy:second:ciphertext"}
  }}
}`
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	environment := environmentWith(map[string]string{"HOME": home, "USERPROFILE": ""})

	output, err := runStandalone(binary, environment, "config", "fork", "list")
	if err != nil {
		t.Fatalf("config fork list: %v\n%s", err, output)
	}
	for _, field := range []string{"work", "gitlab", "https", "gitlab.example", "MDEy***", "personal", "github", "http", "github.example", "QWVy***"} {
		if !strings.Contains(string(output), field) {
			t.Fatalf("list output missing %q: %q", field, output)
		}
	}
	if strings.Index(string(output), "work") > strings.Index(string(output), "personal") {
		t.Fatalf("list output did not preserve stored order: %q", output)
	}
	if strings.Contains(string(output), ciphertext) {
		t.Fatalf("list output exposed the full ciphertext: %q", output)
	}

	helpOutput, err := runStandalone(binary, environment, "config", "fork", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "list") || !strings.Contains(string(helpOutput), "add") || !strings.Contains(string(helpOutput), "remove") {
		t.Fatalf("fork help = (%v, %q)", err, helpOutput)
	}
	missingOutput, err := runStandalone(binary, environment, "config", "fork", "remove")
	if err == nil || string(missingOutput) != "error: config fork remove requires an interactive terminal\n" {
		t.Fatalf("Automation removal = (%v, %q)", err, missingOutput)
	}
}

func TestConfigForkAddStandaloneBinary(t *testing.T) {
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

	home := t.TempDir()
	environment := environmentWith(map[string]string{"HOME": home, "USERPROFILE": ""})
	output, err := runStandaloneWithInput(binary, environment, "work\nhttps://gitlab.example/path\n1\n2\nsecret-token\n", "config", "fork", "add")
	if err == nil || string(output) != "error: config fork add requires an interactive terminal\n" || strings.Contains(string(output), "secret-token") {
		t.Fatalf("Automation config fork add = (%v, %q)", err, output)
	}
	if _, err := os.Stat(filepath.Join(home, ".ycy-cli", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("Automation config fork add wrote configuration: %v", err)
	}
}

func TestConfigForkRemoveStandaloneBinary(t *testing.T) {
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

	home := t.TempDir()
	environment := environmentWith(map[string]string{"HOME": home, "USERPROFILE": ""})
	configPath := writeForkRemoveConfig(t, home)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	output, err := runStandaloneWithInput(binary, environment, "1\ny\n", "config", "fork", "remove")
	if err == nil || string(output) != "error: config fork remove requires an interactive terminal\n" {
		t.Fatalf("Automation removal = (%v, %q)", err, output)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("Automation removal changed config = (%v, %q)", err, after)
	}

	emptyHome := t.TempDir()
	emptyEnvironment := environmentWith(map[string]string{"HOME": emptyHome, "USERPROFILE": ""})
	emptyOutput, err := runStandalone(binary, emptyEnvironment, "config", "fork", "remove")
	if err != nil || string(emptyOutput) != "Nothing to remove\n" {
		t.Fatalf("empty removal = (%v, %q)", err, emptyOutput)
	}
	if _, err := os.Stat(filepath.Join(emptyHome, ".ycy-cli", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("empty removal created config: %v", err)
	}

	helpOutput, err := runStandalone(binary, environment, "config", "fork", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "list") || !strings.Contains(string(helpOutput), "add") || !strings.Contains(string(helpOutput), "remove") {
		t.Fatalf("fork help = (%v, %q)", err, helpOutput)
	}
}

func writeForkRemoveConfig(t *testing.T, home string) string {
	t.Helper()
	directory := filepath.Join(home, ".ycy-cli")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	path := filepath.Join(directory, "config.json")
	contents := `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "fork": {"instances": {
    "work": {"host": "gitlab.example", "type": "gitlab", "token": "MDEyMzQ1Njc4OWFiY2RlZg==:tag:ciphertext"},
    "keep": {"host": "github.example", "type": "github", "token": "QWVy:second:ciphertext"}
  }}
}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return path
}
