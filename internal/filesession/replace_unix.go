//go:build !windows

package filesession

import "os"

func replaceSessionFile(candidate, target string) error {
	return os.Rename(candidate, target)
}
