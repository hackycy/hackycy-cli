//go:build !windows

package updater

import "os"

func protectUpgradePath(path string, mode os.FileMode, chmod func(string, os.FileMode) error) error {
	return chmod(path, mode)
}
