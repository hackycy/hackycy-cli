package fork

import (
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestParseArchiveMatchesTheLegacyTarShapes(t *testing.T) {
	longName := "root/very/long/path/to/file.txt"
	archive := gzipArchive(t, appendTarRecords(
		tarRecord("root/normal.txt", '0', []byte("normal")),
		tarRecord("././@LongLink", 'L', append([]byte(longName), 0)),
		tarRecord("ignored", '0', []byte("long-name")),
		tarRecord("pax-header", 'x', []byte("path=root/from-pax.txt\n")),
		tarRecord("root/not-from-pax.txt", '0', []byte("pax-ignored")),
		tarRecord("root/dir/", '5', nil),
		tarRecord("root/link", '2', []byte("ignored-link")),
	))

	entries, err := ParseArchive(archive)
	if err != nil {
		t.Fatalf("ParseArchive() error = %v", err)
	}
	if got, want := entryNames(entries), []string{
		"root/normal.txt",
		longName,
		"pax-header",
		"root/not-from-pax.txt",
		"root/dir/",
		"root/link",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entry names = %#v, want %#v", got, want)
	}
	if entries[2].Type != archiveOther || entries[3].Name != "root/not-from-pax.txt" {
		t.Fatalf("PAX handling = %#v, want an ignored PAX entry and unchanged following name", entries[2:4])
	}
	if entries[4].Type != archiveDirectory || entries[5].Type != archiveOther {
		t.Fatalf("entry types = %#v", entries[4:])
	}
}

func TestParseArchivePreservesTheLegacyTruncatedTarOutcome(t *testing.T) {
	entries, err := ParseArchive(gzipArchive(t, []byte("not a complete tar block")))
	if err != nil {
		t.Fatalf("ParseArchive() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ParseArchive() entries = %#v, want none", entries)
	}

	if _, err := ParseArchive([]byte("not gzip")); err == nil {
		t.Fatal("ParseArchive() error = nil, want gzip error")
	}
}

func TestExtractArchiveUsesStripOneAndRetainsObservedUnsafePathAndModeBehavior(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	archive := gzipArchive(t, appendTarRecords(
		tarRecord("root/bin/", '5', nil),
		tarRecord("root/bin/run", '0', []byte("run")),
		tarRecord("root/../outside.txt", '0', []byte("escaped")),
		tarRecord("root/link", '2', []byte("ignored")),
		tarRecord("top-level-only", '0', []byte("ignored")),
	))

	if err := ExtractArchive(destination, archive); err != nil {
		t.Fatalf("ExtractArchive() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "bin", "run")); err != nil || string(got) != "run" {
		t.Fatalf("extracted run = %q, %v", got, err)
	}
	info, err := os.Stat(filepath.Join(destination, "bin", "run"))
	if err != nil {
		t.Fatalf("stat extracted run: %v", err)
	}
	if info.Mode()&0o100 != 0 {
		t.Fatalf("extracted mode = %o, want no executable mode preservation", info.Mode())
	}
	if got, err := os.ReadFile(filepath.Join(root, "outside.txt")); err != nil || string(got) != "escaped" {
		t.Fatalf("escaped file = %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link entry was extracted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "top-level-only")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("top-level-only entry was extracted: %v", err)
	}
}

func TestExtractArchiveReportsDestinationWriteFailures(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	if err := os.WriteFile(destination, []byte("file"), 0o600); err != nil {
		t.Fatalf("create destination file: %v", err)
	}
	archive := gzipArchive(t, tarRecord("root/file.txt", '0', []byte("contents")))
	if err := ExtractArchive(destination, archive); err == nil {
		t.Fatal("ExtractArchive() error = nil, want destination write failure")
	}
}

func entryNames(entries []ArchiveEntry) []string {
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name
	}
	return names
}

func gzipArchive(t *testing.T, contents []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(contents); err != nil {
		t.Fatalf("write gzip: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return compressed.Bytes()
}

func appendTarRecords(records ...[]byte) []byte {
	result := make([]byte, 0)
	for _, record := range records {
		result = append(result, record...)
	}
	return append(result, make([]byte, 2*tarBlockSize)...)
}

func tarRecord(name string, typeFlag byte, data []byte) []byte {
	header := make([]byte, tarBlockSize)
	copy(header[0:100], []byte(name))
	copy(header[124:136], []byte(octalField(len(data))))
	header[156] = typeFlag
	result := append(header, data...)
	padded := tarPaddedSize(len(data))
	return append(result, make([]byte, padded-len(data))...)
}

func octalField(value int) string {
	return "00000000000"[:11-len(strconv.FormatInt(int64(value), 8))] + strconv.FormatInt(int64(value), 8) + "\x00"
}
