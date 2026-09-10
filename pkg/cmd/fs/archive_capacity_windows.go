//go:build windows

package fs

import "golang.org/x/sys/windows"

func defaultArchiveCapacity(path string) (ArchiveCapacity, error) {
	directory, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ArchiveCapacity{}, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(directory, &available, &total, &free); err != nil {
		return ArchiveCapacity{}, err
	}
	return ArchiveCapacity{AvailableBytes: archiveAvailableBytes(available, 1)}, nil
}
