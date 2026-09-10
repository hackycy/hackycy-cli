package env

import "path/filepath"

// FileWriter publishes exported JSON to an output file.
type FileWriter interface {
	WriteFile(path string, content []byte) error
}

// WriteOutput resolves an output target from the working directory and overwrites it.
func WriteOutput(workingDirectory, target, content string, writer FileWriter) error {
	return writer.WriteFile(resolveOutputPath(workingDirectory, target), []byte(content))
}

func resolveOutputPath(workingDirectory, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Join(workingDirectory, target)
}
