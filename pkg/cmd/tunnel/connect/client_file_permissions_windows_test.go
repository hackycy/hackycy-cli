//go:build windows

package connect

import (
	"os"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/windowsacl"
)

func assertClientPrivateFile(t *testing.T, path string, _ os.FileMode) {
	t.Helper()
	if err := windowsacl.VerifyPrivatePath(path); err != nil {
		t.Fatalf("verify private Windows DACL for %s: %v", path, err)
	}
}
