//go:build windows

package updater

import (
	"path/filepath"
	"strings"
)

func transactionBinaryPath(targetPath, marker, transactionID string) string {
	name := filepath.Base(targetPath)
	extension := filepath.Ext(name)
	if !strings.EqualFold(extension, ".exe") {
		extension = ".exe"
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return filepath.Join(filepath.Dir(targetPath), base+marker+transactionID+extension)
}

func updaterBinaryPath(directory, transactionID string) string {
	return filepath.Join(directory, "ycy-updater-"+transactionID+".exe")
}
