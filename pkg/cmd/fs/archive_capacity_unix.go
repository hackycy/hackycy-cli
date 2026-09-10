//go:build !windows

package fs

import (
	"syscall"
)

func defaultArchiveCapacity(path string) (ArchiveCapacity, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return ArchiveCapacity{}, err
	}
	return ArchiveCapacity{AvailableBytes: archiveAvailableBytes(uint64(stat.Bavail), uint64(stat.Bsize)), FreeEntries: int64(stat.Ffree)}, nil
}
