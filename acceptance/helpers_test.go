//go:build acceptance

package acceptance

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine acceptance repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func standaloneBinaryOutputPath(binary string) string {
	if runtime.GOOS == "windows" && filepath.Ext(binary) == "" {
		return binary + ".exe"
	}
	return binary
}

func resolveStandaloneBinary(binary string) string {
	if _, err := os.Stat(binary); err == nil {
		return binary
	}
	if runtime.GOOS != "windows" || filepath.Ext(binary) != "" {
		return binary
	}
	withSuffix := binary + ".exe"
	if _, err := os.Stat(withSuffix); err == nil {
		return withSuffix
	}
	return binary
}

func TestResolveStandaloneBinaryUsesWindowsExecutableSuffix(t *testing.T) {
	directory := t.TempDir()
	requested := filepath.Join(directory, "ycy")
	actual := standaloneBinaryOutputPath(requested)
	if err := os.WriteFile(actual, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	if got := resolveStandaloneBinary(actual); got != actual {
		t.Fatalf("resolveStandaloneBinary(%q) = %q, want %q", actual, got, actual)
	}
	if got := standaloneBinaryOutputPath(requested); got != actual {
		t.Fatalf("standaloneBinaryOutputPath(%q) = %q, want %q", requested, got, actual)
	}
}

func environmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return exited.ExitCode()
	}
	return -1
}

func nativeTestExecutablePath(path string) string {
	if runtime.GOOS != "windows" || strings.EqualFold(filepath.Ext(path), ".exe") {
		return path
	}
	return path + ".exe"
}

func expectedTransactionPath(targetPath, marker, transactionID string) string {
	if runtime.GOOS != "windows" {
		return filepath.Join(filepath.Dir(targetPath), filepath.Base(targetPath)+marker+transactionID)
	}
	name := filepath.Base(targetPath)
	extension := filepath.Ext(name)
	if !strings.EqualFold(extension, ".exe") {
		extension = ".exe"
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return filepath.Join(filepath.Dir(targetPath), base+marker+transactionID+extension)
}

func expectedUpdaterPath(directory, transactionID string) string {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	return filepath.Join(directory, "ycy-updater-"+transactionID+suffix)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runStandalone(binary string, environment []string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Env = environment
	return command.CombinedOutput()
}

func runStandaloneWithInput(binary string, environment []string, input string, arguments ...string) ([]byte, error) {
	command := exec.Command(resolveStandaloneBinary(binary), arguments...)
	command.Env = environment
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}
