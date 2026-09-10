//go:build !windows

package connect

import (
	"os"
	"testing"
)

func assertClientPrivateFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != mode.Perm() {
		t.Fatalf("private file mode for %s = (%v, %v), want %o", path, info, err, mode.Perm())
	}
}
