package fork

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxArchiveBytes   = int64(1 << 30)
	maxArchiveEntries = 50_000
)

// ArchiveExtractor is Git Fork's command-owned archive publication boundary.
type ArchiveExtractor interface {
	Extract(string, []byte) error
}

// OSArchiveExtractor publishes archives through the local filesystem.
type OSArchiveExtractor struct{}

func (OSArchiveExtractor) Extract(destination string, compressed []byte) error {
	return ExtractArchive(destination, compressed)
}

// ExtractArchive strips one leading directory and extracts regular files and
// directories. Link entries are ignored; other unsupported types are rejected.
func ExtractArchive(destination string, compressed []byte) error {
	return extractArchive(destination, compressed, maxArchiveBytes, maxArchiveEntries)
}

func extractArchive(destination string, compressed []byte, byteLimit int64, entryLimit int) error {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()

	limited := &io.LimitedReader{R: reader, N: byteLimit + 1}
	archive := tar.NewReader(limited)
	for count := 0; ; count++ {
		header, err := archive.Next()
		if limited.N == 0 {
			return fmt.Errorf("archive exceeds the %d-byte uncompressed limit", byteLimit)
		}
		if errors.Is(err, io.EOF) {
			// TAR's end marker can precede the gzip trailer. Drain within the same
			// limit to verify the checksum and bound trailing decompressed data.
			_, err = io.Copy(io.Discard, limited)
			if limited.N == 0 {
				return fmt.Errorf("archive exceeds the %d-byte uncompressed limit", byteLimit)
			}
			return err
		}
		if err != nil {
			return fmt.Errorf("read archive entry: %w", err)
		}
		if count >= entryLimit {
			return fmt.Errorf("archive exceeds the %d-entry limit", entryLimit)
		}
		if header.Size >= limited.N {
			return fmt.Errorf("archive exceeds the %d-byte uncompressed limit", byteLimit)
		}
		name, include, err := archiveRelativePath(header.Name)
		if err != nil {
			return err
		}
		if !include {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(file, archive)
			if err == nil {
				err = file.Chmod(os.FileMode(header.Mode) & 0o777)
			}
			if err := errors.Join(err, file.Close()); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			continue
		default:
			return fmt.Errorf("unsupported archive entry type %d", header.Typeflag)
		}
	}
}

func archiveRelativePath(name string) (string, bool, error) {
	if err := validateArchivePath(name); err != nil {
		return "", false, err
	}
	index := strings.IndexByte(name, '/')
	if index < 0 || index == len(name)-1 {
		return "", false, nil
	}
	stripped := name[index+1:]
	if err := validateArchivePath(stripped); err != nil {
		return "", false, err
	}
	return filepath.FromSlash(stripped), true, nil
}

func validateArchivePath(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\:\x00") || !filepath.IsLocal(name) {
		return fmt.Errorf("invalid archive path %q", name)
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." {
			return fmt.Errorf("invalid archive path %q", name)
		}
	}
	return nil
}
