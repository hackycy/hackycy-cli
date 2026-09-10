package fork

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const tarBlockSize = 512

// ArchiveEntryType is the limited TAR entry classification observed in the frozen implementation.
type ArchiveEntryType string

const (
	archiveFile      ArchiveEntryType = "file"
	archiveDirectory ArchiveEntryType = "directory"
	archiveOther     ArchiveEntryType = "other"
)

// ArchiveEntry is one uncompressed TAR entry after the legacy parser's limited normalization.
type ArchiveEntry struct {
	Name string
	Size int
	Type ArchiveEntryType
	Data []byte
}

// ArchiveExtractor is Git Fork's command-owned archive publication boundary.
type ArchiveExtractor interface {
	Extract(string, []byte) error
}

// OSArchiveExtractor publishes archives through the local filesystem.
type OSArchiveExtractor struct{}

// Extract publishes one compressed archive using the legacy extraction behavior.
func (OSArchiveExtractor) Extract(destination string, compressed []byte) error {
	return ExtractArchive(destination, compressed)
}

// ParseArchive decompresses a gzip TAR fully in memory and applies the legacy limited TAR parser.
func ParseArchive(compressed []byte) ([]ArchiveEntry, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	decompressed, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return parseLegacyTar(decompressed), nil
}

// ExtractArchive writes the parsed archive with the legacy strip-one and entry-type behavior.
func ExtractArchive(destination string, compressed []byte) error {
	entries, err := ParseArchive(compressed)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}

	var wait sync.WaitGroup
	var firstErr error
	var lock sync.Mutex
	for _, entry := range entries {
		stripped, include := stripArchiveTopLevel(entry.Name)
		if !include {
			continue
		}
		path := filepath.Join(destination, stripped)
		switch entry.Type {
		case archiveDirectory:
			wait.Add(1)
			go func() {
				defer wait.Done()
				recordArchiveWriteError(&lock, &firstErr, os.MkdirAll(path, 0o755))
			}()
		case archiveFile:
			data := entry.Data
			wait.Add(1)
			go func() {
				defer wait.Done()
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					recordArchiveWriteError(&lock, &firstErr, err)
					return
				}
				recordArchiveWriteError(&lock, &firstErr, os.WriteFile(path, data, 0o666))
			}()
		}
	}
	wait.Wait()
	return firstErr
}

func parseLegacyTar(data []byte) []ArchiveEntry {
	entries := make([]ArchiveEntry, 0)
	offset := 0
	longName := ""
	hasLongName := false
	for offset+tarBlockSize <= len(data) {
		if zeroTarBlock(data, offset) {
			if offset+2*tarBlockSize <= len(data) && zeroTarBlock(data, offset+tarBlockSize) {
				break
			}
			offset += tarBlockSize
			continue
		}

		header := data[offset : offset+tarBlockSize]
		size, valid := parseLegacyOctal(tarString(header, 124, 12))
		if !valid {
			break
		}
		if header[156] == 'L' {
			offset += tarBlockSize
			longName = strings.TrimRight(string(tarData(data, offset, size)), "\x00")
			hasLongName = true
			offset += tarPaddedSize(size)
			continue
		}

		name := tarString(header, 0, 100)
		if hasLongName {
			name = longName
			hasLongName = false
		} else if prefix := tarString(header, 345, 155); prefix != "" {
			name = prefix + "/" + name
		}
		entryType := archiveOther
		if header[156] == '5' || strings.HasSuffix(name, "/") {
			entryType = archiveDirectory
		} else if header[156] == '0' || header[156] == 0 {
			entryType = archiveFile
		}
		offset += tarBlockSize
		entries = append(entries, ArchiveEntry{
			Name: name,
			Size: size,
			Type: entryType,
			Data: tarData(data, offset, size),
		})
		offset += tarPaddedSize(size)
	}
	return entries
}

func zeroTarBlock(data []byte, offset int) bool {
	for index := offset; index < offset+tarBlockSize; index++ {
		if data[index] != 0 {
			return false
		}
	}
	return true
}

func tarString(header []byte, offset, length int) string {
	end := offset
	maximum := offset + length
	for end < maximum && header[end] != 0 {
		end++
	}
	return string(header[offset:end])
}

func parseLegacyOctal(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, true
	}
	valueEnd := 0
	for valueEnd < len(trimmed) && trimmed[valueEnd] >= '0' && trimmed[valueEnd] <= '7' {
		valueEnd++
	}
	if valueEnd == 0 {
		return 0, false
	}
	parsed, err := strconv.ParseInt(trimmed[:valueEnd], 8, 0)
	if err != nil {
		return 0, false
	}
	return int(parsed), true
}

func tarData(data []byte, offset, size int) []byte {
	if offset < 0 || offset >= len(data) || size <= 0 {
		return nil
	}
	end := offset + size
	if end < offset || end > len(data) {
		end = len(data)
	}
	return data[offset:end]
}

func tarPaddedSize(size int) int {
	if size <= 0 {
		return 0
	}
	return ((size + tarBlockSize - 1) / tarBlockSize) * tarBlockSize
}

func stripArchiveTopLevel(name string) (string, bool) {
	index := strings.IndexByte(name, '/')
	if index < 0 || index == len(name)-1 {
		return "", false
	}
	return name[index+1:], true
}

func recordArchiveWriteError(lock *sync.Mutex, destination *error, err error) {
	if err == nil {
		return
	}
	lock.Lock()
	defer lock.Unlock()
	if *destination == nil {
		*destination = err
	}
}
