//go:build windows

package server

import (
	"os"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/windowsacl"
)

func assertTunnelPrivateFile(t *testing.T, path string, _ os.FileMode) {
	t.Helper()
	if err := windowsacl.VerifyPrivatePath(path); err != nil {
		t.Fatalf("verify private Windows DACL for %s: %v", path, err)
	}
}
