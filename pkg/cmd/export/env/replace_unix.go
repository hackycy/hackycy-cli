//go:build !windows

package env

import "os"

func replaceExportEnvFile(candidate, target string) error {
	return os.Rename(candidate, target)
}
