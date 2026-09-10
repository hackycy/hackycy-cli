//go:build !windows

package updater

import "os"

func replaceStateFile(candidate, target string) error {
	return os.Rename(candidate, target)
}
