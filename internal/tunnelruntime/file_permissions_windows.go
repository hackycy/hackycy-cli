//go:build windows

package tunnelruntime

import (
	"os"

	"github.com/hackycy/hackycy-cli/internal/windowsacl"
)

// ProtectPrivateFile restricts one Tunnel runtime file to its owner.
func ProtectPrivateFile(path string, _ os.FileMode) error {
	return windowsacl.RestrictPrivatePath(path)
}
