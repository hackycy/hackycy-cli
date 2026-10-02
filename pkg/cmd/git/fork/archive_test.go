package fork

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExtractArchiveStripsOneAndPreservesFilesAndModes(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	archive := tarFixture(t,
		tar.Header{Name: "root/bin/", Typeflag: tar.TypeDir, Mode: 0o755},
		tar.Header{Name: "root/bin/run", Mode: 0o6755},
		tar.Header{Name: "root/link", Typeflag: tar.TypeSymlink, Linkname: "bin/run"},
		tar.Header{Name: "root/hard-link", Typeflag: tar.TypeLink, Linkname: "root/bin/run"},
		tar.Header{Name: "top-level-only"},
	)
	if err := ExtractArchive(destination, gzipArchive(t, archive)); err != nil {
		t.Fatalf("ExtractArchive() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "bin", "run")); err != nil || string(got) != "contents" {
		t.Fatalf("extracted run = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(destination, "bin", "run"))
		if err != nil || info.Mode().Perm() != 0o755 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
			t.Fatalf("executable permissions were not preserved safely: %v, %v", info, err)
		}
	}
	for _, name := range []string{"link", "hard-link", "top-level-only"} {
		if _, err := os.Lstat(filepath.Join(destination, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ignored entry %s was extracted: %v", name, err)
		}
	}
}

func TestExtractArchiveSupportsPAXAndGNULongPaths(t *testing.T) {
	for _, format := range []tar.Format{tar.FormatPAX, tar.FormatGNU} {
		t.Run(format.String(), func(t *testing.T) {
			root := t.TempDir()
			name := strings.Repeat("long/", 30) + "file.txt"
			archive := gzipArchive(t, tarFixture(t, tar.Header{Name: "root/" + name, Format: format}))
			if err := ExtractArchive(root, archive); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name))); err != nil || string(got) != "contents" {
				t.Fatalf("long path contents = %q, %v", got, err)
			}
		})
	}
}

func TestExtractArchiveRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{
		"root/../outside.txt", "../root/file", "/root/file", "root//outside.txt",
		"C:/root/file", "root/C:/file", `root/..\outside.txt`, `\\server\share\file`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(root, "outside.txt")
			if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			archive := gzipArchive(t, tarFixture(t, tar.Header{Name: name}))
			if err := ExtractArchive(filepath.Join(root, "destination"), archive); err == nil {
				t.Fatal("unsafe archive path was accepted")
			}
			if got, err := os.ReadFile(outside); err != nil || string(got) != "unchanged" {
				t.Fatalf("outside file changed: %q, %v", got, err)
			}
		})
	}
}

func TestExtractArchiveRejectsExistingSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{destination, outside} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(destination, "link")); err != nil {
		t.Fatal(err)
	}
	archive := gzipArchive(t, tarFixture(t, tar.Header{Name: "root/link/file.txt"}))
	if err := ExtractArchive(destination, archive); err == nil {
		t.Fatal("symlink escape was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestExtractArchiveDoesNotOverwriteExistingFileOrHardLink(t *testing.T) {
	for _, hardLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "hard link"}[hardLink], func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "destination")
			if err := os.Mkdir(destination, 0o755); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(root, "outside.txt")
			if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(destination, "file.txt")
			if hardLink {
				if err := os.Link(outside, target); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ExtractArchive(destination, gzipArchive(t, tarFixture(t, tar.Header{Name: "root/file.txt"}))); err == nil {
				t.Fatal("existing file was overwritten")
			}
			for _, path := range []string{outside, target} {
				if got, err := os.ReadFile(path); err != nil || string(got) != "unchanged" {
					t.Fatalf("existing file changed: %q, %v", got, err)
				}
			}
		})
	}
}

func TestExtractArchiveRejectsMalformedAndUnsupportedInput(t *testing.T) {
	valid := tarFixture(t, tar.Header{Name: "root/file.txt"})
	badHeader := bytes.Clone(valid)
	badHeader[0] ^= 1
	badChecksum := gzipArchive(t, valid)
	badChecksum[len(badChecksum)-8] ^= 1
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"not gzip", []byte("not gzip")},
		{"short header", gzipArchive(t, []byte("not a complete tar block"))},
		{"header checksum", gzipArchive(t, badHeader)},
		{"short body", gzipArchive(t, valid[:512+2])},
		{"gzip checksum", badChecksum},
		{"device", gzipArchive(t, tarFixture(t, tar.Header{Name: "root/device", Typeflag: tar.TypeChar}))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ExtractArchive(t.TempDir(), test.data); err == nil {
				t.Fatal("invalid archive was accepted")
			}
		})
	}
}

func TestExtractArchiveBoundsEntriesAndAllDecompressedBytes(t *testing.T) {
	archive := tarFixture(t, tar.Header{Name: "root/file.txt"})
	for _, test := range []struct {
		name    string
		data    []byte
		bytes   int64
		entries int
		want    string
	}{
		{"exact limits", archive, int64(len(archive)), 1, ""},
		{"entry limit", archive, int64(len(archive)), 0, "entry limit"},
		{"body limit", archive, 513, 1, "uncompressed limit"},
		{"padding limit", archive, 520, 1, "uncompressed limit"},
		{"end marker limit", archive, int64(len(archive) - 1), 1, "uncompressed limit"},
		{"trailing data limit", append(bytes.Clone(archive), make([]byte, 1024)...), int64(len(archive)), 1, "uncompressed limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := extractArchive(t.TempDir(), gzipArchive(t, test.data), test.bytes, test.entries)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
		})
	}
}

func TestExtractArchiveReportsDestinationWriteFailures(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "destination")
	if err := os.WriteFile(destination, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := gzipArchive(t, tarFixture(t, tar.Header{Name: "root/file.txt"}))
	if err := ExtractArchive(destination, archive); err == nil {
		t.Fatal("destination write failure was accepted")
	}
}

func tarFixture(t *testing.T, headers ...tar.Header) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, header := range headers {
		if header.Mode == 0 {
			header.Mode = 0o644
		}
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			header.Size = int64(len("contents"))
		}
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := io.WriteString(writer, "contents"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func gzipArchive(t *testing.T, contents []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
