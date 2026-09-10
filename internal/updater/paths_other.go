//go:build !windows

package updater

import "path/filepath"

func transactionBinaryPath(targetPath, marker, transactionID string) string {
	return filepath.Join(filepath.Dir(targetPath), filepath.Base(targetPath)+marker+transactionID)
}

func updaterBinaryPath(directory, transactionID string) string {
	return filepath.Join(directory, "ycy-updater-"+transactionID)
}
