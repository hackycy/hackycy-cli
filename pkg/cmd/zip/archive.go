package zip

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

var errNoValidArchiveFiles = errors.New("No valid files matched after filtering.")

// ArchiveEntry identifies one regular file selected for archive creation.
type ArchiveEntry struct {
	Relative string
	Absolute string
}

// CollectArchiveFiles expands patterns sequentially and preserves the first occurrence of each file.
func CollectArchiveFiles(directory string, patterns []string) ([]ArchiveEntry, error) {
	resolvedDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		patterns = []string{defaultGlobPattern}
	}
	files, err := archiveFilesUnder(resolvedDirectory)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(files))
	entries := make([]ArchiveEntry, 0, len(files))
	for _, pattern := range patterns {
		for _, entry := range files {
			if !archiveGlobMatches(pattern, entry.Relative) || seen[entry.Absolute] {
				continue
			}
			seen[entry.Absolute] = true
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// BuildZipData reads each selected file into memory and produces one complete ZIP buffer.
func BuildZipData(entries []ArchiveEntry, outputPath, withDir string) ([]byte, int, error) {
	resolvedOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return nil, 0, err
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	writer.RegisterCompressor(zip.Deflate, func(output io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(output, flate.DefaultCompression)
	})
	createdAt := time.Now()
	includedCount := 0
	for _, entry := range entries {
		if entry.Absolute == resolvedOutputPath {
			continue
		}
		contents, err := os.ReadFile(entry.Absolute)
		if err != nil {
			_ = writer.Close()
			return nil, 0, err
		}
		name := filepath.ToSlash(entry.Relative)
		if withDir != "" {
			name = withDir + "/" + name
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(createdAt)
		entryWriter, err := writer.CreateHeader(header)
		if err != nil {
			_ = writer.Close()
			return nil, 0, err
		}
		if _, err := entryWriter.Write(contents); err != nil {
			_ = writer.Close()
			return nil, 0, err
		}
		includedCount++
	}
	if includedCount == 0 {
		_ = writer.Close()
		return nil, 0, errNoValidArchiveFiles
	}
	if err := writer.Close(); err != nil {
		return nil, 0, err
	}
	return buffer.Bytes(), includedCount, nil
}

// WriteZipFile publishes a complete archive buffer directly at its destination.
func WriteZipFile(outputPath string, data []byte) error {
	return os.WriteFile(outputPath, data, 0o666)
}

func archiveFilesUnder(root string) ([]ArchiveEntry, error) {
	entries := make([]ArchiveEntry, 0)
	err := filepath.WalkDir(root, func(candidate string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if candidate != root && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			return fs.SkipDir
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 || !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		entries = append(entries, ArchiveEntry{
			Relative: filepath.ToSlash(NormalizeRelativePath(root, candidate)),
			Absolute: candidate,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func archiveGlobMatches(pattern, relative string) bool {
	pattern = strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./")
	if pattern == "" {
		return false
	}
	matched, err := doublestar.Match(pattern, relative)
	return err == nil && matched
}
