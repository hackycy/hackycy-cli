package updater

import (
	"path/filepath"
	"runtime"
	"strings"
)

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
