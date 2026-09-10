//go:build !windows

package fs

import "os"

func openWorkspaceRoot(directory string) (workspaceRoot, error) {
	return os.OpenRoot(directory)
}
