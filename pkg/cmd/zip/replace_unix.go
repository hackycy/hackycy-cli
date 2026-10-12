//go:build !windows

package zip

import "os"

func replaceZipFile(candidate, target string) error {
	return os.Rename(candidate, target)
}
