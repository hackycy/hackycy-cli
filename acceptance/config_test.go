//go:build acceptance

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCMListStandaloneBinary(t *testing.T) {
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
	const secret = "api-key-that-must-not-escape"
	config := `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "cm": {"defaultProfile": "personal", "profiles": {
    "work": {"baseURL": "https://work.example/v1", "model": "gpt-4.1-mini", "apiKey": "` + secret + `"},
    "personal": {"baseURL": "https://personal.example/v1", "model": "deepseek-chat", "apiKey": "another-secret"}
  }}
}`
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	environment := environmentWith(map[string]string{"HOME": home, "USERPROFILE": ""})

	output, err := runStandalone(binary, environment, "config", "cm", "list")
	if err != nil {
		t.Fatalf("config cm list: %v\n%s", err, output)
	}
	for _, field := range []string{"work", "gpt-4.1-mini", "https://work.example/v1", "personal", "deepseek-chat", "https://personal.example/v1", "* personal"} {
		if !strings.Contains(string(output), field) {
			t.Fatalf("list output missing %q: %q", field, output)
		}
	}
	if strings.Index(string(output), "work") > strings.Index(string(output), "personal") {
		t.Fatalf("list output did not preserve stored order: %q", output)
	}
	if strings.Contains(string(output), secret) || strings.Contains(string(output), "another-secret") {
		t.Fatalf("list output exposed an API key: %q", output)
	}

	helpOutput, err := runStandalone(binary, environment, "config", "cm", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "list") || !strings.Contains(string(helpOutput), "add") || !strings.Contains(string(helpOutput), "use") || !strings.Contains(string(helpOutput), "set") || !strings.Contains(string(helpOutput), "remove") || !strings.Contains(string(helpOutput), "test") {
		t.Fatalf("cm help = (%v, %q)", err, helpOutput)
	}
}

