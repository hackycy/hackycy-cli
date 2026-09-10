package pulse

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// PathStater is the command-owned filesystem boundary for validating a workspace root.
type PathStater interface {
	Stat(string) (fs.FileInfo, error)
}

func resolvePulseRoot(workingDirectory, directory string, statter PathStater) (string, error) {
	workingDirectory, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", err
	}
	root := workingDirectory
	if directory != "" {
		if filepath.IsAbs(directory) {
			root = filepath.Clean(directory)
		} else {
			root = filepath.Join(workingDirectory, directory)
		}
	}

	info, err := statter.Stat(root)
	if err != nil {
		return "", fmt.Errorf("Directory not found: %s", root)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("Path is not a directory: %s", root)
	}
	return root, nil
}
