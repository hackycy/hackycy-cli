//go:build !windows

package server

import (
	"os"
	"testing"
)

func assertTunnelPrivateFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != mode.Perm() {
		t.Fatalf("private file mode for %s = (%v, %v), want %o", path, info, err, mode.Perm())
	}
}
