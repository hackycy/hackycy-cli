package fs

const (
	archiveByteReserveCap  = int64(1 << 30)
	archiveEntryReserveCap = int64(1024)
)

type ArchiveCapacity struct {
	AvailableBytes int64
	FreeEntries    int64
}

func archiveAvailableBytes(blocks, blockSize uint64) int64 {
	if blockSize == 0 || blocks == 0 {
		return 0
	}
	if blocks > uint64(maxArchiveSafeInteger)/blockSize {
		return maxArchiveSafeInteger
	}
	return int64(blocks * blockSize)
}

func requireArchiveCapacity(inspection ArchiveInspection, capacity ArchiveCapacity) error {
	reservedBytes := min(archiveByteReserveCap, capacity.AvailableBytes/10)
	if inspection.UncompressedBytes > capacity.AvailableBytes-reservedBytes {
		return &ServiceError{Code: "INSUFFICIENT_SPACE", Message: "Archive does not fit in the available disk space"}
	}
	if capacity.FreeEntries > 0 {
		reservedEntries := min(archiveEntryReserveCap, capacity.FreeEntries/10)
		if inspection.EntryCount > capacity.FreeEntries-reservedEntries {
			return &ServiceError{Code: "INSUFFICIENT_SPACE", Message: "Archive does not fit in the available filesystem entries"}
		}
	}
	return nil
}
