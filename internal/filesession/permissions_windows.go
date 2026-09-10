//go:build windows

package filesession

import (
	"os"

	"github.com/hackycy/hackycy-cli/internal/windowsacl"
)

func protectSessionPath(path string, _ os.FileMode) error {
	return windowsacl.RestrictPrivatePath(path)
}
