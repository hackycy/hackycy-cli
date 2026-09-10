//go:build windows

package updater

import (
	"path/filepath"
	"testing"
)

func TestWindowsTransactionPathsKeepExecutableSuffix(t *testing.T) {
	target := filepath.Join(t.TempDir(), "ycy.exe")
	if got, want := transactionBinaryPath(target, ".new.", "tx"), filepath.Join(filepath.Dir(target), "ycy.new.tx.exe"); got != want {
		t.Fatalf("staged path = %q, want %q", got, want)
	}
	if got, want := transactionBinaryPath(target, ".backup.", "tx"), filepath.Join(filepath.Dir(target), "ycy.backup.tx.exe"); got != want {
		t.Fatalf("backup path = %q, want %q", got, want)
	}
	if got, want := updaterBinaryPath(filepath.Dir(target), "tx"), filepath.Join(filepath.Dir(target), "ycy-updater-tx.exe"); got != want {
		t.Fatalf("updater path = %q, want %q", got, want)
	}
}
