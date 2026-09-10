//go:build !windows

package tunnelruntime

import "os"

// ProtectPrivateFile restricts one Tunnel runtime file to its owner.
func ProtectPrivateFile(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