func TestConfigCMAddStandaloneBinary(t *testing.T) {
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
	const apiKey = "api-key-that-must-not-escape"
	output, err := runStandaloneWithInput(binary, environment, "work\n https://provider.example/v1/// \n gpt-4.1-mini \n"+apiKey+"\n", "config", "cm", "add")
	if err == nil || string(output) != "error: config cm add requires an interactive terminal\n" || strings.Contains(string(output), apiKey) {
		t.Fatalf("Automation config cm add = (%v, %q)", err, output)
	}
	if _, err := os.Stat(filepath.Join(home, ".ycy-cli", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("Automation config cm add wrote configuration: %v", err)
	}
}

type standaloneCMConfigDocument struct {
	CM struct {
		DefaultProfile string                             `json:"defaultProfile"`
		Profiles       map[string]standaloneCMProfileData `json:"profiles"`
	} `json:"cm"`
}

type standaloneCMProfileData struct {
	BaseURL         string  `json:"baseURL"`
	Model           string  `json:"model"`
	APIKey          string  `json:"apiKey"`
	Temperature     float64 `json:"temperature"`
	TimeoutMS       int     `json:"timeoutMs"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

func standaloneCMConfig(t *testing.T, contents []byte) standaloneCMConfigDocument {
	t.Helper()
	var document standaloneCMConfigDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return document
}

func standaloneCMProfile(t *testing.T, contents []byte, name string) standaloneCMProfileData {
	t.Helper()
	document := standaloneCMConfig(t, contents)
	profile, found := document.CM.Profiles[name]
	if !found {
		t.Fatalf("config omitted %q: %q", name, contents)
	}
	return profile
}

func TestConfigCMSetStandaloneBinary(t *testing.T) {
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
	const originalAPIKey = "initial-api-key-that-must-not-escape"
	const rotatedAPIKey = "rotated-api-key-that-must-not-escape"
	config := `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "fork": {"instances": {"github": {"host": "github.com", "type": "github", "token": "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ=="}}},
  "cm": {"defaultProfile": "work", "profiles": {
    "work": {"baseURL": "https://old.example/v1", "model": "old-model", "apiKey": "` + originalAPIKey + `", "temperature": 0.2, "timeoutMs": 300000, "maxOutputTokens": 1000}
  }}
}`
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	environment := environmentWith(map[string]string{"HOME": home, "USERPROFILE": ""})

	for _, update := range []struct {
		key   string
		value string
	}{
		{key: "baseURL", value: " https://provider.example/v2/// "},
		{key: "model", value: " next-model "},
		{key: "apiKey", value: rotatedAPIKey},
		{key: "temperature", value: "0b1"},
		{key: "timeoutMs", value: "1000suffix"},
		{key: "maxOutputTokens", value: "32.9"},
	} {
		output, err := runStandalone(binary, environment, "config", "cm", "set", "work", update.key, update.value)
		if err != nil || !strings.Contains(string(output), "Profile work updated") || strings.Contains(string(output), update.value) {
			t.Fatalf("config cm set %s = (%v, %q)", update.key, err, output)
		}
	}

	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	profile := standaloneCMProfile(t, contents, "work")
	if profile.BaseURL != "https://provider.example/v2" || profile.Model != "next-model" || profile.APIKey == "" || profile.APIKey == rotatedAPIKey || strings.Count(profile.APIKey, ":") != 2 || profile.Temperature != 1 || profile.TimeoutMS != 1000 || profile.MaxOutputTokens != 32 {
		t.Fatalf("persisted profile = %#v", profile)
	}
	if strings.Contains(string(contents), originalAPIKey) || strings.Contains(string(contents), rotatedAPIKey) {
		t.Fatalf("config contains plaintext API key: %q", contents)
	}
	if document := standaloneCMConfig(t, contents); document.CM.DefaultProfile != "work" {
		t.Fatalf("default profile = %q, want work", document.CM.DefaultProfile)
	}

	listOutput, err := runStandalone(binary, environment, "config", "cm", "list")
	if err != nil || !strings.Contains(string(listOutput), "* work") || !strings.Contains(string(listOutput), "next-model") || strings.Contains(string(listOutput), rotatedAPIKey) || strings.Contains(string(listOutput), profile.APIKey) {
		t.Fatalf("config cm list after set = (%v, %q)", err, listOutput)
	}
	for _, arguments := range [][]string{
		{"config", "cm", "set", "missing", "model", "next"},
		{"config", "cm", "set", "work", "unsupported", "value"},
		{"config", "cm", "set", "work", "temperature", "2.1"},
	} {
		output, err := runStandalone(binary, environment, arguments...)
		if err == nil || !strings.Contains(string(output), "error:") || strings.Contains(string(output), "Profile work updated") || strings.Contains(string(output), rotatedAPIKey) {
			t.Fatalf("%q = (%v, %q)", arguments, err, output)
		}
	}

	helpOutput, err := runStandalone(binary, environment, "config", "cm", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "list") || !strings.Contains(string(helpOutput), "add") || !strings.Contains(string(helpOutput), "use") || !strings.Contains(string(helpOutput), "set") || !strings.Contains(string(helpOutput), "remove") || !strings.Contains(string(helpOutput), "test") {
		t.Fatalf("cm help = (%v, %q)", err, helpOutput)
	}
}

func TestConfigCMUseStandaloneBinary(t *testing.T) {
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
	const apiKey = "api-key-that-must-not-escape"
	config := `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "fork": {"instances": {"github": {"host": "github.com", "type": "github", "token": "fork-token"}}},
  "cm": {"defaultProfile": "primary", "profiles": {
    "primary": {"baseURL": "https://primary.example/v1", "model": "primary-model", "apiKey": "` + apiKey + `"},
    "work": {"baseURL": "https://work.example/v1", "model": "work-model", "apiKey": "work-api-key"}
  }}
}`
	configPath := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	environment := environmentWith(map[string]string{"HOME": home, "USERPROFILE": ""})

	output, err := runStandalone(binary, environment, "config", "cm", "use", "work")
	if err != nil || !strings.Contains(string(output), "Default CM profile set to work") || strings.Contains(string(output), apiKey) {
		t.Fatalf("config cm use = (%v, %q)", err, output)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	document := standaloneCMConfig(t, contents)
	if document.CM.DefaultProfile != "work" {
		t.Fatalf("default profile = %q, want work", document.CM.DefaultProfile)
	}
	if profile := standaloneCMProfile(t, contents, "primary"); profile.BaseURL != "https://primary.example/v1" || profile.Model != "primary-model" || profile.APIKey != apiKey {
		t.Fatalf("primary profile = %#v", profile)
	}
	if profile := standaloneCMProfile(t, contents, "work"); profile.BaseURL != "https://work.example/v1" || profile.Model != "work-model" || profile.APIKey != "work-api-key" {
		t.Fatalf("work profile = %#v", profile)
	}

	listOutput, err := runStandalone(binary, environment, "config", "cm", "list")
	if err != nil || !strings.Contains(string(listOutput), "* work") || strings.Contains(string(listOutput), apiKey) {
		t.Fatalf("config cm list after use = (%v, %q)", err, listOutput)
	}
	missingOutput, err := runStandalone(binary, environment, "config", "cm", "use", "missing")
	if err == nil || !strings.Contains(string(missingOutput), "error: CM profile not found: missing") || strings.Contains(string(missingOutput), "Default CM profile set") {
		t.Fatalf("missing profile = (%v, %q)", err, missingOutput)
	}
	helpOutput, err := runStandalone(binary, environment, "config", "cm", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "list") || !strings.Contains(string(helpOutput), "add") || !strings.Contains(string(helpOutput), "use") || !strings.Contains(string(helpOutput), "set") || !strings.Contains(string(helpOutput), "remove") || !strings.Contains(string(helpOutput), "test") {
		t.Fatalf("cm help = (%v, %q)", err, helpOutput)
	}
}

func TestConfigCMTestStandaloneBinaryUsesOnlyTheLocalProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/chat/completions" || request.Header.Get("Authorization") != "Bearer test-api-key-that-must-not-escape" {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Model == "failure-model" {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(writer, `{"error":"test-api-key-that-must-not-escape"}`)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

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
	config := `{"cm":{"defaultProfile":"work","profiles":{"work":{"baseURL":"` + server.URL + `","model":"success-model"},"failure":{"baseURL":"` + server.URL + `","model":"failure-model"}}}}`
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	const apiKey = "test-api-key-that-must-not-escape"
	environment := environmentWith(map[string]string{
		"HOME":                     home,
		"USERPROFILE":              "",
		"YCY_CM_PROFILE":           "",
		"YCY_CM_BASE_URL":          "",
		"YCY_CM_MODEL":             "",
		"YCY_CM_API_KEY":           apiKey,
		"YCY_CM_TEMPERATURE":       "",
		"YCY_CM_TIMEOUT_MS":        "",
		"YCY_CM_MAX_OUTPUT_TOKENS": "",
	})

	successOutput, err := runStandalone(binary, environment, "config", "cm", "test")
	if err != nil || !strings.Contains(string(successOutput), "Response:\nok") || !strings.Contains(string(successOutput), "Done") || strings.Contains(string(successOutput), apiKey) {
		t.Fatalf("config cm test success = (%v, %q)", err, successOutput)
	}

	failureOutput, err := runStandalone(binary, environment, "config", "cm", "test", "failure")
	if err == nil || !strings.Contains(string(failureOutput), "Provider: failure") || !strings.Contains(string(failureOutput), "Base URL: "+server.URL) || !strings.Contains(string(failureOutput), "Model: failure-model") || !strings.Contains(string(failureOutput), "error: 429 Too Many Requests") || strings.Contains(string(failureOutput), "try later") || strings.Contains(string(failureOutput), apiKey) {
		t.Fatalf("config cm test failure = (%v, %q)", err, failureOutput)
	}

	helpOutput, err := runStandalone(binary, environment, "config", "cm", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "list") || !strings.Contains(string(helpOutput), "add") || !strings.Contains(string(helpOutput), "use") || !strings.Contains(string(helpOutput), "set") || !strings.Contains(string(helpOutput), "remove") || !strings.Contains(string(helpOutput), "test") {
		t.Fatalf("cm help = (%v, %q)", err, helpOutput)
	}
}

func TestConfigCMRemoveStandaloneBinary(t *testing.T) {
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
	configPath := writeCMRemoveConfig(t, home)
	environment := environmentWith(map[string]string{"HOME": home, "USERPROFILE": ""})
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	output, err := runStandaloneWithInput(binary, environment, "yes\n", "config", "cm", "remove", "work")
	if err == nil || string(output) != "error: config cm remove requires an interactive terminal\n" {
		t.Fatalf("valid Automation removal = (%v, %q)", err, output)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("valid Automation removal changed config = (%v, %q)", err, after)
	}

	output, err = runStandaloneWithInput(binary, environment, "yes\n", "config", "cm", "remove", "missing")
	if err == nil || string(output) != "error: CM profile not found: missing\n" {
		t.Fatalf("missing Automation removal = (%v, %q)", err, output)
	}
	after, err = os.ReadFile(configPath)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("missing Automation removal changed config = (%v, %q)", err, after)
	}

	helpOutput, err := runStandalone(binary, environment, "config", "cm", "--help")
	if err != nil || !strings.Contains(string(helpOutput), "list") || !strings.Contains(string(helpOutput), "add") || !strings.Contains(string(helpOutput), "use") || !strings.Contains(string(helpOutput), "set") || !strings.Contains(string(helpOutput), "remove") || !strings.Contains(string(helpOutput), "test") {
		t.Fatalf("cm help = (%v, %q)", err, helpOutput)
	}
}

func writeCMRemoveConfig(t *testing.T, home string) string {
	t.Helper()
	directory := filepath.Join(home, ".ycy-cli")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	path := filepath.Join(directory, "config.json")
	contents := `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "fork": {"instances": {"github": {"host": "github.com", "type": "github", "token": "fork-token"}}},
  "cm": {"defaultProfile": "work", "profiles": {
    "work": {"baseURL": "https://work.example/v1", "model": "work-model", "apiKey": "work-api-key-that-must-not-escape"},
    "keep": {"baseURL": "https://keep.example/v1", "model": "keep-model", "apiKey": "keep-api-key-that-must-not-escape"}
  }}
}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return path
}
