//go:build windows

package filesession

import (
	"os"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/windowsacl"
)

func assertPrivatePath(t *testing.T, path string, _ os.FileMode) {
	t.Helper()
	if err := windowsacl.VerifyPrivatePath(path); err != nil {
		t.Fatalf("verify private Windows DACL for %s: %v", path, err)
	}
}
