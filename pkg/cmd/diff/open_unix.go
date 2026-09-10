//go:build darwin || linux

package diff

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openComparisonFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open comparison file %q: unable to wrap file descriptor", path)
	}
	return file, nil
}
