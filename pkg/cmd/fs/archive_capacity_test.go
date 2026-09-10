package fs

import "testing"

func TestRequireArchiveCapacityReservesBytesAndEntries(t *testing.T) {
	for _, test := range []struct {
		name       string
		inspection ArchiveInspection
		capacity   ArchiveCapacity
		code       string
		message    string
	}{
		{name: "fits exactly after byte reserve", inspection: ArchiveInspection{UncompressedBytes: 900, EntryCount: 8}, capacity: ArchiveCapacity{AvailableBytes: 1000, FreeEntries: 10}},
		{name: "byte reserve is capped", inspection: ArchiveInspection{UncompressedBytes: 19*archiveByteReserveCap - archiveByteReserveCap}, capacity: ArchiveCapacity{AvailableBytes: 19 * archiveByteReserveCap}},
		{name: "does not fit after byte reserve", inspection: ArchiveInspection{UncompressedBytes: 901}, capacity: ArchiveCapacity{AvailableBytes: 1000}, code: "INSUFFICIENT_SPACE", message: "Archive does not fit in the available disk space"},
		{name: "does not fit after entry reserve", inspection: ArchiveInspection{EntryCount: 10}, capacity: ArchiveCapacity{AvailableBytes: 1000, FreeEntries: 10}, code: "INSUFFICIENT_SPACE", message: "Archive does not fit in the available filesystem entries"},
		{name: "unreported entries are ignored", inspection: ArchiveInspection{EntryCount: 1}, capacity: ArchiveCapacity{AvailableBytes: 1000, FreeEntries: 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := requireArchiveCapacity(test.inspection, test.capacity)
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !serviceErrorIs(err, test.code) {
				t.Fatalf("requireArchiveCapacity() error = %v, want %s", err, test.code)
			}
			if err.(*ServiceError).Message != test.message {
				t.Fatalf("message = %q, want %q", err.(*ServiceError).Message, test.message)
			}
		})
	}
}

func TestArchiveAvailableBytesClampsToTheSafeIntegerLimit(t *testing.T) {
	if got := archiveAvailableBytes(uint64(maxArchiveSafeInteger), 2); got != maxArchiveSafeInteger {
		t.Fatalf("archiveAvailableBytes() = %d, want %d", got, maxArchiveSafeInteger)
	}
	if got := archiveAvailableBytes(0, 4096); got != 0 {
		t.Fatalf("archiveAvailableBytes() = %d, want 0", got)
	}
}
