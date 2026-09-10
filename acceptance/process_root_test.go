//go:build acceptance

package acceptance

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneBinaryDispatchesThumbnailWorkerBeforeCobra(t *testing.T) {
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

	command := exec.Command(resolveStandaloneBinary(binary), "--internal-thumbnail-worker")
	command.Env = environmentWith(map[string]string{})
	command.Stdin = bytes.NewReader([]byte{0, 0, 0, 1})
	output, err := command.CombinedOutput()
	if exitCode(err) != 1 || !strings.Contains(string(output), "thumbnail worker:") {
		t.Fatalf("thumbnail worker dispatch = (%v, %q), want exit 1 and worker error", err, output)
	}
	if len(output) == 0 || !bytes.Contains(output, []byte("thumbnail worker error:")) {
		t.Fatalf("thumbnail worker error prefix missing: %q", output)
	}
}
