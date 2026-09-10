//go:build !windows

package appconfig

import "os"

func replaceConfigFile(candidate, target string) error {
	return os.Rename(candidate, target)
}
