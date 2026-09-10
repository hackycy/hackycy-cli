//go:build !windows

package filesession

import "os"

func protectSessionPath(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
