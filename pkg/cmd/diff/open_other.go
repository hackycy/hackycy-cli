//go:build !darwin && !linux && !windows

package diff

import "os"

func openComparisonFile(path string) (*os.File, error) {
	return os.Open(path)
}
